package auth

// 登录失败锁定 + 登录审计落库，对齐 apps/iam/login_guard.py。
//
// 策略：同一用户名在滑动窗口内连续失败达阈值即锁定一段时间，锁定期内即使凭据
// 正确也拒绝（423 + 剩余分钟）。每次尝试都写一条 iam_login_attempt 持久流水，
// 供 /auth/login-history 与 /org/login-audit 读取。
//
// 计数与锁标记 Django 走 cache（本地是进程内 LocMemCache），Go 侧同样用进程内
// 带 TTL 的 map —— 语义等价。多实例部署时需换成共享存储（Redis/PG），
// 见 PORTING.md 差异清单。

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// 登录审计的 result 取值（对齐 LoginAttempt.RESULT_*）
const (
	ResultSuccess        = "success"
	ResultBadCredentials = "bad_credentials"
	ResultInactive       = "inactive"
	ResultLocked         = "locked"
)

const (
	maxFailures     = 5
	lockoutDuration = 15 * time.Minute
	failureWindow   = 15 * time.Minute
)

type counter struct {
	n        int
	expireAt time.Time
}

var (
	guardMu  sync.Mutex
	failures = map[string]counter{}
	locks    = map[string]time.Time{} // username → 解锁时刻
)

func normUser(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// LockRemaining 返回剩余锁定秒数；未锁定返回 0
func LockRemaining(username string) int {
	guardMu.Lock()
	defer guardMu.Unlock()
	until, ok := locks[normUser(username)]
	if !ok || time.Now().After(until) {
		return 0
	}
	return int(time.Until(until).Seconds())
}

func IsLocked(username string) bool { return LockRemaining(username) > 0 }

// RegisterFailure 记一次失败；返回是否因此触发锁定
func RegisterFailure(username string) bool {
	u := normUser(username)
	if u == "" {
		return false
	}
	now := time.Now()
	guardMu.Lock()
	defer guardMu.Unlock()
	c := failures[u]
	if c.expireAt.Before(now) {
		c = counter{expireAt: now.Add(failureWindow)}
	}
	c.n++
	if c.n >= maxFailures {
		delete(failures, u)
		locks[u] = now.Add(lockoutDuration)
		return true
	}
	failures[u] = c
	return false
}

// ClearFailures 登录成功或管理员解锁时清零
func ClearFailures(username string) {
	u := normUser(username)
	guardMu.Lock()
	defer guardMu.Unlock()
	delete(failures, u)
	delete(locks, u)
}

// RecordAttempt 写一条登录审计流水；失败不阻断登录主流程（对齐 Django 的 try/except）
func RecordAttempt(ctx context.Context, db *pgxpool.Pool, r *http.Request, username, userID, result string, success bool) {
	id, _ := uuid.NewV7()
	var uid any
	if userID != "" {
		uid = userID
	}
	var ip any
	var ua string
	if r != nil {
		if v := httpx.ClientIP(r); v != "" {
			ip = v
		}
		ua = r.Header.Get("User-Agent")
		if len(ua) > 255 {
			ua = ua[:255]
		}
	}
	if len(username) > 150 {
		username = username[:150]
	}
	_, _ = db.Exec(ctx, `
		INSERT INTO iam_login_attempt (id, created_at, updated_at, username, success, result, ip, user_agent, user_id)
		VALUES ($1, now(), now(), $2, $3, $4, $5::inet, $6, $7::uuid)`,
		id.String(), username, success, result, ip, ua, uid)
}
