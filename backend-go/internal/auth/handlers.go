package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

type ctxKey int

const userIDKey ctxKey = iota

// UserID 从请求上下文取当前用户 id（RequireAuth 之后可用）。
func UserID(r *http.Request) string {
	v, _ := r.Context().Value(userIDKey).(string)
	return v
}

type Handlers struct {
	Svc    *Service
	Issuer *TokenIssuer
	// MediaBase 头像等媒体文件的绝对地址前缀（并跑期由 Django 提供 /media/）
	MediaBase string
}

// RequireAuth Bearer 校验中间件：任何原生路由的鉴权入口。
func (h *Handlers) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if raw == "" || raw == r.Header.Get("Authorization") {
			httpx.Err(w, http.StatusUnauthorized, "NOT_AUTHENTICATED", "未提供有效凭证")
			return
		}
		claims, err := h.Issuer.Parse(raw, "access")
		if err != nil {
			httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "凭证无效或已过期")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userIDKey, claims.UserID)))
	})
}

// POST /api/v1/auth/token —— 对齐 simplejwt TokenObtainPairView
func (h *Handlers) Token(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
		httpx.Err(w, http.StatusBadRequest, "VALIDATION", "请提供用户名与密码")
		return
	}
	u, err := h.Svc.Authenticate(r.Context(), body.Username, body.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			httpx.Err(w, http.StatusUnauthorized, "AUTH_FAILED", "用户名或密码错误")
			return
		}
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "登录服务暂不可用")
		return
	}
	access, refresh, err := h.Issuer.IssuePair(u.ID)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "签发凭证失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"access": access, "refresh": refresh})
}

// POST /api/v1/auth/token/refresh —— 对齐 simplejwt TokenRefreshView（ROTATE_REFRESH_TOKENS）
func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) {
	var body struct{ Refresh string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Refresh == "" {
		httpx.Err(w, http.StatusBadRequest, "VALIDATION", "请提供 refresh token")
		return
	}
	claims, err := h.Issuer.Parse(body.Refresh, "refresh")
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "refresh token 无效或已过期")
		return
	}
	access, refresh, err := h.Issuer.IssuePair(claims.UserID)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "签发凭证失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"access": access, "refresh": refresh})
}

// GET /api/v1/auth/me —— 对齐 iam.views.MeView 的字段契约
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	u, err := h.Svc.UserByID(r.Context(), UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	roles, roleNames, perms, err := h.Svc.RolesAndPerms(r.Context(), u)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取角色失败")
		return
	}
	var avatarURL any
	if u.Avatar != "" {
		avatarURL = h.MediaBase + "/media/" + u.Avatar
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"id": u.ID, "username": u.Username, "nickname": u.Nickname,
		"phone": u.Phone, "email": u.Email, "avatar_url": avatarURL,
		"preferences": u.Preferences, "is_staff": u.IsStaff, "is_superuser": u.IsSuperuser,
		"organization_id": u.OrgID, "organization_name": u.OrgName,
		"date_joined": u.DateJoined, "last_login": u.LastLogin,
		"roles": roles, "role_names": roleNames, "permissions": perms,
	})
}
