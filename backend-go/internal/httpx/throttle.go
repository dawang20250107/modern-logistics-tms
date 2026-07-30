package httpx

// 通用滑动窗口限流，对齐 DRF 的 ScopedRateThrottle。
//
// Django 侧各 scope 的速率来自 REST_FRAMEWORK.DEFAULT_THROTTLE_RATES
// （ai=30/min、register=10/min、password_reset=8/min…），计数走 cache。
// 这些闸不是可选装饰：注册闸防批量刷号、找回密码闸防验证码爆破、
// AI 闸防 LLM token 成本 DoS —— 原生化时必须一并带过来。

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Throttle struct {
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
	return &Throttle{hits: map[string][]time.Time{}, limit: limit, window: window}
}

// Allow 返回是否放行；被限时给出建议重试秒数（向上取整，对齐 DRF）
func (t *Throttle) Allow(key string) (bool, int) {
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
