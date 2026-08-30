package httpx

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// AllowedRequestHeaders 预检放行的请求头。
//
// 这个列表少一个头，对应的整块功能在浏览器里就是死的——而且**服务端一切正常**：
// 预检返回 204，业务请求压根没发出去，服务端日志上什么都看不到，
// 界面只报一句 "Failed to fetch"。
//
// X-Driver-Token 就是这么漏掉的：司机端登录（无自定义头）返回 200，
// 紧接着的 /driver/tasks 带上 X-Driver-Token 触发预检，被浏览器挡下。
// 于是**司机端登录之后是一片空白**，五个接口一个都调不到。
// 前后端同源部署（nginx 反代）时这条不生效，所以预发很可能复现不出来；
// 而 DJANGO_CORS_ORIGINS 这个配置存在的意义正是支持前端单独部署的场景。
//
// 加自定义请求头时必须同时加到这里。有用例钉着（middleware_test.go）:
// 它直接扫前端源码里所有 headers.set("X-…")，漏一个就红。
var AllowedRequestHeaders = []string{
	"Authorization",
	"Content-Type",
	"X-Driver-Token", // 司机端令牌，见 DriverPortalPage.tsx
}

// CORS 网关根中间件：统一应答所有路由（含代理路由）的预检与响应头。
// 代理层会剥掉 Django corsheaders 的同名头，保证单一来源不重复。
// 放在路由匹配之前，OPTIONS 预检对未注册方法的路径同样生效（否则 chi 405 无头，浏览器拦截）。
func CORS(allowed []string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if _, ok := set[origin]; ok {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Vary", "Origin")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", strings.Join(AllowedRequestHeaders, ", "))
				h.Set("Access-Control-Max-Age", "86400")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Recover 兜底 panic → 500 信封，进程不崩。
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "path", r.URL.Path, "err", rec)
				Err(w, http.StatusInternalServerError, "INTERNAL", "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }

// AccessLog 结构化访问日志（对齐 Django apps.access 的字段习惯）。
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		elapsed := time.Since(start)
		// 指标要在 next 之后取：chi 是在路由匹配时才把模式写进 RouteContext 的
		observe(r.Method, routePattern(r), sw.status, elapsed.Seconds())

		// 5xx 与慢请求提到 Warn/Error：上线后翻日志时，
		// 「哪里出错了、哪里慢」不该淹在每秒几十条的 INFO 里。
		lvl := slog.LevelInfo
		switch {
		case sw.status >= 500:
			lvl = slog.LevelError
		case sw.status >= 400 || elapsed > 2*time.Second:
			lvl = slog.LevelWarn
		}
		slog.Log(r.Context(), lvl, "req",
			"method", r.Method, "path", r.URL.Path, "route", routePattern(r),
			"status", sw.status, "duration_ms", float64(elapsed.Microseconds())/1000,
			// nginx 会带 X-Request-ID 进来；一条请求在网关与前置日志里能对上号
			"request_id", r.Header.Get("X-Request-ID"),
		)
	})
}
