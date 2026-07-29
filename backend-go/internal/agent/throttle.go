package agent

// AI 端点限流（对齐 Django 的 ScopedRateThrottle scope="ai"，默认 30/min）。
// 目的与 Django 一致：防 LLM token 成本 DoS —— 缺了这道闸，一次脚本循环就能
// 把真金白银的模型调用打爆，因此原生化时必须一并带过来。
//
// 实现：按用户维度的滑动窗口计数（Django 侧走 cache，本地是 LocMemCache，
// 语义等价）。窗口内超限返回 429 + 与 DRF 同款文案「请求已被限流。预计 N 秒后可用。」

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type throttler struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

var aiThrottle = newThrottle(os.Getenv("THROTTLE_AI"))

func newThrottle(rate string) *throttler {
	limit, window := 30, time.Minute
	if rate != "" {
		// DRF 速率格式 "<num>/<period>"，period ∈ {s,sec,min,hour,day}
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
	}
	return &throttler{hits: map[string][]time.Time{}, limit: limit, window: window}
}

// allow 返回是否放行；被限时同时给出建议重试秒数（向上取整，对齐 DRF）
func (t *throttler) allow(key string) (bool, int) {
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
