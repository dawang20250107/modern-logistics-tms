package main

// 鉴权矩阵：每条路由对「登录了但没有对应权限点」的账号应该回什么。
//
// 这份用例的来历是一次手工探针。当时拿一个刚 /auth/register 出来的账号
// （is_superuser=false、permissions=[]、role_names=[]、organization_id=null）
// 挨个打接口，发现财务域整个没挂闸：statement-overview 回 200 且带着
// net_position 129,413.33，settle 回的是 404 而不是 403——404 意味着请求
// 压根没被鉴权拦住，只是那个 UUID 不存在，换成真实单号就能把别人的钱标记成已收。
//
// 那次是靠人眼发现的。**没有任何自动化能发现「新加了一条路由但忘了挂闸」**，
// 这份文件就是补上那个自动化：新增受保护端点必须在这里登记期望码，
// 忘了挂闸的话，CI 会在合并前告诉你，而不是等哪天有人手痒去 curl。
//
// 需要一个真实的 Postgres（CI 里有 service container）。没有 DATABASE_URL
// 就跳过——本地不带库跑 go test 时不该红。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/config"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/migrate"
)

// testEnv 一个装好 schema 的库 + 组装好的路由 + 一个可控权限的测试账号
type testEnv struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router http.Handler
	issuer *auth.TokenIssuer
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("未设置 DATABASE_URL，跳过 HTTP 层鉴权测试")
	}
	// SecretKey 必须在 config.Load 之前就位：签发与校验共用它
	if os.Getenv("DJANGO_SECRET_KEY") == "" {
		t.Setenv("DJANGO_SECRET_KEY", "test-insecure-secret-min-32-bytes-long!!")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连库失败：%v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("库 ping 不通，跳过：%v", err)
	}
	if err := migrate.Run(ctx, pool); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	// 每个用例各建一个池，用完必须关。
	//
	// 原先不关也没事，因为池的 MinConns 是 0，空闲池几乎不占连接。
	// 后来给生产配上 DB_MIN_CONNS=4（4 条连接曾是整个网关的吞吐天花板），
	// 每个池就常驻 4 条了——30 个用例 × 4 = 120 条，而 max_connections 是 100，
	// 于是后面的用例连不上库。
	// 症状很有迷惑性：失败的是"建账号"这种和连接数毫无关系的地方，
	// 而且哪个用例先挂取决于执行顺序。
	//
	// 注册在这里（最早）意味着它最后执行：t.Cleanup 是后进先出，
	// 后面 mkUser 注册的删数据要先跑完，再关池。反过来的话删不掉。
	// 后台常驻任务跟着用例一起收。不收的话，buildRouter 每被调用一次
	// 就漏 6 个永不退出的 goroutine 继续打库，用例之间会互相干扰，
	// 池关掉之后还会刷一屏 "closed pool"。
	// 顺序要紧：先停任务，再关池——反过来任务会拿着已关闭的池报错。
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	t.Cleanup(func() {
		stopWorkers()
		pool.Close()
	})

	cfg := config.Load()
	if err := auth.EnsurePermissions(ctx, pool); err != nil {
		t.Fatalf("权限目录失败：%v", err)
	}
	return &testEnv{
		t: t, pool: pool,
		router: buildRouter(ctx, workerCtx, pool, cfg),
		issuer: auth.NewIssuer(cfg.SecretKey, cfg.AccessMinutes, cfg.RefreshDays),
	}
}

