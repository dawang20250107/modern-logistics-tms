package main

// /metrics 的两条要害：**默认关闭**，以及**标签基数有界**。
//
// 基数那条是这类端点最经典的翻车方式：拿 r.URL.Path 当标签，
// /api/v1/orders/<uuid> 每个订单就是一条新时间序列，几万单之后
// Prometheus 内存被打爆——而且是上线几周后才爆，最难查的那种。
// 这里用五个不同 UUID 打同一条路由，断言只产生一条序列。

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const metricsTestToken = "test-metrics-token"

// 未配 METRICS_TOKEN 时端点应当**不存在**（404 而不是 401）：
// 401 等于告诉扫描器「这里有个端点，只是要凭证」。
func TestMetricsIsInvisibleWithoutToken(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "")
	e := newTestEnv(t)
	rec := e.call("", "GET", "/metrics", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("未配 METRICS_TOKEN 时期望 404（端点不存在），实际 %d：%s",
			rec.Code, truncate(rec.Body.String(), 120))
	}
}

// 配了 token 但没带凭证 → 401
func TestMetricsRequiresToken(t *testing.T) {
	t.Setenv("METRICS_TOKEN", metricsTestToken)
	e := newTestEnv(t)
	if rec := e.call("", "GET", "/metrics", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("配了 token 却不带凭证时期望 401，实际 %d", rec.Code)
	}
	if rec := e.call("wrong-token", "GET", "/metrics", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("token 不匹配时期望 401，实际 %d", rec.Code)
	}
}

func TestMetricsLabelCardinalityIsBounded(t *testing.T) {
	t.Setenv("METRICS_TOKEN", metricsTestToken)
	e := newTestEnv(t)
	token := e.mkUser(false)

	// 五个不同 UUID 打同一条参数化路由
	for i := 0; i < 5; i++ {
		e.call(token, "GET", "/api/v1/orders/"+uuid.NewString(), "")
	}

	rec := e.call(metricsTestToken, "GET", "/metrics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("带正确 token 应回 200，实际 %d：%s", rec.Code, truncate(rec.Body.String(), 160))
	}
	// 断言的是「label 里不能出现原始 id」，不是「series 条数等于 1」。
	//
	// 第一版数的是 route 含 "/orders/" 的行数，要求恰好 1 条。单独跑能过，
	// 跟整个包一起跑就挂——指标注册表是进程级全局的，鉴权矩阵那些用例
	// 也打了一堆 /orders/... 路由，series 早就攒了好几条。
	// 而且 routeKey 含 status，同一条路由本来就可以合法地有多条 series
	// （200 一条、404 一条）。所以按条数断言从一开始就是错的口径：
	// 它既会误报，也没有真的盯住要盯的东西。
	//
	// 真正要盯的是：导出的 route 标签里永远不许出现具体 id。
	// 这个判断跟执行顺序、跟别的用例攒了多少 series 都无关。
	uuidLike := regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-`)
	body := rec.Body.String()
	sawPattern := false
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "tms_http_requests_total{") {
			continue
		}
		if uuidLike.MatchString(line) {
			t.Errorf("时间序列的标签里出现了具体 id —— 用的是原始路径而不是 chi 的"+
				"路由模式。几万单之后 Prometheus 会被基数打爆，而且是上线几周后才爆。\n  %s",
				truncate(line, 160))
		}
		if strings.Contains(line, `route="/api/v1/orders/{id}"`) {
			sawPattern = true
		}
	}
	if !sawPattern {
		t.Error("没有 route=\"/api/v1/orders/{id}\" 的时间序列，指标压根没记上")
	}
}
