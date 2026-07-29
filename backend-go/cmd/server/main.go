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

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/analytics"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/audit"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/config"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/exceptions"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/finance"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/orders"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/org"
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
	finH := &finance.Handler{DB: pool}
	anaH := &analytics.Handler{DB: pool, Svc: authSvc}
	orgH := &org.Handler{DB: pool, Svc: authSvc, MD: mdH}
	excH := &exceptions.Handler{DB: pool, Svc: authSvc, MD: mdH}
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
		p.Post("/api/v1/orders/intake", orderH.Intake)
		p.Post("/api/v1/orders/assign", orderH.Assign)
		p.Post("/api/v1/orders/batch-dispatch", orderH.BatchDispatch)
		p.Post("/api/v1/orders/{id}/confirm", orderH.Confirm)
		p.Post("/api/v1/orders/{id}/pool", orderH.Pool)
		p.Post("/api/v1/orders/{id}/cancel", orderH.Cancel)
		p.Post("/api/v1/orders/{id}/claim", orderH.Claim)
		p.Post("/api/v1/orders/{id}/release", orderH.Release)
		p.Post("/api/v1/orders/{id}/unassign", orderH.Unassign)
		p.Post("/api/v1/orders/{id}/dispatch", orderH.Dispatch)
		p.Post("/api/v1/orders/{id}/report-exception", excH.ReportForOrder)
		p.Get("/api/v1/exceptions", excH.List)
		p.Post("/api/v1/exceptions", excH.Create)
		p.Get("/api/v1/orders/{id}", orderH.Detail)
		p.Get("/api/v1/orders/{id}/timeline", orderH.Timeline)
		p.Get("/api/v1/waybills", waybillH.List)
		p.Get("/api/v1/waybills/stats", waybillH.Stats)
		p.Get("/api/v1/waybills/{no}", waybillH.Detail)
		p.Post("/api/v1/waybills/{no}/transition", waybillH.Transition)
		p.Post("/api/v1/waybills/{no}/sign", waybillH.Sign)
		p.Post("/api/v1/waybills/{no}/stop-event", waybillH.StopEvent)
		p.Get("/api/v1/customers", mdH.Customers)
		p.Get("/api/v1/vehicles", mdH.Vehicles)
		p.Get("/api/v1/drivers", mdH.Drivers)
		p.Get("/api/v1/b2b-partners", mdH.B2BPartners)
		p.Get("/api/v1/carriers", mdH.Carriers)
		p.Get("/api/v1/finance/statement-overview", finH.StatementOverview)
		p.Get("/api/v1/finance/statements", finH.Statements(mdH))
		p.Get("/api/v1/finance/aging", finH.Aging)
		p.Get("/api/v1/audit-logs", audit.Logs(authSvc, mdH))
		p.Get("/api/v1/analytics/dashboard", anaH.Dashboard)
		p.Get("/api/v1/credentials/expiring", mdH.ExpiringCredentials)
		p.Get("/api/v1/finance/dashboard-metrics", finH.DashboardMetrics)
		p.Get("/api/v1/workbench", orderH.Workbench)
		p.Get("/api/v1/org/overview", orgH.Overview)
		p.Get("/api/v1/org/organizations", orgH.Organizations)
		p.Post("/api/v1/org/organizations", orgH.CreateOrganization)
		p.Get("/api/v1/org/organizations/tree", orgH.Tree)
		p.Get("/api/v1/org/roles", orgH.Roles)
		p.Post("/api/v1/org/roles/{id}/set-permissions", orgH.SetRolePermissions)
		p.Get("/api/v1/org/rbac/matrix", orgH.RbacMatrix)
		p.Get("/api/v1/org/service-areas", orgH.ServiceAreas)
		p.Post("/api/v1/org/service-areas", orgH.CreateServiceArea)
		p.Get("/api/v1/org/employees", orgH.Employees)
		p.Post("/api/v1/org/employees", orgH.CreateEmployee)
		p.Get("/api/v1/org/employees/{id}/roles", orgH.EmployeeRoles)
		p.Post("/api/v1/org/employees/{id}/roles", orgH.EmployeeRoles)
		p.Post("/api/v1/org/employees/{id}/enable", orgH.ToggleEmployee(true))
		p.Post("/api/v1/org/employees/{id}/disable", orgH.ToggleEmployee(false))
		p.Post("/api/v1/org/employees/{id}/reset-password", orgH.ResetPassword)
		p.Post("/api/v1/org/employees/{id}/handover", orgH.Handover)
		p.Get("/api/v1/org/handovers", orgH.Handovers)
		p.Get("/api/v1/org/login-audit", orgH.LoginAudit)
		p.Get("/api/v1/org/route-resolve", orgH.RouteResolve)
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
