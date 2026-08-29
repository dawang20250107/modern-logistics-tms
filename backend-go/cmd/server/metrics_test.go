package main

// /metrics 的两条要害：**默认关闭**，以及**标签基数有界**。
//
// 基数那条是这类端点最经典的翻车方式：拿 r.URL.Path 当标签，
// /api/v1/orders/<uuid> 每个订单就是一条新时间序列，几万单之后
// Prometheus 内存被打爆——而且是上线几周后才爆，最难查的那种。
// 这里用五个不同 UUID 打同一条路由，断言只产生一条序列。

import (
	"net/http"
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
	var n int
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, "tms_http_requests_total{") && strings.Contains(line, "/orders/") {
			n++
		}
	}
	if n == 0 {
		t.Fatal("一条 orders 相关的时间序列都没有，指标没被记录")
	}
	if n > 1 {
		t.Errorf("同一条参数化路由产生了 %d 条时间序列 —— 标签用的是原始路径而不是"+
			"chi 的路由模式。几万单之后 Prometheus 会被基数打爆，"+
			"而且是上线几周后才爆。", n)
	}
	// 顺带确认导出的是路由模式而不是具体 id
	if !strings.Contains(rec.Body.String(), `route="/api/v1/orders/{id}"`) {
		t.Error("标签里应出现路由模式 /api/v1/orders/{id}")
	}
}