// mkUser 建一个测试账号并授予指定权限点；返回 access token。
// perms 为空 = 只登录、什么权限点都没有（就是当初那个探针账号）。
func (e *testEnv) mkUser(superuser bool, perms ...string) string {
	e.t.Helper()
	ctx := context.Background()
	uid, _ := uuid.NewV7()
	// uuid v7 的前几位是时间戳前缀，同毫秒内建多个账号会撞唯一索引；用全串
	username := "authz_test_" + uid.String()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO accounts_user (id, password, last_login, is_superuser, username, first_name, last_name,
		  email, is_staff, is_active, date_joined, phone, nickname, organization_id, avatar, preferences)
		VALUES ($1::uuid, '!', NULL, $2, $3, '', '', '', false, true, now(), '', '', NULL, NULL, '{}'::jsonb)`,
		uid.String(), superuser, username); err != nil {
		e.t.Fatalf("建账号失败：%v", err)
	}
	e.t.Cleanup(func() {
		ctx := context.Background()
		_, _ = e.pool.Exec(ctx, `DELETE FROM iam_role_assignment WHERE user_id=$1::uuid`, uid.String())
		_, _ = e.pool.Exec(ctx, `DELETE FROM iam_login_attempt WHERE user_id=$1::uuid`, uid.String())
		_, _ = e.pool.Exec(ctx, `DELETE FROM iam_token_denylist WHERE user_id=$1::uuid`, uid.String())
		_, _ = e.pool.Exec(ctx, `DELETE FROM accounts_user WHERE id=$1::uuid`, uid.String())
	})

	if len(perms) > 0 {
		rid, _ := uuid.NewV7()
		roleCode := "authz_test_role_" + rid.String()
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO iam_role (id, created_at, updated_at, code, name, data_scope, is_active)
			VALUES ($1::uuid, now(), now(), $2, $2, 'all', true)`, rid.String(), roleCode); err != nil {
			e.t.Fatalf("建角色失败：%v", err)
		}
		e.t.Cleanup(func() {
			// 依赖顺序：分配 → 角色权限 → 角色。少了前两条，角色删不掉（外键挡着），
			// 而错误在这里是被忽略的，于是库里会一轮一轮攒下 authz_test_role_*。
			// 测试留垃圾比测试失败更坏：它让人开始不信任这套用例。
			ctx := context.Background()
			_, _ = e.pool.Exec(ctx, `DELETE FROM iam_role_assignment WHERE role_id=$1::uuid`, rid.String())
			_, _ = e.pool.Exec(ctx, `DELETE FROM iam_role_permissions WHERE role_id=$1::uuid`, rid.String())
			_, _ = e.pool.Exec(ctx, `DELETE FROM iam_role WHERE id=$1::uuid`, rid.String())
		})
		for _, p := range perms {
			if _, err := e.pool.Exec(ctx, `
				INSERT INTO iam_role_permissions (role_id, permission_id)
				SELECT $1::uuid, id FROM iam_permission WHERE code = $2
				ON CONFLICT DO NOTHING`, rid.String(), p); err != nil {
				e.t.Fatalf("授权限点 %s 失败：%v", p, err)
			}
		}
		aid, _ := uuid.NewV7()
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO iam_role_assignment (id, created_at, updated_at, user_id, role_id)
			VALUES ($1::uuid, now(), now(), $2::uuid, $3::uuid)`,
			aid.String(), uid.String(), rid.String()); err != nil {
			e.t.Fatalf("分配角色失败：%v", err)
		}
	}

	access, _, err := e.issuer.IssuePair(uid.String())
	if err != nil {
		e.t.Fatalf("签发失败：%v", err)
	}
	return access
}

func (e *testEnv) call(token, method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// ── 受保护端点清单 ──────────────────────────────────────────
//
// want 是「登录了、但没有 perm 这个权限点」时期望的状态码。
// 一律是 403：401 表示没登录，404 表示"资源不存在"——后者尤其危险，
// 它意味着请求已经穿过鉴权走到了查库那一步（财务那个洞就长这样）。
type endpoint struct {
	method, path, perm string
	body               string
}

var protectedEndpoints = []endpoint{
	// 财务：这一组是整份文件的由来
	{"GET", "/api/v1/finance/statement-overview", "finance.view", ""},
	{"GET", "/api/v1/finance/aging", "finance.view", ""},
	{"GET", "/api/v1/finance/statements", "finance.view", ""},
	{"GET", "/api/v1/finance/dashboard-metrics", "finance.view", ""},
	{"GET", "/api/v1/finance/statements/" + zeroUUID, "finance.view", ""},
	{"POST", "/api/v1/finance/statements/" + zeroUUID + "/settle", "finance.manage", `{"amount":"1.00"}`},
	{"POST", "/api/v1/finance/statements/" + zeroUUID + "/confirm", "finance.manage", ""},
	{"POST", "/api/v1/finance/statements/" + zeroUUID + "/audit", "finance.manage", ""},
	{"GET", "/api/v1/finance/statements/" + zeroUUID + "/payments", "finance.view", ""},
	{"POST", "/api/v1/finance/statements/generate", "finance.manage",
		`{"direction":"receivable","counterparty_type":"customer","counterparty_id":"` + zeroUUID + `","start":"2026-01-01","end":"2026-12-31"}`},
	// 运单
	{"GET", "/api/v1/waybills", "waybill.view", ""},
	{"GET", "/api/v1/waybills/stats", "waybill.view", ""},
	// 经营分析
	{"GET", "/api/v1/analytics/dashboard", "analytics.view", ""},
	{"GET", "/api/v1/analytics/metrics", "analytics.view", ""},
	// 主数据（走通用 CRUD 引擎的闸）
	{"GET", "/api/v1/carriers", "carrier.view", ""},
	{"GET", "/api/v1/vehicles", "masterdata.view", ""},
	{"GET", "/api/v1/drivers", "masterdata.view", ""},
	{"GET", "/api/v1/customers", "masterdata.view", ""},
	// 组织与权限
	{"GET", "/api/v1/org/rbac/matrix", "org.rbac", ""},

	// ── 通用 CRUD 资源：22 个读写配置里有 22 处没写全权限点 ──
	//
	// 这一批是发布前把 WriteCfg 逐个数出来的。gate 的语义是「want 为空 = 不设限」，
	// 于是漏写一行就等于对所有登录用户敞开。实测：一个只有
	// masterdata.view + waybill.view 的客服账号，**POST /finance/pricing-rules
	// 返回 201**——它建出一条 priority=9999、base_price=99999 的通配收入价，
	// 此后每一次报价与成本生成都会命中它。
	//
	// 这条链上还有一处更隐蔽的：异常的 assign/handle/close 三个动作
	// 一个权限检查都没有，挡住它们的只有数据范围。**数据范围管的是
	// "看得见谁的单"，不是"能不能做这件事"**——同一个网点的客服照样能替公司
	// 定责赔钱（close 会按赔付金额落一条应付）。
	//
	// 下面每一条都登记期望码，漏挂闸的话 CI 会在合并前说话。
	{"GET", "/api/v1/finance/pricing-rules", "finance.view", ""},
	{"POST", "/api/v1/finance/pricing-rules", "finance.manage",
		`{"name":"x","price_type":"income","charge_method":"flat","expense_item_code":"TRANSPORT_INCOME"}`},
	{"GET", "/api/v1/finance/expense-records", "finance.view", ""},
	{"POST", "/api/v1/finance/expense-records", "finance.manage", `{}`},
	{"GET", "/api/v1/finance/payment-requests", "finance.view", ""},
	{"POST", "/api/v1/finance/payment-requests", "finance.manage", `{}`},
	{"GET", "/api/v1/finance/expense-items", "finance.view", ""},
	{"POST", "/api/v1/finance/expense-items", "finance.manage", `{}`},
	{"GET", "/api/v1/finance/contracts", "finance.view", ""},
	{"POST", "/api/v1/finance/contracts", "finance.manage", `{}`},
	{"GET", "/api/v1/finance/reimbursements", "finance.view", ""},
	{"GET", "/api/v1/finance/webhooks", "org.manage", ""},
	{"POST", "/api/v1/finance/webhooks", "org.manage", `{}`},
	{"GET", "/api/v1/finance/webhook-deliveries", "org.manage", ""},
	{"GET", "/api/v1/receipts", "waybill.view", ""},
	{"POST", "/api/v1/receipts", "waybill.manage", `{}`},
	{"GET", "/api/v1/order-templates", "waybill.view", ""},
	{"POST", "/api/v1/order-templates", "waybill.manage", `{}`},
	{"GET", "/api/v1/reminder-templates", "waybill.view", ""},
	{"POST", "/api/v1/reminder-templates", "waybill.manage", `{}`},
	{"GET", "/api/v1/dispatch-batches", "waybill.view", ""},
	{"GET", "/api/v1/finance/projects", "masterdata.view", ""},
	{"POST", "/api/v1/finance/projects", "masterdata.manage", `{}`},
	{"GET", "/api/v1/telematics/alerts", "telematics.view", ""},
	{"GET", "/api/v1/org/login-audit", "org.rbac", ""},
	{"GET", "/api/v1/audit-logs", "org.rbac", ""},
	{"GET", "/api/v1/org/permissions", "org.rbac", ""},

	// 异常：登记的门低（waybill.view），定责赔钱的门高（waybill.manage）
	{"POST", "/api/v1/exceptions", "waybill.view", `{"exception_type":"other","level":"low","description":"x"}`},
	{"POST", "/api/v1/exceptions/" + zeroUUID + "/assign", "waybill.manage", `{}`},
	{"POST", "/api/v1/exceptions/" + zeroUUID + "/handle", "waybill.manage", `{}`},
	{"POST", "/api/v1/exceptions/" + zeroUUID + "/close", "waybill.manage", `{}`},
	{"GET", "/api/v1/exceptions/" + zeroUUID + "/timeline", "waybill.view", ""},
}

const zeroUUID = "00000000-0000-0000-0000-000000000000"

func TestProtectedEndpointsRejectUnprivileged(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(false) // 登录了，但什么权限点都没有

	for _, ep := range protectedEndpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			rec := e.call(token, ep.method, ep.path, ep.body)
			if rec.Code != http.StatusForbidden {
				t.Errorf("期望 403（无 %s 权限），实际 %d\n响应：%s\n"+
					"提示：404/409/500 说明请求已经穿过鉴权走到了业务逻辑——"+
					"这正是财务域当初漏挂闸的表征。",
					ep.perm, rec.Code, truncate(rec.Body.String(), 200))
			}
		})
	}
}

func TestProtectedEndpointsRejectAnonymous(t *testing.T) {
	e := newTestEnv(t)
	for _, ep := range protectedEndpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			rec := e.call("", ep.method, ep.path, ep.body)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("未带凭证时期望 401，实际 %d：%s", rec.Code, truncate(rec.Body.String(), 160))
			}
		})
	}
}

// 授予对应权限点之后必须放行 —— 否则「全都 403」也能让上面的用例全绿，
// 那种通过毫无意义。
func TestGrantedPermissionOpensEndpoint(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(false, "finance.view", "waybill.view", "analytics.view")
	for _, ep := range []endpoint{
		{"GET", "/api/v1/finance/statement-overview", "finance.view", ""},
		{"GET", "/api/v1/finance/aging", "finance.view", ""},
		{"GET", "/api/v1/finance/statements", "finance.view", ""},
		{"GET", "/api/v1/waybills", "waybill.view", ""},
		{"GET", "/api/v1/analytics/dashboard", "analytics.view", ""},
	} {
		t.Run(ep.path, func(t *testing.T) {
			rec := e.call(token, ep.method, ep.path, ep.body)
			if rec.Code != http.StatusOK {
				t.Errorf("授予 %s 后期望 200，实际 %d：%s", ep.perm, rec.Code, truncate(rec.Body.String(), 200))
			}
		})
	}
}

// 权限点规范目录必须覆盖代码里所有被校验的权限点。
// 库里曾经只有 3 行而代码校验 12 个，差额那些在权限矩阵界面上没有勾选框，
// 任何角色都无法被授予——功能对非超管永久 403，改配置也没用。
func TestPermissionCatalogCoversCheckedPermissions(t *testing.T) {
	e := newTestEnv(t)
	inCatalog := map[string]bool{}
	for _, p := range auth.Catalog {
		inCatalog[p.Code] = true
	}
	for _, ep := range protectedEndpoints {
		if !inCatalog[ep.perm] {
			t.Errorf("端点 %s 校验权限点 %q，但它不在 auth.Catalog 里——"+
				"没有目录行就没有勾选框，任何角色都授不了它", ep.path, ep.perm)
		}
	}

	// 目录也必须真的落了库
	rows, err := e.pool.Query(context.Background(), `SELECT code FROM iam_permission`)
	if err != nil {
		t.Fatalf("读权限表失败：%v", err)
	}
	defer rows.Close()
	inDB := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		inDB[c] = true
	}
	for _, p := range auth.Catalog {
		if !inDB[p.Code] {
			t.Errorf("权限点 %q 在目录里但没落库，EnsurePermissions 没生效", p.Code)
		}
	}
}

// 超管走 `*` 通配符，不该被任何权限点挡住
func TestSuperuserBypassesPermissionGates(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	for _, path := range []string{
		"/api/v1/finance/statement-overview",
		"/api/v1/finance/statements",
		"/api/v1/waybills",
		"/api/v1/analytics/dashboard",
	} {
		rec := e.call(token, "GET", path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("超管访问 %s 期望 200，实际 %d：%s", path, rec.Code, truncate(rec.Body.String(), 160))
		}
	}
}

// 自助注册默认关闭：它是一个任何人都能自助拿到的已认证身份，
// 而所有只判「登录了没有」的端点对它敞开。
func TestSelfRegistrationClosedByDefault(t *testing.T) {
	t.Setenv("TMS_ALLOW_SELF_REGISTRATION", "")
	e := newTestEnv(t)
	rec := e.call("", "POST", "/api/v1/auth/register",
		`{"username":"should_not_exist_`+uuid.NewString()[:8]+`","password":"Probe12345!"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("默认应关闭自助注册（期望 403），实际 %d：%s", rec.Code, truncate(rec.Body.String(), 160))
	}
	// /auth/methods 要如实告诉前端，好让登录页别显示一个必然 403 的入口
	rec = e.call("", "GET", "/api/v1/auth/methods", "")
	var env struct {
		Data struct {
			Registration struct {
				Enabled bool `json:"enabled"`
			} `json:"registration"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析 /auth/methods 失败：%v", err)
	}
	if env.Data.Registration.Enabled {
		t.Error("/auth/methods 说注册开着，但实际是关的——前端会显示一个点进去就 403 的入口")
	}
}

// 令牌吊销：轮换后旧 refresh 必须失效。
// 少了这一步，一张泄漏的 refresh 能无限换出新券，等于永久有效。
func TestRefreshRotationRevokesOldToken(t *testing.T) {
	e := newTestEnv(t)
	uid, _ := uuid.NewV7()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO accounts_user (id, password, last_login, is_superuser, username, first_name, last_name,
		  email, is_staff, is_active, date_joined, phone, nickname, organization_id, avatar, preferences)
		VALUES ($1::uuid, '!', NULL, false, $2, '', '', '', false, true, now(), '', '', NULL, NULL, '{}'::jsonb)`,
		uid.String(), "rot_test_"+uid.String()); err != nil {
		t.Fatalf("建账号失败：%v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM accounts_user WHERE id=$1::uuid`, uid.String())
	})

	_, refresh, err := e.issuer.IssuePair(uid.String())
	if err != nil {
		t.Fatal(err)
	}
	body := func(r string) string { return fmt.Sprintf(`{"refresh":%q}`, r) }

	rec := e.call("", "POST", "/api/v1/auth/token/refresh", body(refresh))
	if rec.Code != http.StatusOK {
		t.Fatalf("首次刷新期望 200，实际 %d：%s", rec.Code, truncate(rec.Body.String(), 160))
	}
	var env struct {
		Data struct{ Refresh string } `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}

	if rec := e.call("", "POST", "/api/v1/auth/token/refresh", body(refresh)); rec.Code != http.StatusUnauthorized {
		t.Errorf("旧 refresh 复用期望 401，实际 %d —— 轮换没有作废旧券，"+
			"泄漏一张就等于永久有效", rec.Code)
	}
	if rec := e.call("", "POST", "/api/v1/auth/token/refresh", body(env.Data.Refresh)); rec.Code != http.StatusOK {
		t.Errorf("新 refresh 期望 200，实际 %d", rec.Code)
	}
}

// 账号级水位线：改密 / 退出全部会话之后，既有 access 必须立刻失效
func TestRevokeAllInvalidatesExistingAccess(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(false)
	if rec := e.call(token, "GET", "/api/v1/auth/me", ""); rec.Code != http.StatusOK {
		t.Fatalf("吊销前 /me 期望 200，实际 %d", rec.Code)
	}
	// 水位线截断到秒，同一秒签发的券按设计放行（见 RevokeAllForUser 注释），
	// 所以这里必须跨过秒边界才测得到吊销本身
	time.Sleep(1100 * time.Millisecond)
	if rec := e.call(token, "POST", "/api/v1/auth/logout", `{"all":true}`); rec.Code != http.StatusOK {
		t.Fatalf("logout all 期望 200，实际 %d", rec.Code)
	}
	if rec := e.call(token, "GET", "/api/v1/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("退出全部会话后旧 access 期望 401，实际 %d —— "+
			"只作废 refresh 是不够的，access 还能再活满一个 TTL", rec.Code)
	}
}

// ── 只靠数据范围收窄的端点 ────────────────────────────────
//
// 订单域没有 `order.view` 这种权限点，设计上就是「登录即可访问，
// 但只看得见自己组织范围内的数据」。所以这几条对无组织账号应该是
// **200 但空**，而不是 403 —— 期望值不同，判法也不同：要看返回体。
//
// 这一组同样是实测发现的：/orders 列表收窄了，但换个聚合口径的
// /orders/funnel 把全库订单漏斗（总量/分状态/分渠道）原样放出去，
// /workbench 更进一步，pool_top 给的是完整订单记录而不只是个数字。
// 列表那道收窄，被同一批数据的另一个出口绕过去了。
func TestScopeOnlyEndpointsReturnNothingForOrglessUser(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(false)

	// 前置：库里得真有订单，否则"返回空"是因为没数据，测了个寂寞
	var total int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ops_order WHERE NOT is_deleted`).Scan(&total); err != nil {
		t.Fatalf("查订单总数失败：%v", err)
	}
	if total == 0 {
		t.Skip("库里没有订单，无法区分「收窄生效」与「本来就没数据」")
	}

	t.Run("orders/funnel 不泄漏全库聚合", func(t *testing.T) {
		rec := e.call(token, "GET", "/api/v1/orders/funnel", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d", rec.Code)
		}
		var env struct {
			Data struct {
				Total     int            `json:"total"`
				ByStatus  map[string]int `json:"by_status"`
				ByChannel map[string]int `json:"by_channel"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.Total != 0 || len(env.Data.ByStatus) != 0 || len(env.Data.ByChannel) != 0 {
			t.Errorf("无组织账号看到了 total=%d、%d 个状态、%d 个渠道；"+
				"库里共 %d 单——聚合口径绕过了 /orders 列表的数据范围",
				env.Data.Total, len(env.Data.ByStatus), len(env.Data.ByChannel), total)
		}
	})

	// pool-counts 是后加的聚合出口。加它的时候就该想到这一条：
	// 每开一个新的聚合口径，就等于给同一批数据开了一个新出口，
	// 而数据范围是按出口挂的，不是按数据挂的。
	t.Run("orders/pool-counts 不泄漏全库计数", func(t *testing.T) {
		rec := e.call(token, "GET", "/api/v1/orders/pool-counts", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d", rec.Code)
		}
		var env struct {
			Data map[string]int `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		for k, v := range env.Data {
			if v != 0 {
				t.Errorf("无组织账号在 %s 上看到 %d（库里共 %d 单）——"+
					"计数口径绕过了数据范围", k, v, total)
			}
		}
	})

	t.Run("workbench 不泄漏订单池", func(t *testing.T) {
		rec := e.call(token, "GET", "/api/v1/workbench", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d", rec.Code)
		}
		var env struct {
			Data struct {
				Dispatch struct {
					PoolCount int              `json:"pool_count"`
					PoolTop   []map[string]any `json:"pool_top"`
				} `json:"dispatch"`
				Finance struct {
					DraftStatements int `json:"draft_statements"`
				} `json:"finance"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.Dispatch.PoolCount != 0 || len(env.Data.Dispatch.PoolTop) != 0 {
			t.Errorf("无组织账号看到池中 %d 单、pool_top %d 条完整订单记录",
				env.Data.Dispatch.PoolCount, len(env.Data.Dispatch.PoolTop))
		}
		if env.Data.Finance.DraftStatements != 0 {
			t.Errorf("无 finance.view 的账号看到草稿对账单数 %d —— "+
				"那是财务域的数字，不该出现在通用工作台上", env.Data.Finance.DraftStatements)
		}
	})

	t.Run("waybills/stats 不泄漏全库统计", func(t *testing.T) {
		// 这条有 waybill.view 闸，无权限时应 403（不是 200 空）
		if rec := e.call(token, "GET", "/api/v1/waybills/stats", ""); rec.Code != http.StatusForbidden {
			t.Errorf("期望 403，实际 %d：%s", rec.Code, truncate(rec.Body.String(), 160))
		}
	})
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
