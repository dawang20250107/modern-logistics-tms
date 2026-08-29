package auth

// 自助账户能力（个人中心），对齐 apps/iam/account_views.py + password_reset.py：
// 注册 / 改密 / 我的登录记录 / 登录方式探测 / 找回密码（请求+确认）/ 头像上删 /
// 资料自助维护 / token 校验。
//
// 设计取向原样保留：注册只创建基础账号，**不**自带组织与角色 —— 组织与角色一律
// 由管理员在组织中台分配，杜绝"自助注册即提权"。

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/pbkdf2"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

var (
	registerThrottle = httpx.NewThrottle("THROTTLE_REGISTER", "10/min")
	pwResetThrottle  = httpx.NewThrottle("THROTTLE_PASSWORD_RESET", "8/min")
)

// MakeDjangoPassword 生成 django.contrib.auth.hashers 兼容的 pbkdf2_sha256 哈希
func MakeDjangoPassword(password string) string {
	salt := randomString(22)
	const iterations = 870000
	dk := pbkdf2Key([]byte(password), []byte(salt), iterations, sha256.Size)
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", iterations, salt, base64.StdEncoding.EncodeToString(dk))
}

func pbkdf2Key(password, salt []byte, iterations, keyLen int) []byte {
	return pbkdf2.Key(password, salt, iterations, keyLen, sha256.New)
}

const saltAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		v, _ := rand.Int(rand.Reader, big.NewInt(int64(len(saltAlphabet))))
		b[i] = saltAlphabet[v.Int64()]
	}
	return string(b)
}

// ── 注册 ──

// Register POST /auth/register —— 创建基础账号并直接签发 JWT（自动登录）
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	// 默认关闭。理由见 config.Config.AllowSelfRegistration：
	// 自助注册出来的账号业务上什么也干不了（无组织、无角色、数据范围为空），
	// 但它是一个任何人都能自助拿到的**已认证身份**——所有只判「登录了没有」
	// 的端点对它敞开。10/min 的限流挡的是批量刷号，不是"该不该给"这件事。
	if !h.AllowSelfRegistration {
		httpx.Err(w, http.StatusForbidden, "REGISTRATION_CLOSED",
			"本系统不开放自助注册，请联系管理员开通账号。")
		return
	}
	if !registerThrottle.Guard(w, r) {
		return
	}
	ctx := r.Context()
	var body struct {
		Username string `json:"username"`
		Nickname string `json:"nickname"`
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{"请求体不是合法 JSON。"}})
		return
	}
	details := map[string]any{}
	username := strings.TrimSpace(body.Username)
	switch {
	case body.Username == "":
		details["username"] = []string{"该字段是必填项。"}
	case username == "":
		details["username"] = []string{"用户名不能为空"}
	default:
		var taken bool
		_ = h.Svc.DB.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM accounts_user WHERE lower(username)=lower($1))", username).Scan(&taken)
		if taken {
			details["username"] = []string{"该用户名已被占用"}
		}
	}
	if body.Password == "" {
		details["password"] = []string{"该字段是必填项。"}
	} else if msgs := ValidatePassword(body.Password, nil); len(msgs) > 0 {
		details["password"] = msgs
	}
	if len(details) > 0 {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
		return
	}

	id, _ := uuid.NewV7()
	if _, err := h.Svc.DB.Exec(ctx, `
		INSERT INTO accounts_user (id, password, last_login, is_superuser, username, first_name, last_name,
		  email, is_staff, is_active, date_joined, phone, nickname, organization_id, avatar, preferences)
		VALUES ($1::uuid, $2, NULL, false, $3, '', '', '', false, true, now(), $4, $5, NULL, NULL, '{}'::jsonb)`,
		id.String(), MakeDjangoPassword(body.Password), username,
		strings.TrimSpace(body.Phone), strings.TrimSpace(body.Nickname)); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "注册失败："+err.Error())
		return
	}
	RecordAttempt(ctx, h.Svc.DB, r, username, id.String(), ResultSuccess, true)
	access, refresh, err := h.Issuer.IssuePair(id.String())
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "签发凭证失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"access": access, "refresh": refresh})
}

// ── 改密 ──

