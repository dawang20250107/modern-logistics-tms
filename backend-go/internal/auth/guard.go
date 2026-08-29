package auth

// 登录失败锁定 + 登录审计落库，对齐 apps/iam/login_guard.py。
//
// 策略：同一用户名在滑动窗口内连续失败达阈值即锁定一段时间，锁定期内即使凭据
// 正确也拒绝（423 + 剩余分钟）。每次尝试都写一条 iam_login_attempt 持久流水，
// 供 /auth/login-history 与 /org/login-audit 读取。
//
// 计数与锁标记原本是进程内带 TTL 的 map（对齐 Django 的 LocMemCache）。
// 那在单副本下语义等价，多副本下就不是了：5 次失败锁定，两个副本等于 10 次，
// 暴力破解的成本随副本数线性下降——而这是一道安全闸，不该由部署形态决定强度。
// 现在计数落库（iam_login_throttle），UseSharedStore 注入连接池后自动生效；
// 未注入时仍走进程内 map（单元测试与不带库的场景）。

import (
	"context"
	"log/slog"
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

	// sharedDB 由 UseSharedStore 注入；为空时退回进程内 map
	sharedDB *pgxpool.Pool
)

// UseSharedStore 让登录锁定改用库计数（多副本部署必须调用）。
func UseSharedStore(db *pgxpool.Pool) { sharedDB = db }

func guardCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Second)
}

func normUser(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// LockRemaining 返回剩余锁定秒数；未锁定返回 0
func LockRemaining(username string) int {
	u := normUser(username)
	if sharedDB != nil {
		ctx, cancel := guardCtx()
		defer cancel()
		var secs *float64
		err := sharedDB.QueryRow(ctx, `
			SELECT EXTRACT(EPOCH FROM (locked_until - now()))
			  FROM iam_login_throttle
			 WHERE username = $1 AND locked_until IS NOT NULL AND locked_until > now()`, u).Scan(&secs)
		if err != nil || secs == nil {
			// 查不到或出错都按"没锁"处理：库抖动时把所有人挡在登录页外面，
			// 换来的不是安全，是自己制造的全站不可用。
			return 0
		}
		return int(*secs)
	}
	guardMu.Lock()
	defer guardMu.Unlock()
	until, ok := locks[u]
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
	if sharedDB != nil {
		ctx, cancel := guardCtx()
		defer cancel()
		// 一条 upsert 完成「窗口过期则重新计数、否则累加；达阈值即上锁」。
		// 放在一条语句里是为了避免"读-判-写"之间被另一个副本插进来。
		var locked bool
		err := sharedDB.QueryRow(ctx, `
			INSERT INTO iam_login_throttle (username, failures, window_end, updated_at)
			VALUES ($1, 1, now() + $2::interval, now())
			ON CONFLICT (username) DO UPDATE SET
			  failures = CASE WHEN iam_login_throttle.window_end < now() THEN 1
			                  ELSE iam_login_throttle.failures + 1 END,
			  window_end = CASE WHEN iam_login_throttle.window_end < now() THEN now() + $2::interval
			                    ELSE iam_login_throttle.window_end END,
			  locked_until = CASE WHEN (CASE WHEN iam_login_throttle.window_end < now() THEN 1
			                                 ELSE iam_login_throttle.failures + 1 END) >= $3
			                      THEN now() + $4::interval ELSE iam_login_throttle.locked_until END,
			  updated_at = now()
			RETURNING (locked_until IS NOT NULL AND locked_until > now())`,
			u, failureWindow.String(), maxFailures, lockoutDuration.String()).Scan(&locked)
		if err != nil {
			slog.Warn("登录失败计数写库失败", "err", err)
			return false
		}
		return locked
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
	if sharedDB != nil {
		ctx, cancel := guardCtx()
		defer cancel()
		if _, err := sharedDB.Exec(ctx, `DELETE FROM iam_login_throttle WHERE username = $1`, u); err != nil {
			slog.Warn("清理登录失败计数失败", "err", err)
		}
		return
	}
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
