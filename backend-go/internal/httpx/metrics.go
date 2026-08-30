package httpx

// Prometheus 文本格式的 /metrics。
//
// 为什么手写而不是引 client_golang：这套系统上线后需要回答的问题就三类——
// 哪个接口在报错、哪个接口慢、连接池够不够。这三类各自是一个计数器/直方图，
// 手写的曝光格式约百来行，比多一个依赖树划算；也和这个仓库其它地方
// （自研迁移器、自研限流、自研表格）的取舍一致。真要接 OTel 时再换不迟。
//
// **路径基数是这里最容易踩的坑**：直接用 r.URL.Path 做标签，
// /api/v1/orders/<uuid> 每个订单就是一条新时间序列，几万单之后
// Prometheus 会被打爆。所以标签取的是 chi 的**路由模式**
// （/api/v1/orders/{id}），它的取值集合是有限的、等于路由表大小。

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 直方图分桶（秒）。覆盖 5ms 到 10s：低端要能分辨列表查询的常态，
// 高端要能看见"卡住了"。桶边界一旦上线就不该随便改——改了历史曲线不可比。
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type routeKey struct {
	method, pattern string
	status          int
}

type histogram struct {
	counts []uint64 // 各桶累计（le 语义在导出时再累加）
	sum    float64
	n      uint64
}

var (
	metricsMu   sync.Mutex
	reqTotal    = map[routeKey]uint64{}
	reqLatency  = map[routeKey]*histogram{}
	metricsPool *pgxpool.Pool
	startedAt   = time.Now()
)

// BindMetricsPool 让 /metrics 能导出连接池水位（可为 nil）。
func BindMetricsPool(p *pgxpool.Pool) { metricsPool = p }

func observe(method, pattern string, status int, sec float64) {
	if pattern == "" {
		// 没匹配到路由（404）的请求全部归到一个桶里，避免被扫描器刷爆基数
		pattern = "__unmatched__"
	}
	k := routeKey{method: method, pattern: pattern, status: status}
	metricsMu.Lock()
	defer metricsMu.Unlock()
	reqTotal[k]++
	h := reqLatency[k]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(latencyBuckets))}
		reqLatency[k] = h
	}
	h.sum += sec
	h.n++
	for i, b := range latencyBuckets {
		if sec <= b {
			h.counts[i]++
			break // 导出时再做累加，存的是"落在这个桶"的次数
		}
	}
}

// MetricsHandler 曝光端点。
//
// **默认关闭**：METRICS_TOKEN 未设置时返回 404。
// /metrics 会泄露路由表、流量规模与错误率——这些对攻击者是有用的侦察信息，
// 不该像 /healthz 那样人人可读。设了 token 就要求 Bearer 匹配。
func MetricsHandler(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			Err(w, http.StatusNotFound, "not_found", "未找到。")
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != token {
			Err(w, http.StatusUnauthorized, "NOT_AUTHENTICATED", "未提供有效凭证")
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var b strings.Builder

		b.WriteString("# HELP tms_build_info 构建与运行信息（值恒为 1，信息在标签里）\n")
		b.WriteString("# TYPE tms_build_info gauge\n")
		fmt.Fprintf(&b, "tms_build_info{engine=\"go\"} 1\n")

		b.WriteString("# HELP tms_process_uptime_seconds 进程已运行秒数\n")
		b.WriteString("# TYPE tms_process_uptime_seconds gauge\n")
		fmt.Fprintf(&b, "tms_process_uptime_seconds %.0f\n", time.Since(startedAt).Seconds())

		metricsMu.Lock()
		keys := make([]routeKey, 0, len(reqTotal))
		for k := range reqTotal {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].pattern != keys[j].pattern {
				return keys[i].pattern < keys[j].pattern
			}
			if keys[i].method != keys[j].method {
				return keys[i].method < keys[j].method
			}
			return keys[i].status < keys[j].status
		})

		b.WriteString("# HELP tms_http_requests_total HTTP 请求数（按路由模式，不是原始路径——见文件头注释）\n")
		b.WriteString("# TYPE tms_http_requests_total counter\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "tms_http_requests_total{method=%q,route=%q,status=\"%d\"} %d\n",
				k.method, k.pattern, k.status, reqTotal[k])
		}

		b.WriteString("# HELP tms_http_request_duration_seconds 请求耗时\n")
		b.WriteString("# TYPE tms_http_request_duration_seconds histogram\n")
		for _, k := range keys {
			h := reqLatency[k]
			if h == nil {
				continue
			}
			var cum uint64
			for i, bound := range latencyBuckets {
				cum += h.counts[i]
				fmt.Fprintf(&b, "tms_http_request_duration_seconds_bucket{method=%q,route=%q,status=\"%d\",le=%q} %d\n",
					k.method, k.pattern, k.status, strconv.FormatFloat(bound, 'g', -1, 64), cum)
			}
			fmt.Fprintf(&b, "tms_http_request_duration_seconds_bucket{method=%q,route=%q,status=\"%d\",le=\"+Inf\"} %d\n",
				k.method, k.pattern, k.status, h.n)
			fmt.Fprintf(&b, "tms_http_request_duration_seconds_sum{method=%q,route=%q,status=\"%d\"} %g\n",
				k.method, k.pattern, k.status, h.sum)
			fmt.Fprintf(&b, "tms_http_request_duration_seconds_count{method=%q,route=%q,status=\"%d\"} %d\n",
				k.method, k.pattern, k.status, h.n)
		}
		metricsMu.Unlock()

		if metricsPool != nil {
			s := metricsPool.Stat()
			b.WriteString("# HELP tms_db_connections 数据库连接池水位\n")
			b.WriteString("# TYPE tms_db_connections gauge\n")
			fmt.Fprintf(&b, "tms_db_connections{state=\"acquired\"} %d\n", s.AcquiredConns())
			fmt.Fprintf(&b, "tms_db_connections{state=\"idle\"} %d\n", s.IdleConns())
			fmt.Fprintf(&b, "tms_db_connections{state=\"max\"} %d\n", s.MaxConns())
			b.WriteString("# HELP tms_db_acquire_wait_seconds_total 获取连接的累计等待时长（持续上涨=池子不够用）\n")
			b.WriteString("# TYPE tms_db_acquire_wait_seconds_total counter\n")
			fmt.Fprintf(&b, "tms_db_acquire_wait_seconds_total %g\n", s.AcquireDuration().Seconds())
		}
		_, _ = w.Write([]byte(b.String()))
	}
}

// routePattern 取 chi 的路由模式；没匹配到时返回空串。
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		return rc.RoutePattern()
	}
	return ""
}
