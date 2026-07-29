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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/agent"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/analytics"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/audit"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/config"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/driver"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/exceptions"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/finance"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/migrate"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/notifications"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/orders"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/org"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/proxy"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/resources"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/telematics"
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

	// Go 侧自有 schema 由内嵌迁移器管（收官时 Django 表所有权也移交到这里）
	if err := migrate.Run(ctx, pool); err != nil {
		slog.Error("schema migrate", "err", err)
		os.Exit(1)
	}

	authSvc := &auth.Service{DB: pool}
	issuer := auth.NewIssuer(cfg.SecretKey, cfg.AccessMinutes, cfg.RefreshDays)
	authH := &auth.Handlers{Svc: authSvc, Issuer: issuer, MediaBase: cfg.DjangoUpstream,
		MediaRoot: cfg.MediaRoot, Debug: cfg.Debug}
	orderH := &orders.Handler{DB: pool, Svc: authSvc}
	waybillH := &waybills.Handler{DB: pool, Svc: authSvc}
	mdH := &masterdata.Handler{DB: pool, Svc: authSvc}
	finH := &finance.Handler{DB: pool}
	anaH := &analytics.Handler{DB: pool, Svc: authSvc}
	orgH := &org.Handler{DB: pool, Svc: authSvc, MD: mdH}
	excH := &exceptions.Handler{DB: pool, Svc: authSvc, MD: mdH}
	ntfH := &notifications.Handler{DB: pool, Svc: authSvc, MD: mdH}
	django := proxy.New(cfg.DjangoUpstream)
	mdH.Fallback = django // CRUD 子路由未声明的自定义动作仍回代上游
	resH := &resources.Handler{DB: pool, Svc: authSvc, MD: mdH}
	drvH := &driver.Handler{DB: pool, Secret: cfg.SecretKey, MediaRoot: cfg.MediaRoot}
	// 车联网上报走进程内有界队列 + 后台批处理，替代 Redis 队列 + Celery
	ingestor := telematics.NewIngestor(pool)
	ingestor.Start(context.Background())
	ingestor.StartOfflineScanner(context.Background(), 1*time.Minute)
	telH := &telematics.Handler{DB: pool, Svc: authSvc, In: ingestor}
	agentH := &agent.Handler{DB: pool, Svc: authSvc, MD: mdH, Fallback: django}
	if err := agent.EnsureSchema(ctx, pool); err != nil {
		slog.Warn("agent schema", "err", err)
	}

	// detailOnly 包一层：路径参数不是 UUID 时，说明命中的其实是集合级动作
	// （/orders/pool、/orders/export…）——chi 的 {id} 会先把它吃掉，导致这些端点
	// 变成 404。凡是「静态段与 {id} 同层」的路由都必须显式回代上游。
	detailOnly := func(param string, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, rq *http.Request) {
			if _, err := uuid.Parse(chi.URLParam(rq, param)); err != nil {
				django.ServeHTTP(w, rq)
				return
			}
			next(w, rq)
		}
	}

	r := chi.NewRouter()
	r.Use(httpx.Recover, httpx.AccessLog, httpx.CORS(cfg.CORSOrigins))

	// 健康探针（Go 自身）
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok", "engine": "go"})
	})

	// ── 已移植域：Go 原生处理 ──
	r.Post("/api/v1/auth/token", authH.Token)
	r.Post("/api/v1/auth/token/refresh", authH.Refresh)
	r.Post("/api/v1/auth/token/verify", authH.TokenVerify)
	r.Get("/api/v1/auth/methods", authH.AuthMethods)
	r.Post("/api/v1/auth/register", authH.Register)
	r.Post("/api/v1/auth/password-reset/request", authH.PasswordResetRequest)
	r.Post("/api/v1/auth/password-reset/confirm", authH.PasswordResetConfirm)
	// ── 司机端 H5 与公开域：免登录，各自带自证机制（详见各 handler 注释）──
	r.Post("/api/v1/driver/login", drvH.Login)
	r.Get("/api/v1/driver/tasks", drvH.Tasks)
	r.Post("/api/v1/driver/checkin", drvH.Checkin)
	r.Post("/api/v1/driver/credentials", drvH.UploadCredential)
	r.Post("/api/v1/driver/reminders/{id}/ack", drvH.AckReminder)
	r.Post("/api/v1/public/orders", orderH.PublicIntake)
	r.Get("/api/v1/track", orderH.PublicTrack)
	r.Group(func(p chi.Router) {
		p.Use(authH.RequireAuth)
		p.Get("/api/v1/auth/me", authH.Me)
		p.Patch("/api/v1/auth/me", authH.MePatch)
		p.Post("/api/v1/auth/me/avatar", authH.Avatar)
		p.Delete("/api/v1/auth/me/avatar", authH.Avatar)
		p.Post("/api/v1/auth/change-password", authH.ChangePassword)
		p.Get("/api/v1/auth/login-history", authH.LoginHistory)
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
		p.Post("/api/v1/orders/{id}/clone", orderH.Clone)
		p.Post("/api/v1/orders/{id}/edit", orderH.Edit)
		p.Post("/api/v1/orders/{id}/report-exception", excH.ReportForOrder)
		p.Get("/api/v1/exceptions", excH.List)
		p.Post("/api/v1/exceptions", excH.Create)
		p.Get("/api/v1/notifications", ntfH.List)
		p.Get("/api/v1/notifications/unread-count", ntfH.UnreadCount)
		p.Post("/api/v1/notifications/{id}/read", ntfH.Read)
		p.Post("/api/v1/notifications/read-all", ntfH.ReadAll)
		p.Get("/api/v1/orders/{id}", detailOnly("id", orderH.Detail))
		p.Get("/api/v1/orders/{id}/timeline", orderH.Timeline)
		p.Get("/api/v1/orders/{id}/workflow", orderH.Workflow)
		p.Get("/api/v1/orders/{id}/lineage", orderH.Lineage)
		p.Get("/api/v1/waybills", waybillH.List)
		p.Get("/api/v1/waybills/stats", waybillH.Stats)
		p.Get("/api/v1/waybills/cost-catalog", waybillH.CostCatalog)
		p.Get("/api/v1/waybills/{no}/costs", waybillH.Costs)
		p.Get("/api/v1/waybills/{no}/eta", waybillH.ETA)
		p.Get("/api/v1/waybills/{no}/collection", waybillH.Collection)
		p.Get("/api/v1/waybills/{no}/finance-card", waybillH.FinanceCard)
		p.Get("/api/v1/waybills/{no}/reply-card", waybillH.ReplyCard)
		p.Get("/api/v1/waybills/{no}/contract", waybillH.Contract)
		p.Get("/api/v1/waybills/{no}/reminders", waybillH.Reminders)
		p.Get("/api/v1/waybills/{no}/tracking", telH.WaybillTracking)
		p.Post("/api/v1/tracking/points", telH.TrackingIngest)
		p.Post("/api/v1/telematics/ingest", telH.Ingest)
		p.Get("/api/v1/telematics/vehicles/live", telH.Live)
		p.Get("/api/v1/telematics/waybills/{no}/trajectory", telH.Trajectory)
		p.Get("/api/v1/telematics/command-center/summary", telH.CommandCenterSummary)
		// 与 {no} 同层的集合级动作：运单号不是 UUID，没法靠格式判别，
		// 只能显式挂到代理上（随各自域移植后从这里摘掉）。
		p.Post("/api/v1/waybills/dispatch-plan", django.ServeHTTP)
		p.Post("/api/v1/waybills/merge", django.ServeHTTP)
		p.Get("/api/v1/waybills/{no}", waybillH.Detail)
		p.Post("/api/v1/waybills/{no}/transition", waybillH.Transition)
		p.Post("/api/v1/waybills/{no}/sign", waybillH.Sign)
		p.Post("/api/v1/waybills/{no}/stop-event", waybillH.StopEvent)
		// 主数据与标准资源：一份读写配置驱动全套 CRUD
		p.Route("/api/v1/customers", mdH.CRUD(masterdata.CustomersCfg, masterdata.CustomerWrite))
		p.Route("/api/v1/vehicles", mdH.CRUD(masterdata.VehiclesCfg, masterdata.VehicleWrite))
		p.Route("/api/v1/drivers", mdH.CRUD(masterdata.DriversCfg, masterdata.DriverWrite))
		p.Route("/api/v1/carriers", mdH.CRUD(masterdata.CarriersCfg, masterdata.CarrierWrite))
		p.Route("/api/v1/b2b-partners", mdH.CRUD(masterdata.B2BCfg, masterdata.B2BWrite))
		p.Route("/api/v1/routes", mdH.CRUD(masterdata.RoutesCfg, masterdata.RouteWrite))
		p.Route("/api/v1/carrier-lane-prices", mdH.CRUD(masterdata.LanePricesCfg, masterdata.LanePriceWrite))
		p.Route("/api/v1/driver-credentials", func(rt chi.Router) {
			mdH.CRUD(masterdata.DriverCredsCfg, masterdata.DriverCredWrite)(rt)
			rt.Post("/{id}/ocr", mdH.CredentialOCR)
		})
		// 其余标准 ModelViewSet 资源：同一份引擎，配置在 internal/resources
		p.Route("/api/v1/order-templates", mdH.CRUD(resources.OrderTemplatesCfg, resources.OrderTemplateWrite))
		p.Route("/api/v1/reminder-templates", mdH.CRUD(resources.ReminderTemplatesCfg, resources.ReminderTemplateWrite))
		p.Route("/api/v1/reminders", func(rt chi.Router) {
			mdH.CRUD(resources.DriverRemindersCfg, resources.DriverReminderWrite)(rt)
			rt.Post("/{id}/acknowledge", resH.ReminderAcknowledge)
		})
		p.Route("/api/v1/receipts", func(rt chi.Router) {
			mdH.CRUD(resources.ReceiptsCfg, resources.ReceiptWrite)(rt)
			rt.Post("/{id}/confirm", resH.ReceiptConfirm)
		})
		p.Route("/api/v1/dispatch-batches", mdH.CRUD(resources.DispatchBatchesCfg, resources.DispatchBatchWrite))
		p.Route("/api/v1/org/departments", mdH.CRUD(resources.DepartmentsCfg, resources.DepartmentWrite))
		p.Route("/api/v1/org/employee-groups", mdH.CRUD(resources.EmployeeGroupsCfg, resources.EmployeeGroupWrite))
		p.Route("/api/v1/org/permissions", mdH.CRUD(resources.PermissionsCfg, resources.PermissionWrite))
		p.Route("/api/v1/telematics/devices", mdH.CRUD(resources.DevicesCfg, resources.DeviceWrite))
		p.Route("/api/v1/telematics/geofences", mdH.CRUD(resources.GeofencesCfg, resources.GeofenceWrite))
		p.Route("/api/v1/telematics/alerts", func(rt chi.Router) {
			mdH.CRUD(resources.AlertsCfg, resources.AlertWrite)(rt)
			rt.Post("/{id}/ack", resH.AlertTransition("acknowledged"))
			rt.Post("/{id}/close", resH.AlertTransition("closed"))
		})
		p.Route("/api/v1/finance/expense-items", mdH.CRUD(resources.ExpenseItemsCfg, resources.ExpenseItemWrite))
		p.Route("/api/v1/finance/expense-records", mdH.CRUD(resources.ExpenseRecordsCfg, resources.ExpenseRecordWrite))
		p.Route("/api/v1/finance/payment-requests", mdH.CRUD(resources.PaymentRequestsCfg, resources.PaymentRequestWrite))
		p.Route("/api/v1/finance/pricing-rules", mdH.CRUD(resources.PricingRulesCfg, resources.PricingRuleWrite))
		p.Route("/api/v1/finance/webhooks", mdH.CRUD(resources.WebhooksCfg, resources.WebhookWrite))
		p.Route("/api/v1/finance/webhook-deliveries", mdH.CRUD(resources.WebhookDeliveriesCfg, resources.WebhookDeliveryWrite))
		p.Route("/api/v1/finance/reimbursements", func(rt chi.Router) {
			mdH.CRUD(resources.ReimbursementsCfg, resources.ReimbursementWrite)(rt)
			rt.Post("/", resH.ReimbursementCreate) // ViewSet.create 完全重写
			rt.Post("/{id}/approve", resH.ReimbursementApprove)
			rt.Post("/{id}/reject", resH.ReimbursementReject)
			rt.Post("/{id}/pay", resH.ReimbursementPay)
		})
		p.Post("/api/v1/finance/payment-results", resH.PaymentResult)
		p.Route("/api/v1/finance/contracts", mdH.CRUD(resources.ContractsCfg, resources.ContractWrite))
		p.Post("/api/v1/waybills/{no}/generate-costs", finH.GenerateCosts)
		p.Get("/api/v1/finance/statement-overview", finH.StatementOverview)
		p.Get("/api/v1/finance/statements", finH.Statements(mdH))
		p.Get("/api/v1/finance/aging", finH.Aging)
		p.Get("/api/v1/audit-logs", audit.Logs(authSvc, mdH))
		p.Get("/api/v1/analytics/dashboard", anaH.Dashboard)
		p.Get("/api/v1/credentials/expiring", mdH.ExpiringCredentials)
		p.Get("/api/v1/finance/dashboard-metrics", finH.DashboardMetrics)
		p.Get("/api/v1/workbench", orderH.Workbench)
		p.Get("/api/v1/ai/deepseek/status", agentH.Status)
		p.Get("/api/v1/agent/tools", agentH.Tools)
		p.Post("/api/v1/agent/tools/execute", agentH.Execute)
		p.Post("/api/v1/agent/chat", agentH.Chat)
		p.Get("/api/v1/ai/suggestions", agentH.Suggestions)
		p.Post("/api/v1/ai/suggestions/{id}/confirm", agentH.ConfirmSuggestion)
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