// ChangePassword POST /auth/change-password
func (h *Handlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := h.Svc.UserByID(ctx, UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{"请求体不是合法 JSON。"}})
		return
	}
	details := map[string]any{}
	if body.OldPassword == "" {
		details["old_password"] = []string{"该字段是必填项。"}
	} else if ok, _ := VerifyDjangoPassword(body.OldPassword, u.Password); !ok {
		details["old_password"] = []string{"当前密码不正确"}
	}
	if body.NewPassword == "" {
		details["new_password"] = []string{"该字段是必填项。"}
	} else if msgs := ValidatePassword(body.NewPassword, pwUser(u)); len(msgs) > 0 {
		details["new_password"] = msgs
	}
	// 字段级校验都过了才跑对象级校验（对齐 Serializer.validate 的时机）
	if len(details) == 0 && body.OldPassword == body.NewPassword {
		details["new_password"] = []string{"新密码不能与当前密码相同"}
	}
	if len(details) > 0 {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
		return
	}
	if _, err := h.Svc.DB.Exec(ctx, "UPDATE accounts_user SET password=$2 WHERE id=$1::uuid",
		u.ID, MakeDjangoPassword(body.NewPassword)); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	// 改密码必须踢掉所有已存在的会话，否则"我怀疑密码泄漏了所以改一下"这个动作
	// 起不到任何作用——攻击者手里的 access/refresh 照样能用到自然过期。
	if err := RevokeAllForUser(ctx, h.Svc.DB, u.ID); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "作废旧会话失败")
		return
	}
	// 顺手把新券发回去：不然用户刚改完密码就被自己的水位线踢下线，
	// 体验上像是"改密码 = 被登出"，而这一步本来可以无缝。
	access, refresh, err := h.Issuer.IssuePair(u.ID)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]string{"detail": "密码已更新，请重新登录"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{
		"detail": "密码已更新", "access": access, "refresh": refresh,
	})
}

func pwUser(u *UserRow) *PasswordUser {
	return &PasswordUser{Username: u.Username, Email: u.Email}
}

// ── 我的登录记录 ──

// LoginHistory GET /auth/login-history —— 本人最近 20 条（复用登录审计表）
func (h *Handlers) LoginHistory(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Svc.DB.Query(r.Context(), `
		SELECT l.id::text, l.username, l.user_id::text, l.success, l.result,
		       host(l.ip), l.user_agent, l.created_at
		FROM iam_login_attempt l WHERE l.user_id = $1::uuid
		ORDER BY l.created_at DESC, l.id LIMIT 20`, UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, username, result, ua string
		var userID, ip *string
		var success bool
		var createdAt time.Time
		if err := rows.Scan(&id, &username, &userID, &success, &result, &ip, &ua, &createdAt); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取失败")
			return
		}
		items = append(items, map[string]any{
			"id": id, "username": username, "username_display": username, "user": userID,
			"success": success, "result": result, "result_label": loginResultLabel(result),
			"ip": ip, "user_agent": ua, "created_at": pyISO(createdAt),
		})
	}
	httpx.JSON(w, http.StatusOK, items)
}

func loginResultLabel(result string) string {
	switch result {
	case ResultSuccess:
		return "成功"
	case ResultBadCredentials:
		return "凭据错误"
	case ResultInactive:
		return "账号停用"
	case ResultLocked:
		return "已锁定"
	}
	return result
}

func pyISO(t time.Time) string {
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05Z07:00")
	}
	return t.Format("2006-01-02T15:04:05.000000Z07:00")
}

// ── 登录方式探测 ──

// AuthMethods GET /auth/methods —— 账号密码恒开；微信扫码为预留能力
func (h *Handlers) AuthMethods(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"password": true,
		// 自助注册默认关闭。告诉前端，是为了让登录页直接不显示「注册新账号」——
		// 留一个点进去必然 403 的入口，比没有入口更糟。
		"registration": map[string]any{"enabled": h.AllowSelfRegistration},
		"wechat": map[string]any{
			"enabled": strings.EqualFold(os.Getenv("WECHAT_LOGIN_ENABLED"), "true"),
			"note":    "微信扫码登录为预留能力，配置微信开放平台/企业微信后启用。",
		},
	})
}

// ── 找回密码 ──

// 验证码存进程内（Django 走 cache，本地同为进程内 LocMem），10 分钟一次性。
type resetCode struct {
	code     string
	expireAt time.Time
}

