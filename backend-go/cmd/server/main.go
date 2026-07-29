// 智运 TMS Go 网关（绞杀者架构入口）。
//
// 迁移策略：Go 进程接管对外端口，已移植域（auth/orders…）原生处理，
// 其余路由反向代理回 Django 上游。前端与部署零改动，逐域移植直至 Django 退役。
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/config"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/orders"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/proxy"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/waybills"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect", "err", err)
		os.Exit(1)
	}
	if err := pool.Ping(ctx); err != nil {
		slog.Error("db ping", "err", err)
		os.Exit(1)
	}

	authSvc := &auth.Service{DB: pool}
	issuer := auth.NewIssuer(cfg.SecretKey, cfg.AccessMinutes, cfg.RefreshDays)
	authH := &auth.Handlers{Svc: authSvc, Issuer: issuer, MediaBase: cfg.DjangoUpstream}
	orderH := &orders.Handler{DB: pool, Svc: authSvc}
	waybillH := &waybills.Handler{DB: pool, Svc: authSvc}
	mdH := &masterdata.Handler{DB: pool, Svc: authSvc}
	django := proxy.New(cfg.DjangoUpstream)

	r := chi.NewRouter()
	r.Use(httpx.Recover, httpx.AccessLog, httpx.CORS(cfg.CORSOrigins))

	// 健康探针（Go 自身）
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok", "engine": "go"})
	})

	// ── 已移植域：Go 原生处理 ──
	r.Post("/api/v1/auth/token", authH.Token)
	r.Post("/api/v1/auth/token/refresh", authH.Refresh)
	r.Group(func(p chi.Router) {
		p.Use(authH.RequireAuth)
		p.Get("/api/v1/auth/me", authH.Me)
		p.Get("/api/v1/orders", orderH.List)
		p.Get("/api/v1/orders/funnel", orderH.Funnel)
		p.Get("/api/v1/waybills", waybillH.List)
		p.Get("/api/v1/waybills/stats", waybillH.Stats)
		p.Get("/api/v1/customers", mdH.Customers)
		p.Get("/api/v1/vehicles", mdH.Vehicles)
		p.Get("/api/v1/drivers", mdH.Drivers)
	})

	// ── 其余全部：绞杀者代理回 Django ──
	r.NotFound(django.ServeHTTP)

	slog.Info("gateway up", "addr", cfg.ListenAddr, "django_upstream", cfg.DjangoUpstream)
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
