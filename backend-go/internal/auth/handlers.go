package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/blob"
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
	// MediaRoot 媒体文件落盘根目录（对齐 Django 的 MEDIA_ROOT）。
	// Blob 非空时以 Blob 为准，这里只留给尚未迁移的调用方。
	MediaRoot string
	// Blob 媒体存放。为 nil 时退回 MediaRoot 直接落盘（仅测试构造会出现）。
	Blob blob.Store
	// Debug 对齐 settings.DEBUG：仅调试期在找回密码响应里附 dev_code
	Debug bool
	// AllowSelfRegistration 见 config.Config 同名字段：默认关闭
	AllowSelfRegistration bool
	// ResetSender 密码找回验证码的下发通道。nil = 未开通自助找回。
	// 刻意不给默认实现：原先"默认写日志"等于验证码人人可读（见 notify.go）。
	ResetSender Sender
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
		// 账号级水位线：改密 / 停用 / 管理员重置之后，签发早于水位线的 access
		// 必须立刻失效。只作废 refresh 是不够的——access 还能再活满一个 TTL。
		if claims.IssuedAt != nil &&
			IssuedBeforeCutoff(r.Context(), h.Svc.DB, claims.UserID, claims.IssuedAt.Time) {
			httpx.Err(w, http.StatusUnauthorized, "TOKEN_REVOKED", "凭证已失效，请重新登录")
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
	ctx := r.Context()
	// 校验必须在签发之前：先签发再检查旧券，等于拿一张已作废的券照样能换新券，
	// 黑名单就只是记了个账。
	if IsRevoked(ctx, h.Svc.DB, claims.JTI) {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_REVOKED", "该凭证已失效，请重新登录")
		return
	}
	if claims.IssuedAt != nil &&
		IssuedBeforeCutoff(ctx, h.Svc.DB, claims.UserID, claims.IssuedAt.Time) {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_REVOKED", "凭证已失效，请重新登录")
		return
	}
	access, refresh, err := h.Issuer.IssuePair(claims.UserID)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "签发凭证失败")
		return
	}
	// 旧券入列：轮换的意义就在这一步。少了它，一张泄漏的 refresh 在自然过期前
	// 可以无限次换出新的 access + refresh，等于永久有效。
	exp := time.Now().Add(24 * time.Hour)
	if claims.ExpiresAt != nil {
		exp = claims.ExpiresAt.Time
	}
	if err := Revoke(ctx, h.Svc.DB, claims.JTI, claims.UserID, "refresh", ReasonRotated, exp); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "作废旧凭证失败")
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

// Logout POST /api/v1/auth/logout —— 把提交的 refresh 券作废。
//
// 原先服务端没有这个动作，"退出登录"只是前端把本地存的 token 删掉；
// 券本身照样有效到自然过期，在共用电脑或凭证已泄漏的场景下等于没退出。
//
// 幂等：券已失效 / 解析不出来都回 200 —— 退出登录不该因为"你本来就没登录"而报错。
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Refresh string `json:"refresh"`
		All     bool   `json:"all"` // true = 踢掉该账号全部会话
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ctx := r.Context()
	if body.All {
		if uid := UserID(r); uid != "" {
			_ = RevokeAllForUser(ctx, h.Svc.DB, uid)
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"detail": "已退出全部会话"})
		return
	}
	if body.Refresh != "" {
		if claims, err := h.Issuer.Parse(body.Refresh, "refresh"); err == nil {
			exp := time.Now().Add(24 * time.Hour)
			if claims.ExpiresAt != nil {
				exp = claims.ExpiresAt.Time
			}
			_ = Revoke(ctx, h.Svc.DB, claims.JTI, claims.UserID, "refresh", ReasonLogout, exp)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"detail": "已退出登录"})
}