var (
	resetMu    sync.Mutex
	resetCodes = map[string]resetCode{}
)

const resetCodeTTL = 10 * time.Minute

func issueResetCode(identifier string) string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	code := fmt.Sprintf("%06d", n.Int64())
	key := strings.ToLower(strings.TrimSpace(identifier))
	// 验证码放进程内 map，在多副本下是**功能断裂**而不只是弱化：
	// A 副本发的码，请求落到 B 副本时查无此码，用户永远重置不了密码。
	// 落库之后哪个副本接手都认。
	if sharedDB != nil {
		ctx, cancel := guardCtx()
		defer cancel()
		if _, err := sharedDB.Exec(ctx, `
			INSERT INTO iam_reset_code (identifier, code, expires_at)
			VALUES ($1, $2, now() + $3::interval)
			ON CONFLICT (identifier) DO UPDATE SET
			  code = EXCLUDED.code, expires_at = EXCLUDED.expires_at, created_at = now()`,
			key, code, resetCodeTTL.String()); err != nil {
			slog.Error("验证码写库失败", "err", err)
		}
		return code
	}
	resetMu.Lock()
	resetCodes[key] = resetCode{code, time.Now().Add(resetCodeTTL)}
	resetMu.Unlock()
	return code
}

// verifyResetCode 一次性校验（命中即作废），比较用恒定时间避免计时侧信道
func verifyResetCode(identifier, code string) bool {
	key := strings.ToLower(strings.TrimSpace(identifier))
	if sharedDB != nil {
		ctx, cancel := guardCtx()
		defer cancel()
		// DELETE ... RETURNING：取出即作废，一条语句里完成，
		// 两个并发请求不可能都拿到同一个码。
		var stored string
		err := sharedDB.QueryRow(ctx, `
			DELETE FROM iam_reset_code
			 WHERE identifier = $1 AND expires_at > now()
			RETURNING code`, key).Scan(&stored)
		if err != nil {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(stored), []byte(code)) == 1
	}
	resetMu.Lock()
	defer resetMu.Unlock()
	rc, ok := resetCodes[key]
	if !ok || time.Now().After(rc.expireAt) {
		delete(resetCodes, key)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(rc.code), []byte(code)) != 1 {
		return false
	}
	delete(resetCodes, key)
	return true
}

// findUserByIdentifier 邮箱 / 手机号 / 用户名任一定位账号（顺序对齐 find_user）
func (h *Handlers) findUserByIdentifier(r *http.Request, ident string) *UserRow {
	ident = strings.TrimSpace(ident)
	if ident == "" {
		return nil
	}
	for _, where := range []string{"lower(u.email) = lower($1)", "u.phone = $1", "lower(u.username) = lower($1)"} {
		if u, err := h.Svc.userBy(r.Context(), where, ident); err == nil && u != nil {
			return u
		}
	}
	return nil
}

// maskTarget 返回（掩码后的发送目标, 渠道）：优先邮箱，其次手机号
func maskTarget(u *UserRow) (string, string) {
	if u.Email != "" {
		name, dom, _ := strings.Cut(u.Email, "@")
		if len(name) > 2 {
			name = name[:2]
		}
		return name + "***@" + dom, "email"
	}
	if len([]rune(u.Phone)) >= 7 {
		p := []rune(u.Phone)
		return string(p[:3]) + "****" + string(p[len(p)-4:]), "phone"
	}
	return "", ""
}

// fullTarget 返回未掩码的下发目标（优先邮箱，其次手机号）。
// 与 maskTarget 的优先级必须一致，否则响应里说"发到邮箱"而实际发了短信。
func fullTarget(u *UserRow) string {
	if strings.Contains(u.Email, "@") {
		return u.Email
	}
	if len([]rune(u.Phone)) >= 7 {
		return u.Phone
	}
	return ""
}

