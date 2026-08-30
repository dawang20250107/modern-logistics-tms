// 智运 TMS Go 网关（绞杀者架构入口）。
//
// 迁移策略：Go 进程接管对外端口，已移植域（auth/orders…）原生处理，
// 其余路由反向代理回 Django 上游。前端与部署零改动，逐域移植直至 Django 退役。
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/agent"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/analytics"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/audit"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/blob"
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

	// 生产配置前置检查：把「配错了也能跑起来」改成开不了机。
	// 最要紧的一条是 DJANGO_SECRET_KEY 还留着占位值——它同时签发内部用户令牌
	// 与司机端令牌，而这类问题在测试环境永远不会暴露（那里本来就用默认值）。
	if err := cfg.Preflight(); err != nil {
		slog.Error("启动前置检查失败", "err", err)
		os.Exit(1)
	}
	for _, w := range cfg.Warnings() {
		slog.Warn("配置提醒", "msg", w)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := newPool(ctx, cfg)
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

	// 常驻任务活到进程结束
	r := buildRouter(ctx, context.Background(), pool, cfg)

	slog.Info("gateway up", "addr", cfg.ListenAddr)
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// buildRouter 组装全部依赖与 155 条路由，返回可直接 ServeHTTP 的处理器。
//
// 从 main 里拆出来只为一件事：**让路由可被 httptest 打**。
// 在此之前全库没有一个 HTTP 层测试（唯一一处 httptest 是 agent 包里的假 MCP server），
// 于是「这条路由挂没挂权限闸」只能靠人手 curl 去试——而财务那个洞正是这么漏掉的：
// 没有任何自动化能发现「新加了一条路由但忘了加闸」。
// 拆出来之后，router_test.go 就能把那张手工探针表钉成回归用例。
// newPool 建连接池，并且**显式**设定池大小。
//
// 原来是裸的 pgxpool.New(ctx, url)，池大小走 pgx 默认的 max(4, NumCPU)。
// 这个默认值在演示数据上看不出问题（查询快到连接根本不会被占住），
// 一旦数据上量就变成整个网关的吞吐天花板：
// 实测 5 万单时，4 条连接把吞吐锁死在 221 req/s，p95 124ms；
// 池开到 20 之后同样的压测跑到 —— 见 cmd/loadtest 的说明。
//
// MaxConnLifetime 存在的理由不是性能，是运维：连接活得越久，
// 越可能在某次 failover / 连接池代理重启之后拿着一条实际已死的连接。
// 定期回收让这类故障自愈，代价只是偶尔多握一次手。
func newPool(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	pc.MaxConns = cfg.DBMaxConns
	pc.MinConns = cfg.DBMinConns
	pc.MaxConnLifetime = 30 * time.Minute
	pc.MaxConnIdleTime = 5 * time.Minute
	pc.HealthCheckPeriod = 30 * time.Second
	return pgxpool.NewWithConfig(ctx, pc)
}

// buildRouter 组装路由，并拉起后台常驻任务。
//
// 两个 ctx 是有意分开的，它们管的不是同一件事：
//
//	· startCtx —— 启动期那些一次性动作（建 schema、灌权限目录、拉 MCP 工具），
//	  应该有超时，卡住了就该让启动失败而不是干等。
//	· workerCtx —— 常驻任务（削峰、掉线扫描、指标物化、各种清理器）的生命周期，
//	  生产上就是进程的一生。
//
// 原先常驻任务一律写死 context.Background()，因为 startCtx 带 10 秒超时，
// 用它会让所有后台任务十秒后集体停摆。那在生产上没问题，但测试里
// buildRouter 被调用几十次，每次都漏 6 个永不退出的 goroutine 继续打库——
// 于是用例之间会互相干扰，池关掉之后还会刷一屏 "closed pool"。
// 分成两个参数，调用方就能明确表达"这批后台任务活多久"。
// inlineSafeMedia 允许按原类型内联发出的媒体类型。
//
// 判据是"浏览器拿它当页面渲染时会不会执行脚本"：
// 图片和纯文本不会；PDF 会跑自己的脚本，但那是在阅读器的沙箱里，
// 拿不到页面的同源上下文——而回单和证件必须能在界面上直接看，
// 所以这两类留在名单里。其余一律降成八位字节流强制下载。
var inlineSafeMedia = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
	"image/bmp": true, "image/tiff": true,
	"application/pdf": true,
	"text/plain":      true, "text/csv": true,
}

