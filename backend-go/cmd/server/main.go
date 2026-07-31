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
	"strings"
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
	authH := &auth.Handlers{Svc: authSvc, Issuer: issuer, MediaBase: cfg.PublicBase,
		MediaRoot: cfg.MediaRoot, Debug: cfg.Debug,
		AllowSelfRegistration: cfg.AllowSelfRegistration}
	orderH := &orders.Handler{DB: pool, Svc: authSvc}
	waybillH := &waybills.Handler{DB: pool, Svc: authSvc}
	mdH := &masterdata.Handler{DB: pool, Svc: authSvc}
	finH := &finance.Handler{DB: pool, Svc: authSvc}
	orderH.Projects = finH // 建单表单可直接新建项目
	anaH := &analytics.Handler{DB: pool, Svc: authSvc}
	orgH := &org.Handler{DB: pool, Svc: authSvc, MD: mdH}
	excH := &exceptions.Handler{DB: pool, Svc: authSvc, MD: mdH}
	ntfH := &notifications.Handler{DB: pool, Svc: authSvc, MD: mdH}
	resH := &resources.Handler{DB: pool, Svc: authSvc, MD: mdH}
	drvH := &driver.Handler{DB: pool, Secret: cfg.SecretKey, MediaRoot: cfg.MediaRoot}
	// 车联网上报走进程内有界队列 + 后台批处理，替代 Redis 队列 + Celery
	ingestor := telematics.NewIngestor(pool)
	ingestor.Start(context.Background())
	ingestor.StartOfflineScanner(context.Background(), 1*time.Minute)
	// MQTT 终端接入（替代 mqtt_gateway 管理命令）；未配 MQTT_HOST 则不启用
	ingestor.StartMQTTGateway(context.Background(), telematics.MQTTOptions{
		Host: cfg.MQTTHost, Port: cfg.MQTTPort, Topic: cfg.MQTTTopic,
		Username: cfg.MQTTUsername, Password: cfg.MQTTPassword,
	})
	telH := &telematics.Handler{DB: pool, Svc: authSvc, In: ingestor}
	// 指标按日物化，替代 Django 的 materialize_metrics 命令（趋势图的数据来源）
	analytics.StartMaterializer(context.Background(), pool, 1*time.Hour, 30)
	// 权限点规范目录落库：库里原先只有 3 行（Django 演示数据残留），而代码校验 12 个，
	// 差额那些在权限矩阵界面上没有行、没法勾选，等于永久 403。见 auth/permcatalog.go。
	if err := auth.EnsurePermissions(ctx, pool); err != nil {
		slog.Warn("permission catalog", "err", err)
	}
	// 令牌黑名单只需覆盖「券还没自然过期」那段窗口，过期条目留着只会让表无限长
	auth.StartDenylistPurger(context.Background(), pool, 1*time.Hour)
	agentH := &agent.Handler{DB: pool, Svc: authSvc, MD: mdH}
	if err := agent.EnsureSchema(ctx, pool); err != nil {
		slog.Warn("agent schema", "err", err)
	}
	// 外部 MCP server 的工具与内置工具同表注册。必须在开始监听之前跑完：
	// 注册表是启动期一次性构建的，之后只读，这样才不用为它上锁。
	agent.LoadMCPTools(ctx, os.Getenv("AGENT_MCP_SERVERS"))

	// detailOnly 包一层：路径参数不是 UUID 时说明命中的其实是同层的集合级动作，
	// 而那些动作都已单独注册——走到这里就是真的没有这个资源。
	detailOnly := func(param string, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, rq *http.Request) {
			if _, err := uuid.Parse(chi.URLParam(rq, param)); err != nil {
				httpx.Err(w, http.StatusNotFound, "error", "Not found.")
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
		p.Post("/api/v1/auth/logout", authH.Logout)
		p.Get("/api/v1/auth/login-history", authH.LoginHistory)
		p.Get("/api/v1/orders", orderH.List)
		p.Get("/api/v1/orders/funnel", orderH.Funnel)
		p.Get("/api/v1/orders/pool", orderH.PoolList)
		p.Get("/api/v1/orders/dispatched", orderH.Dispatched)
		p.Get("/api/v1/orders/dispatchers", orderH.Dispatchers)
		p.Get("/api/v1/orders/customer-addresses", orderH.CustomerAddresses)
		p.Get("/api/v1/orders/export", orderH.Export)
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
		p.Post("/api/v1/orders/{id}/approve", orderH.Approve)
		p.Post("/api/v1/orders/{id}/reject", orderH.RejectApproval)
		p.Post("/api/v1/orders/{id}/split", orderH.Split)
		p.Post("/api/v1/orders/merge", orderH.Merge)
		p.Post("/api/v1/orders/batch", orderH.Batch)
		p.Post("/api/v1/orders/batch-update", orderH.BatchUpdate)
		p.Post("/api/v1/orders/import", orderH.Import)
		p.Post("/api/v1/orders/quote", orderH.Quote)
		p.Post("/api/v1/orders/parse-preview", orderH.ParsePreview)
		// 订单池批量排线，与 /waybills/dispatch-plan 是两个端点：
		// 前者拼同向小单成整车、找承运商，后者给已有运单派自有车
		p.Post("/api/v1/orders/dispatch-plan", orderH.DispatchPlan)
		p.Get("/api/v1/orders/{id}/ymm-quote", orderH.YmmQuote)
		p.Post("/api/v1/orders/{id}/convert", orderH.Convert)
		p.Get("/api/v1/orders/{id}/dispatch-suggestion", orderH.DispatchSuggestion)
		p.Post("/api/v1/dispatch-batches/{id}/statement", finH.BatchStatement)
		p.Post("/api/v1/ai/deepseek/chat", agentH.DeepSeekChat)
		p.Post("/api/v1/ai/query-waybill", agentH.QueryWaybill)
		p.Get("/api/v1/orders/{id}/attachments", orderH.Attachments)
		p.Post("/api/v1/orders/{id}/attachments", orderH.Attachments)
		p.Delete("/api/v1/orders/{id}/attachments/{att_id}", orderH.DeleteAttachment)
		p.Route("/api/v1/exceptions", func(rt chi.Router) {
			mdH.CRUD(exceptions.Cfg, exceptions.Write)(rt)
			rt.Get("/", excH.List)    // 列表的数据范围按运单组织，单独实现
			rt.Post("/", excH.Create) // 建异常要落异常事件并回填 order_id
			rt.Get("/{id}/timeline", excH.Timeline)
			rt.Post("/{id}/assign", excH.Assign)
			rt.Post("/{id}/handle", excH.Handle)
			rt.Post("/{id}/close", excH.Close)
		})
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
		p.Get("/api/v1/waybills/{no}", waybillH.Detail)
		p.Post("/api/v1/waybills/{no}/transition", waybillH.Transition)
		p.Post("/api/v1/waybills/{no}/sign", waybillH.Sign)
		p.Post("/api/v1/waybills/{no}/stop-event", waybillH.StopEvent)
		p.Post("/api/v1/waybills/merge", waybillH.Merge)
		p.Post("/api/v1/waybills/dispatch-plan", waybillH.DispatchPlan)
		p.Get("/api/v1/waybills/{no}/dispatch-recommendation", waybillH.DispatchRecommendation)
		p.Post("/api/v1/waybills/{no}/dispatch", waybillH.Dispatch)
		p.Get("/api/v1/waybills/{no}/events", waybillH.Events)
		p.Post("/api/v1/waybills/{no}/events", waybillH.Events)
		p.Post("/api/v1/waybills/{no}/add-expense", waybillH.AddExpense)
		p.Post("/api/v1/waybills/{no}/contract/send", waybillH.ContractSend)
		p.Post("/api/v1/waybills/{no}/contract/confirm", waybillH.ContractConfirm)
		p.Post("/api/v1/waybills/{no}/partial-sign", waybillH.PartialSign)
		p.Post("/api/v1/waybills/{no}/reject", waybillH.Reject)
		p.Post("/api/v1/waybills/{no}/collect-cod", waybillH.CollectCOD)
		p.Post("/api/v1/waybills/{no}/remit-cod", waybillH.RemitCOD)
		p.Post("/api/v1/waybills/{no}/split", waybillH.Split)
		// 主数据与标准资源：一份读写配置驱动全套 CRUD
		p.Route("/api/v1/customers", func(rt chi.Router) {
			mdH.CRUD(masterdata.CustomersCfg, masterdata.CustomerWrite)(rt)
			rt.Get("/{id}/context", mdH.CustomerContext)
			rt.Get("/{id}/lane-suggest", mdH.CustomerLaneSuggest)
		})
		p.Route("/api/v1/vehicles", mdH.CRUD(masterdata.VehiclesCfg, masterdata.VehicleWrite))
		p.Route("/api/v1/drivers", func(rt chi.Router) {
			rt.Get("/lookup", mdH.DriverLookup) // detail=False，必须排在 {id} 之前
			mdH.CRUD(masterdata.DriversCfg, masterdata.DriverWrite)(rt)
			rt.Post("/{id}/refresh-stats", mdH.DriverRefreshStats)
		})
		p.Route("/api/v1/carriers", func(rt chi.Router) {
			mdH.CRUD(masterdata.CarriersCfg, masterdata.CarrierWrite)(rt)
			rt.Get("/{id}/performance", mdH.CarrierPerformance)
			rt.Post("/{id}/blacklist", mdH.CarrierBlacklist)
		})
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
		p.Route("/api/v1/finance/projects", func(rt chi.Router) {
			// 静态段必须先注册，否则会被 CRUD 的 {id} 吃掉
			rt.Get("/suggest", finH.SuggestProjects)
			mdH.CRUD(resources.ProjectsCfg, resources.ProjectWrite)(rt)
		})
		p.Post("/api/v1/finance/statements/generate", finH.GenerateStatement)
		p.Post("/api/v1/finance/statements/{id}/confirm", finH.ConfirmStatement)
		p.Post("/api/v1/finance/statements/{id}/audit", finH.AuditStatement)
		p.Post("/api/v1/finance/statements/{id}/settle", finH.SettleStatement)
		p.Get("/api/v1/finance/statements/{id}/payments", finH.StatementPayments)
		p.Post("/api/v1/waybills/{no}/generate-costs", finH.GenerateCosts)
		p.Get("/api/v1/finance/statement-overview", finH.StatementOverview)
		p.Get("/api/v1/finance/statements", finH.Statements(mdH))
		p.Get("/api/v1/finance/statements/{id}", func(w http.ResponseWriter, rq *http.Request) {
			if authSvc.Guard(w, rq, finance.PermView, "无财务查看权限") == nil {
				return
			}
			mdH.Retrieve(w, rq, finance.StatementDetailCfg, finance.StatementWrite)
		})
		p.Get("/api/v1/finance/aging", finH.Aging)
		p.Get("/api/v1/audit-logs", audit.Logs(authSvc, mdH))
		p.Get("/api/v1/audit-logs/{id}", audit.Detail(authSvc, mdH))
		p.Get("/api/v1/lookup", orderH.Lookup)
		p.Get("/api/v1/integrations/status", orderH.IntegrationStatus)
		p.Get("/api/v1/analytics/dashboard", anaH.Dashboard)
		p.Get("/api/v1/analytics/metrics", anaH.MetricCatalog)
		p.Post("/api/v1/analytics/metrics/query", anaH.MetricQuery)
		p.Get("/api/v1/analytics/metrics/{code}/trend", anaH.MetricTrend)
		p.Get("/api/v1/analytics/catalog", anaH.DataCatalog)
		p.Get("/api/v1/credentials/expiring", mdH.ExpiringCredentials)
		p.Get("/api/v1/finance/dashboard-metrics", finH.DashboardMetrics)
		p.Get("/api/v1/workbench", orderH.Workbench)
		p.Get("/api/v1/ai/deepseek/status", agentH.Status)
		p.Get("/api/v1/agent/tools", agentH.Tools)
		p.Post("/api/v1/agent/tools/execute", agentH.Execute)
		p.Post("/api/v1/agent/chat", agentH.Chat)
		p.Get("/api/v1/ai/suggestions", agentH.Suggestions)
		p.Get("/api/v1/ai/suggestions/{id}", agentH.SuggestionDetail)
		p.Post("/api/v1/ai/suggestions/{id}/confirm", agentH.ConfirmSuggestion)
		p.Get("/api/v1/org/overview", orgH.Overview)
		p.Get("/api/v1/org/rbac/matrix", orgH.RbacMatrix)
		// 组织中台的标准资源全部走通用引擎；只有 detail=False 的动作需要单独注册
		p.Route("/api/v1/org/organizations", func(rt chi.Router) {
			rt.Get("/tree", orgH.Tree)
			rt.Get("/export", orgH.ExportOrganizations)
			mdH.CRUD(org.OrganizationsCfg, org.OrganizationWrite)(rt)
		})
		p.Route("/api/v1/org/roles", func(rt chi.Router) {
			mdH.CRUD(org.RolesCfg, org.RoleWrite)(rt)
			rt.Post("/{id}/set-permissions", orgH.SetRolePermissions)
		})
		p.Route("/api/v1/org/service-areas", mdH.CRUD(org.ServiceAreasCfg, org.ServiceAreaWrite))
		p.Route("/api/v1/org/handovers", mdH.CRUD(org.HandoversCfg, org.HandoverWrite))
		p.Route("/api/v1/org/login-audit", func(rt chi.Router) {
			rt.Post("/unlock", orgH.UnlockLogin)
			mdH.CRUD(org.LoginAuditCfg, org.LoginAuditWrite)(rt)
		})
		p.Route("/api/v1/org/employees", func(rt chi.Router) {
			rt.Get("/export", orgH.ExportEmployees)
			rt.Post("/import", orgH.ImportEmployees)
			mdH.CRUD(org.EmployeesCfg, org.EmployeeWrite)(rt)
			rt.Get("/{id}/roles", orgH.EmployeeRoles)
			rt.Post("/{id}/roles", orgH.EmployeeRoles)
			rt.Post("/{id}/enable", orgH.ToggleEmployee(true))
			rt.Post("/{id}/disable", orgH.ToggleEmployee(false))
			rt.Post("/{id}/reset-password", orgH.ResetPassword)
			rt.Post("/{id}/handover", orgH.Handover)
		})
		p.Get("/api/v1/org/route-resolve", orgH.RouteResolve)
	})

	// 媒体文件（头像/回单/证件/打卡照/合同）：Django 退役后由网关自服务。
	// nosniff 是必须的——这些是用户上传物，绝不能让浏览器按内容猜出可执行类型。
	media := http.StripPrefix("/media/", http.FileServer(http.Dir(cfg.MediaRoot)))
	r.Handle("/media/*", http.HandlerFunc(
		func(w http.ResponseWriter, rq *http.Request) {
			// 不开目录列表。判定必须在 StripPrefix 之前做：`/media/` 自身被削完就是
			// 空串，尾斜杠判断落空，FileServer 会把整个上传目录列出来。
			if strings.HasSuffix(rq.URL.Path, "/") {
				httpx.Err(w, http.StatusNotFound, "not_found", "未找到。")
				return
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			media.ServeHTTP(w, rq)
		}))

	// 未知路由：DRF 同款 404 信封
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		httpx.Err(w, http.StatusNotFound, "not_found", "未找到。")
	})

	slog.Info("gateway up", "addr", cfg.ListenAddr)
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