// PasswordResetRequest POST /auth/password-reset/request
//
// 不泄露账号是否存在：无论是否命中都返回 sent=true —— 这是防账号枚举的关键，
// 命中与否响应体的差异仅限掩码提示，且掩码本身不足以还原目标。
func (h *Handlers) PasswordResetRequest(w http.ResponseWriter, r *http.Request) {
	if !pwResetThrottle.Guard(w, r) {
		return
	}
	var body struct {
		Identifier string `json:"identifier"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ident := strings.TrimSpace(body.Identifier)
	if ident == "" {
		// Django 这里返回的是普通 Response 而非抛异常：信封仍是 success:true，
		// 状态码却是 400 —— 原样复刻，避免前端按 error 分支解析时取不到 detail。
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"detail": "请输入邮箱或手机号"})
		return
	}
	// 没有配下发通道时，直说没开通，不要假装发出去了。
	// 原先这里无论如何都回 sent=true，而验证码只写进了 stderr——
	// 用户永远收不到，日志却人人可读（见 notify.go）。
	if h.ResetSender == nil {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"sent": false, "target": nil, "channel": nil,
			"detail": "本系统未开通自助找回密码，请联系管理员重置。",
		})
		return
	}

	payload := map[string]any{"sent": true}
	if u := h.findUserByIdentifier(r, ident); u != nil {
		target, channel := maskTarget(u)
		full := fullTarget(u)
		if full == "" {
			// 账号没留邮箱/手机号：同样不能泄露"这个账号存在但没联系方式"，
			// 响应保持与命中不到时一致
			httpx.JSON(w, http.StatusOK, payload)
			return
		}
		code := issueResetCode(ident)
		if err := h.ResetSender.Send(r.Context(), full, code); err != nil {
			// 失败只记通道与用户名，**绝不记验证码，也不记完整目标**
			slog.Error("验证码下发失败", "user", u.Username, "channel", h.ResetSender.Channel(), "err", err)
			httpx.Err(w, http.StatusBadGateway, "SEND_FAILED", "验证码发送失败，请稍后重试或联系管理员。")
			return
		}
		payload["target"] = nilIfEmpty(target)
		payload["channel"] = nilIfEmpty(channel)
		// dev_code 只在 DEBUG 下回给前端，方便本地联调；生产 DEBUG 必须是 false
		//（config.Preflight 不检查这一条，因为 DEBUG=true 时它整个不跑——
		// 所以"生产别开 DEBUG"这条在部署文档里是硬要求）
		if h.Debug {
			payload["dev_code"] = code
		}
	}
	httpx.JSON(w, http.StatusOK, payload)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// PasswordResetConfirm POST /auth/password-reset/confirm
func (h *Handlers) PasswordResetConfirm(w http.ResponseWriter, r *http.Request) {
	if !pwResetThrottle.Guard(w, r) {
		return
	}
	var body struct {
		Identifier  string `json:"identifier"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{"请求体不是合法 JSON。"}})
		return
	}
	details := map[string]any{}
	if strings.TrimSpace(body.Identifier) == "" {
		details["identifier"] = []string{"该字段是必填项。"}
	}
	switch n := len([]rune(body.Code)); {
	case body.Code == "":
		details["code"] = []string{"该字段是必填项。"}
	case n > 6:
		details["code"] = []string{"请确保这个字段不能超过 6 个字符。"}
	case n < 6:
		details["code"] = []string{"请确保这个字段至少包含 6 个字符。"}
	}
	if body.NewPassword == "" {
		details["new_password"] = []string{"该字段是必填项。"}
	} else if msgs := ValidatePassword(body.NewPassword, nil); len(msgs) > 0 {
		details["new_password"] = msgs
	}
	if len(details) > 0 {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
		return
	}
	u := h.findUserByIdentifier(r, body.Identifier)
	if u == nil || !verifyResetCode(body.Identifier, body.Code) {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"detail": "验证码无效或已过期"})
		return
	}
	if _, err := h.Svc.DB.Exec(r.Context(), "UPDATE accounts_user SET password=$2 WHERE id=$1::uuid",
		u.ID, MakeDjangoPassword(body.NewPassword)); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	// 找回密码是"我进不去了"或"我怀疑被盗了"，正是最需要踢掉既有会话的场景
	if err := RevokeAllForUser(r.Context(), h.Svc.DB, u.ID); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "作废旧会话失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"detail": "密码已重置，请用新密码登录"})
}

// ── 头像 ──

const avatarMaxBytes = 2 * 1024 * 1024

var avatarTypes = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif",
}

