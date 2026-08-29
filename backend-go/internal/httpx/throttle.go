package httpx

// 通用滑动窗口限流，对齐 DRF 的 ScopedRateThrottle。
//
// Django 侧各 scope 的速率来自 REST_FRAMEWORK.DEFAULT_THROTTLE_RATES
// （ai=30/min、register=10/min、password_reset=8/min…），计数走 cache。
// 这些闸不是可选装饰：注册闸防批量刷号、找回密码闸防验证码爆破、
// AI 闸防 LLM token 成本 DoS —— 原生化时必须一并带过来。

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Throttle struct {
	scope  string // 库模式下的闸名（取自环境变量名，如 THROTTLE_REGISTER）
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

// NewThrottle 按 DRF 速率串（"<num>/<period>"，period ∈ s|min|hour|day）构造。
// envKey 非空时优先读同名环境变量，缺省回落 fallback。
func NewThrottle(envKey, fallback string) *Throttle {
	rate := fallback
	if envKey != "" {
		if v := os.Getenv(envKey); v != "" {
			rate = v
		}
	}
	limit, window := 60, time.Minute
	parts := strings.SplitN(rate, "/", 2)
	if n, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil && n > 0 {
		limit = n
	}
	if len(parts) == 2 {
		switch strings.TrimSpace(parts[1]) {
		case "s", "sec", "second":
			window = time.Second
		case "min", "minute":
			window = time.Minute
		case "hour", "hr":
			window = time.Hour
		case "day":
			window = 24 * time.Hour
		}
	}
	return &Throttle{scope: envKey, hits: map[string][]time.Time{}, limit: limit, window: window}
}

// sharedDB 由 UseSharedStore 在启动期注入。为空时限流退回进程内 map。
//
// 为什么要能共享：限流是**安全闸**（注册 10/min 防批量刷号、找回密码 8/min
// 防验证码爆破、AI 30/min 防 token 成本 DoS）。计数在进程内的话，
// 副本数一乘闸就形同虚设——两个副本等于把每个闸的额度直接翻倍。
// "要不要多副本"不该由这个实现细节决定。
var sharedDB *pgxpool.Pool

// UseSharedStore 让所有限流器改用库做计数（多副本部署必须调用）。
func UseSharedStore(db *pgxpool.Pool) { sharedDB = db }

// allowShared 用库做滑动窗口：先删过期命中，再数窗口内的行数，未超则插一行。
// 单条 SQL 里完成，避免"读-判-写"之间的竞态。
func (t *Throttle) allowShared(key string) (bool, int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	scope := t.scope
	if scope == "" {
		scope = "default"
	}
	var allowed bool
	var oldest *time.Time
	err := sharedDB.QueryRow(ctx, `
		WITH purged AS (
		  DELETE FROM iam_rate_hit
		   WHERE scope = $1 AND key = $2 AND hit_at < now() - $3::interval
		), cur AS (
		  SELECT count(*) AS n, min(hit_at) AS oldest
		    FROM iam_rate_hit WHERE scope = $1 AND key = $2 AND hit_at >= now() - $3::interval
		), ins AS (
		  INSERT INTO iam_rate_hit (scope, key)
		  SELECT $1, $2 FROM cur WHERE cur.n < $4
		  RETURNING 1
		)
		SELECT (SELECT count(*) FROM ins) > 0, (SELECT oldest FROM cur)`,
		scope, key, t.window.String(), t.limit).Scan(&allowed, &oldest)
	if err != nil {
		// 库出问题时**放行**而不是拒绝。限流不是鉴权：把所有人挡在门外
		// 换来的不是安全，是自己制造的全站不可用。降级的方向要选对。
		slog.Warn("限流查询失败，本次放行", "scope", scope, "err", err)
		return true, 0, true
	}
	if allowed {
		return true, 0, true
	}
	wait := 1
	if oldest != nil {
		if w := int(time.Until(oldest.Add(t.window)).Seconds()) + 1; w > wait {
			wait = w
		}
	}
	return false, wait, true
}

// PurgeExpiredRateHits 清理过期命中行。allowShared 里那条 DELETE 只清
// 「本 key 本 scope」的，从没被访问过的旧 key 需要这个兜底。
func PurgeExpiredRateHits(ctx context.Context, db *pgxpool.Pool, keep time.Duration) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM iam_rate_hit WHERE hit_at < now() - $1::interval`, keep.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Allow 返回是否放行；被限时给出建议重试秒数（向上取整，对齐 DRF）
func (t *Throttle) Allow(key string) (bool, int) {
	if sharedDB != nil {
		if ok, wait, handled := t.allowShared(key); handled {
			return ok, wait
		}
	}
	now := time.Now()
	cutoff := now.Add(-t.window)

	t.mu.Lock()
	defer t.mu.Unlock()
	kept := t.hits[key][:0]
	for _, at := range t.hits[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	t.hits[key] = kept
	if len(kept) >= t.limit {
		wait := int(kept[0].Add(t.window).Sub(now).Seconds())
		if wait < 1 {
			wait = 1
		}
		return false, wait
	}
	t.hits[key] = append(t.hits[key], now)
	return true, 0
}

// Guard 匿名端点限流入口：超限时写 DRF 同款 429 并返回 false
func (t *Throttle) Guard(w http.ResponseWriter, r *http.Request) bool {
	ok, wait := t.Allow(ClientIP(r))
	if !ok {
		// 文案逐字对齐 DRF zh-hans 翻译（两句之间有一个空格，别手滑省掉）
		Err(w, http.StatusTooManyRequests, "throttled",
			"请求已被限流。 预计 "+strconv.Itoa(wait)+" 秒后可用。")
		return false
	}
	return true
}

// GuardKey 按调用方给的键限流，而不是按来源 IP。
//
// 用在「被猜的那个东西」上，而不是「猜的人」上。按 IP 限流挡不住换 IP，
// 而暴破一个具体资源时，被反复试的**是那个资源**——把闸挂在它身上，
// 攻击者换多少 IP 都绕不开。登录锁定按用户名而不是按 IP，是同一个道理。
//
// 两道闸通常要一起用：按键的挡定向暴破，按 IP 的挡广撒网式扫描。
func (t *Throttle) GuardKey(w http.ResponseWriter, key string) bool {
	ok, wait := t.Allow(key)
	if !ok {
		Err(w, http.StatusTooManyRequests, "throttled",
			"请求已被限流。 预计 "+strconv.Itoa(wait)+" 秒后可用。")
		return false
	}
	return true
}

// ClientIP 对齐 Django 的 X-Forwarded-For 取首段、否则 REMOTE_ADDR
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
