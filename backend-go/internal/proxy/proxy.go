// Package proxy 绞杀者反向代理：未移植到 Go 的路由原样转发给 Django 上游，
// 保证迁移期系统 100% 可用；每移植完一个资源域，就在路由表把它从代理面摘下。
package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func New(upstream string) http.Handler {
	target, err := url.Parse(upstream)
	if err != nil {
		panic("bad DJANGO_UPSTREAM: " + err.Error())
	}
	p := httputil.NewSingleHostReverseProxy(target)
	// CORS 单一来源：网关根中间件统一加头，剥掉 Django corsheaders 的同名头避免重复
	p.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Access-Control-Allow-Origin")
		resp.Header.Del("Access-Control-Allow-Credentials")
		resp.Header.Del("Access-Control-Allow-Methods")
		resp.Header.Del("Access-Control-Allow-Headers")
		return nil
	}
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		slog.Error("proxy upstream error", "path", r.URL.Path, "err", e)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"success":false,"data":null,"error":{"code":"UPSTREAM_DOWN","message":"上游服务暂不可用","details":null}}`))
	}
	return p
}