// Avatar POST/DELETE /auth/me/avatar —— 本人头像上传 / 移除
func (h *Handlers) Avatar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := UserID(r)
	if r.Method == http.MethodDelete {
		var cur string
		_ = h.Svc.DB.QueryRow(ctx, "SELECT COALESCE(avatar,'') FROM accounts_user WHERE id=$1::uuid", uid).Scan(&cur)
		if cur != "" {
			_ = os.Remove(filepath.Join(h.MediaRoot, filepath.FromSlash(cur)))
			_, _ = h.Svc.DB.Exec(ctx, "UPDATE accounts_user SET avatar=NULL WHERE id=$1::uuid", uid)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := r.ParseMultipartForm(avatarMaxBytes + 1<<20); err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"detail": "未收到文件"})
		return
	}
	file, hdr, err := r.FormFile("avatar")
	if err != nil {
		file, hdr, err = r.FormFile("file")
	}
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"detail": "未收到文件"})
		return
	}
	defer file.Close()
	if hdr.Size > avatarMaxBytes {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"detail": "图片过大，请控制在 2MB 内"})
		return
	}
	ext, ok := avatarTypes[hdr.Header.Get("Content-Type")]
	if !ok {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"detail": "仅支持 JPG / PNG / WEBP / GIF"})
		return
	}
	// 落盘路径对齐 Django 的 upload_to（avatars/<uuid><ext>），使 /media/ 直出一致
	rel := "avatars/" + uuid.NewString() + ext
	abs := filepath.Join(h.MediaRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "存储目录不可写")
		return
	}
	dst, err := os.Create(abs)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
		return
	}
	written, cerr := io.Copy(dst, io.LimitReader(file, avatarMaxBytes+1))
	_ = dst.Close()
	if cerr != nil || written > avatarMaxBytes {
		_ = os.Remove(abs)
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"detail": "图片过大，请控制在 2MB 内"})
		return
	}
	if _, err := h.Svc.DB.Exec(ctx, "UPDATE accounts_user SET avatar=$2 WHERE id=$1::uuid", uid, rel); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"avatar_url": h.MediaBase + "/media/" + rel})
}

// ── 资料自助维护 ──

// 偏好白名单：仅这些键可自助维护（对齐 PREFERENCE_KEYS）
var preferenceKeys = map[string]bool{
	"default_route": true, "table_density": true, "page_size": true,
	"notify_desktop": true, "notify_email": true,
}

// MePatch PATCH /auth/me —— 昵称/手机号/邮箱 + 个人偏好（白名单键），不含组织与角色
func (h *Handlers) MePatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := h.Svc.UserByID(ctx, UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{"请求体不是合法 JSON。"}})
		return
	}
	sets, args := []string{}, []any{u.ID}
	details := map[string]any{}
	for _, f := range []string{"nickname", "phone", "email"} {
		raw, has := body[f]
		if !has {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			details[f] = []string{"该字段必须是字符串。"}
			continue
		}
		if f == "email" && s != "" && !strings.Contains(s, "@") {
			details[f] = []string{"请输入一个有效的电子邮件地址。"}
			continue
		}
		args = append(args, s)
		sets = append(sets, fmt.Sprintf("%s=$%d", f, len(args)))
	}
	if len(details) > 0 {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
		return
	}
	if prefs, ok := body["preferences"].(map[string]any); ok {
		merged := map[string]any{}
		for k, v := range u.Preferences {
			merged[k] = v
		}
		for k, v := range prefs {
			if preferenceKeys[k] {
				merged[k] = v
			}
		}
		pj, _ := json.Marshal(merged)
		args = append(args, string(pj))
		sets = append(sets, fmt.Sprintf("preferences=$%d::jsonb", len(args)))
	}
	if len(sets) > 0 {
		if _, err := h.Svc.DB.Exec(ctx,
			"UPDATE accounts_user SET "+strings.Join(sets, ", ")+" WHERE id=$1::uuid", args...); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
			return
		}
	}
	h.Me(w, r) // 对齐 Django：改完直接回 GET /me 的全量视图
}

// ── token 校验 ──

// TokenVerify POST /auth/token/verify —— 对齐 simplejwt TokenVerifyView（有效返回 200 空对象）
func (h *Handlers) TokenVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"token": []string{"该字段是必填项。"}})
		return
	}
	if _, err := h.Issuer.ParseAny(body.Token); err != nil {
		httpx.ErrDetails(w, http.StatusUnauthorized, "token_not_valid", "请求参数校验失败",
			map[string]any{"detail": err.Error(), "code": "token_not_valid"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{})
}
