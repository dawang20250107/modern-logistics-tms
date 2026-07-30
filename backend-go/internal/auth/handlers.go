package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// MediaRoot 媒体文件落盘根目录（对齐 Django 的 MEDIA_ROOT）
	MediaRoot string
	// Debug 对齐 settings.DEBUG：仅调试期在找回密码响应里附 dev_code
	Debug bool
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

// POST /api/v1/auth/token —— 对齐 iam.auth_views.AuditedTokenObtainPairView：
// 失败锁定前置、失败分类（凭据错误 / 账号停用）、每次尝试写审计流水。
func (h *Handlers) Token(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct{ Username, Password string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	details := map[string]any{}
	if body.Username == "" {
		details["username"] = []string{"该字段是必填项。"}
	}
	if body.Password == "" {
		details["password"] = []string{"该字段是必填项。"}
	}
	if len(details) > 0 {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
		return
	}
	if IsLocked(body.Username) {
		h.rejectLocked(ctx, w, r, body.Username)
		return
	}
	u, err := h.Svc.Authenticate(ctx, body.Username, body.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			RecordAttempt(ctx, h.Svc.DB, r, body.Username, "", h.classifyFailure(ctx, body.Username), false)
			if RegisterFailure(body.Username) {
				h.rejectLocked(ctx, w, r, body.Username)
				return
			}
			httpx.Err(w, http.StatusUnauthorized, "authentication_failed",
				"No active account found with the given credentials")
			return
		}
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "登录服务暂不可用")
		return
	}
	ClearFailures(body.Username)
	RecordAttempt(ctx, h.Svc.DB, r, body.Username, u.ID, ResultSuccess, true)
	access, refresh, err := h.Issuer.IssuePair(u.ID)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "签发凭证失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"access": access, "refresh": refresh})
}

// rejectLocked 记锁定审计并按 Django 的 AccountLocked 返回 423
func (h *Handlers) rejectLocked(ctx context.Context, w http.ResponseWriter, r *http.Request, username string) {
	secs := LockRemaining(username)
	mins := 1
	if secs > 0 {
		mins = (secs + 59) / 60
	}
	RecordAttempt(ctx, h.Svc.DB, r, username, "", ResultLocked, false)
	httpx.Err(w, http.StatusLocked, "account_locked",
		fmt.Sprintf("登录失败次数过多，账号已锁定，请约 %d 分钟后重试。", mins))
}

// classifyFailure 区分「账号停用」与「凭据错误」，便于审计定位
func (h *Handlers) classifyFailure(ctx context.Context, username string) string {
	var active bool
	if err := h.Svc.DB.QueryRow(ctx,
		"SELECT is_active FROM accounts_user WHERE username=$1", username).Scan(&active); err == nil && !active {
		return ResultInactive
	}
	return ResultBadCredentials
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