func buildRouter(startCtx, workerCtx context.Context, pool *pgxpool.Pool, cfg config.Config) http.Handler {
	// 媒体存放。配错（比如 MEDIA_BACKEND=s3 但少了 S3_BUCKET）就直接退出，
	// 不静默退回本地盘——退回去会让多副本部署"看起来正常"地间歇丢文件。
	store, err := blob.FromEnv()
	if err != nil {
		slog.Error("媒体存储配置有误", "err", err)
		os.Exit(1)
	}
	slog.Info("媒体存储", "backend", store.Kind())
	authSvc := &auth.Service{DB: pool}
	issuer := auth.NewIssuer(cfg.SecretKey, cfg.AccessMinutes, cfg.RefreshDays)
	authH := &auth.Handlers{Svc: authSvc, Issuer: issuer, MediaBase: cfg.PublicBase,
		MediaRoot: cfg.MediaRoot, Blob: store, Debug: cfg.Debug,
		AllowSelfRegistration: cfg.AllowSelfRegistration,
		ResetSender:           auth.NewSender()}
	orderH := &orders.Handler{DB: pool, Svc: authSvc, MediaRoot: cfg.MediaRoot, Blob: store}
	waybillH := &waybills.Handler{DB: pool, Svc: authSvc}
	mdH := &masterdata.Handler{DB: pool, Svc: authSvc, MediaRoot: cfg.MediaRoot, Blob: store}
	finH := &finance.Handler{DB: pool, Svc: authSvc}
	orderH.Projects = finH // 建单表单可直接新建项目
	anaH := &analytics.Handler{DB: pool, Svc: authSvc}
	orgH := &org.Handler{DB: pool, Svc: authSvc, MD: mdH}
	excH := &exceptions.Handler{DB: pool, Svc: authSvc, MD: mdH}
	ntfH := &notifications.Handler{DB: pool, Svc: authSvc, MD: mdH}
	resH := &resources.Handler{DB: pool, Svc: authSvc, MD: mdH}
	drvH := &driver.Handler{DB: pool, Secret: cfg.SecretKey, MediaRoot: cfg.MediaRoot, Blob: store}
	// 车联网上报走进程内有界队列 + 后台批处理，替代 Redis 队列 + Celery
	ingestor := telematics.NewIngestor(pool)
	ingestor.Start(workerCtx)
	ingestor.StartOfflineScanner(workerCtx, 1*time.Minute)
	// MQTT 终端接入（替代 mqtt_gateway 管理命令）；未配 MQTT_HOST 则不启用
	ingestor.StartMQTTGateway(workerCtx, telematics.MQTTOptions{
		Host: cfg.MQTTHost, Port: cfg.MQTTPort, Topic: cfg.MQTTTopic,
		Username: cfg.MQTTUsername, Password: cfg.MQTTPassword,
	})
	telH := &telematics.Handler{DB: pool, Svc: authSvc, In: ingestor}
	// 指标按日物化，替代 Django 的 materialize_metrics 命令（趋势图的数据来源）
	analytics.StartMaterializer(workerCtx, pool, 1*time.Hour, 30)
	// 权限点规范目录落库：库里原先只有 3 行（Django 演示数据残留），而代码校验 12 个，
	// 差额那些在权限矩阵界面上没有行、没法勾选，等于永久 403。见 auth/permcatalog.go。
	if err := auth.EnsurePermissions(startCtx, pool); err != nil {
		slog.Warn("permission catalog", "err", err)
	}
	// 令牌黑名单只需覆盖「券还没自然过期」那段窗口，过期条目留着只会让表无限长
	auth.StartDenylistPurger(workerCtx, pool, 1*time.Hour)
	// 登录锁定 / 限流 / 找回密码验证码从进程内 map 改为共享存储。
	// 不注入的话它们仍走进程内 map —— 那在单副本下没问题，多副本下
	// 前两者是安全闸被稀释、后者是功能直接断（见 007 迁移的说明）。
	auth.UseSharedStore(pool)
	httpx.UseSharedStore(pool)
	auth.StartRuntimeStatePurger(workerCtx, pool, 30*time.Minute)
	agentH := &agent.Handler{DB: pool, Svc: authSvc, MD: mdH}
	if err := agent.EnsureSchema(startCtx, pool); err != nil {
		slog.Warn("agent schema", "err", err)
	}
	// 外部 MCP server 的工具与内置工具同表注册。必须在开始监听之前跑完：
	// 注册表是启动期一次性构建的，之后只读，这样才不用为它上锁。
	agent.LoadMCPTools(startCtx, os.Getenv("AGENT_MCP_SERVERS"))

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

	// 指标曝光。默认关闭（METRICS_TOKEN 未设置即 404）——/metrics 会泄露
	// 路由表、流量规模与错误率，不该像 /healthz 那样人人可读。
	httpx.BindMetricsPool(pool)
	r.Get("/metrics", httpx.MetricsHandler(os.Getenv("METRICS_TOKEN")))

	// 存活探针：进程还在、HTTP 还能响应。**刻意不查库**——
	// 库抖一下就重启应用进程是错的处置，那只会让恢复更慢。
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok", "engine": "go"})
	})
	// 就绪探针：能不能接流量。这条必须查库——
	// 原先只有 /healthz 且恒回 200，于是一个连不上数据库的网关会被
	// 编排器判定为健康、继续往它身上打流量，每个请求都 500。
	// 存活与就绪是两件事，探针也得是两个。
	r.Get("/readyz", func(w http.ResponseWriter, rq *http.Request) {
		c, cancel := context.WithTimeout(rq.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(c); err != nil {
			httpx.Err(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "数据库不可用")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ready", "engine": "go"})
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
		p.Get("/api/v1/orders/pool-counts", orderH.PoolCounts)
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
		// POST 是「按需生成」。此前只有 GET，前端写好的 genContract 打过来是 405。
		p.Post("/api/v1/waybills/{no}/contract", waybillH.ContractGenerate)
		p.Get("/api/v1/waybills/{no}/reminders", waybillH.Reminders)
		// 发提醒。此前只有 GET，页面上那颗「发送提醒」按钮恒定 405。
		p.Post("/api/v1/waybills/{no}/reminders", waybillH.SendReminder)
		p.Get("/api/v1/waybills/{no}/tracking", telH.WaybillTracking)
		p.Post("/api/v1/tracking/points", telH.TrackingIngest)
		p.Post("/api/v1/telematics/ingest", telH.Ingest)
		p.Get("/api/v1/telematics/vehicles/live", telH.Live)
		p.Get("/api/v1/telematics/waybills/{no}/trajectory", telH.Trajectory)
		p.Get("/api/v1/telematics/command-center/summary", telH.CommandCenterSummary)
		p.Get("/api/v1/waybills/{no}", waybillH.Detail)
		// PATCH 只开放回单状态一个字段（运单列表的批量「标记已回收」打这里）。
		p.Patch("/api/v1/waybills/{no}", waybillH.Patch)
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
	//
	// 从 blob.Store 读，而不是 http.FileServer(http.Dir(...))：
	// 接了对象存储之后文件不在本地盘上。读也走网关（不 302 到预签名 URL），
	// 因为媒体里有身份证、行驶证、签收回单——让它们只经过一个出口，
	// 将来要加鉴权、加水印、加访问审计都只有一个地方要改。
	//
	// 换掉 FileServer 就意味着**它顺手提供的两样保护也没了**，得自己补：
	//   · 路径穿越——现在由 blob.Local.abs 拒绝带 ".." 的 key（有用例钉着）
	//   · 目录列表——Store 对目录 key 返回 ErrNotFound
	// 这类"本来由框架白送"的保障，是换实现时最容易悄悄丢掉的东西。
	//
	// nosniff 是必须的——这些是用户上传物，绝不能让浏览器按内容猜出可执行类型。
	r.Handle("/media/*", http.HandlerFunc(
		func(w http.ResponseWriter, rq *http.Request) {
			key := strings.TrimPrefix(rq.URL.Path, "/media/")
			if key == "" || strings.HasSuffix(key, "/") {
				httpx.Err(w, http.StatusNotFound, "not_found", "未找到。")
				return
			}
			rc, info, err := store.Get(rq.Context(), key)
			if err != nil {
				if !errors.Is(err, blob.ErrNotFound) {
					// 底层错误只进日志。它可能带 bucket 名、内网地址，
					// 不能原样回给请求方。
					slog.Error("媒体读取失败", "key", key, "err", err)
				}
				httpx.Err(w, http.StatusNotFound, "not_found", "未找到。")
				return
			}
			defer rc.Close()
			// nosniff 只挡住"浏览器自己猜类型"，挡不住**声明出来就是 text/html**。
			//
			// 三条上传路径都用 http.DetectContentType 按内容嗅探类型：
			// 传一个 .html 上去，存下来的类型就是 text/html，
			// 再从 /media/ 原样吐出来——**在应用自己的域名下执行脚本**。
			// 实测传 `<script>alert(document.domain)</script>`，
			// GET 回来是 200 + Content-Type: text/html，内容原样。
			//
			// 最短的攻击路径是司机端：/api/v1/driver/credentials 是**自助上传**，
			// 只要司机令牌（手机号 + 身份证后 6 位）就能传。司机种一个 HTML 当"证件"，
			// 客服在后台点开那条链接，脚本就带着客服的会话在同源里跑起来了。
			//
			// 所以按"这个类型能不能安全地内联"来发：能的照常（图片和 PDF 要在
			// 界面上直接显示，回单照片、证件都是这么看的），不能的一律降成
			// 八位字节流并强制下载。已经存进去的文件也一起被这条挡住，
			// 不用去翻历史数据。
			ct, dispo := info.ContentType, ""
			if !inlineSafeMedia[strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))] {
				ct, dispo = "application/octet-stream", "attachment"
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Type", ct)
			if dispo != "" {
				// 文件名是 UUID+扩展名（上传时就不收用户给的名字），
				// 这里不拼用户输入，没有头注入的面。
				w.Header().Set("Content-Disposition", dispo)
			}
			if info.Size >= 0 {
				w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
			}
			if info.ETag != "" {
				w.Header().Set("ETag", `"`+info.ETag+`"`)
			}
			if _, err := io.Copy(w, rc); err != nil {
				// 响应头已经发出去了，这里只能记一笔——客户端会看到截断的响应
				slog.Warn("媒体传输中断", "key", key, "err", err)
			}
		}))

	// 未知路由：DRF 同款 404 信封
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		httpx.Err(w, http.StatusNotFound, "not_found", "未找到。")
	})

	return r
}
