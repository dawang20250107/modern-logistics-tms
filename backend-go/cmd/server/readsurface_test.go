package main

// 读的那一面：每一条集合 GET 都必须挂权限闸。
//
// 写动作那 100 条已经逐条打过了（authz_test.go / authzcoverage_test.go）。
// 读这一面一直没系统扫过，而它才是数据泄漏的入口——一次请求带走的是整张表。
//
// 探针账号：角色只有 masterdata.view（"主数据查看"，听起来只是看客户和
// 司机档案），数据范围给"全部"。这是客服/调度岗上很常见的一种配法。
// 拿它把所有无路径参数的 GET 打了一遍，实测漏了三处：
//
//   GET /api/v1/orders          → 200，全库订单
//   GET /api/v1/orders/export   → 200，5.26 MB、50002 行 CSV，
//                                 客户名、始发目的、报价一次拉走
//   GET /api/v1/workbench       → pool_count=4588 + pool_top 五条完整订单
//   GET /api/v1/exceptions      → 异常记录，含责任方、赔付金额、处理结论
//
// 订单那 9 条读路由一条都没挂闸；工作台只收了数据范围没判权限点；
// 异常列表则是 main.go 里 `rt.Get("/", excH.List)` **覆盖掉**了
// 通用 CRUD 挂好的那条带闸路由——盖上去的这份忘了带闸。
//
// 数据范围挡不住这些：范围管的是"看得见谁的单"，给了"全部"就等于全库。
//
// 这条用例抓不到什么：只打无路径参数的集合路由。带 {id} 的详情要一个个
// 造真实 ID，成本高而泄漏面小（一次一条）。下面会把跳过的条数报出来，
// 免得"没扫到"被当成"没问题"。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// 允许 masterdata.view 账号读到内容的路由，每条都要写清为什么。
var readAllowed = map[string]string{
	"/api/v1/auth/me":                    "自己的账号信息",
	"/api/v1/auth/methods":               "登录方式，登录页要用，没有业务数据",
	"/api/v1/auth/login-history":         "只返回自己的登录记录",
	"/api/v1/notifications":              "只返回发给自己的通知",
	"/api/v1/notifications/unread-count": "同上，只数自己的",
	"/api/v1/customers/":                 "客户档案就是 masterdata.view 的本职",
	"/api/v1/drivers/":                   "司机档案，同上",
	"/api/v1/drivers/lookup":             "司机下拉选项，同上",
	"/api/v1/vehicles/":                  "车辆档案，同上",
	"/api/v1/driver-credentials/":        "司机证件档案，资源库那一页的内容，同上",
	"/api/v1/credentials/expiring":       "证件到期提醒，资源库那一页的内容",
	"/api/v1/routes/":                    "线路档案，同上",
	"/api/v1/b2b-partners/":              "B2B 伙伴档案，B2BWrite.ReadPerm 就是 masterdata.view",
	"/api/v1/finance/projects/":          "项目档案按主数据管（ProjectWrite.ReadPerm 是 masterdata.view），不是财务流水",
	"/api/v1/lookup":                     "全局搜索，落到各资源自己的范围与权限上",
	"/api/v1/org/route-resolve":          "按起止地名反查线路，输入即输出，不带库里的业务数据",
	"/api/v1/integrations/status":        "集成开关是否配置，不含凭据",
	"/api/v1/waybills/cost-catalog":      "费用科目词表，是常量表不是数据",
	"/api/v1/workbench":                  "按块分权：池中待派要 waybill.view、对账草稿要 finance.view，其余都是本人的",
}

func TestCollectionReadsAreGated(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(false, "masterdata.view")

	routes, ok := e.router.(chi.Routes)
	if !ok {
		t.Fatal("路由器不是 chi.Routes，这条用例没法走查")
	}
	var gets []string
	skipped := 0
	_ = chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != "GET" || !strings.HasPrefix(route, "/api/v1/") {
			return nil
		}
		if strings.Contains(route, "{") {
			skipped++
			return nil
		}
		gets = append(gets, strings.TrimSuffix(route, "/*"))
		return nil
	})
	t.Logf("扫了 %d 条无参数的 GET 路由，另有 %d 条带路径参数的没扫", len(gets), skipped)

	// 空转防护：chi 的 Walk 签名或路由前缀变了会一条都走不到，
	// 那时候"全都合规"和"根本没检查"长得一模一样。
	if len(gets) < 50 {
		t.Fatalf("只走到 %d 条 GET 路由 —— 路由走查多半失配了，这次结果不作数", len(gets))
	}

	seen := map[string]bool{}
	for _, p := range gets {
		seen[p] = true
		rec := e.call(tok, "GET", p, "")
		if rec.Code != http.StatusOK {
			continue
		}
		if why, okAllow := readAllowed[p]; okAllow {
			_ = why
			continue
		}
		// 200 且不在名单里：只有"确实一行都没返回"才放过。
		// 空表不算证据——表里没数据不等于闸挂上了，所以这里要求登记。
		t.Errorf("GET %s 对只有 masterdata.view 的账号返回 200 —— 要么补权限闸，"+
			"要么写进 readAllowed 并说明为什么这个账号该看得到。返回 %d 字节：%s",
			p, rec.Body.Len(), head(rec.Body.String(), 160))
	}

	for p := range readAllowed {
		if !seen[p] {
			t.Errorf("readAllowed 里登记的 %s 已经不是一条 GET 路由了，名单该清理", p)
		}
	}
	// 名单自检的另一半：**已经挡住了的条目，豁免就是过期的**。
	//
	// 过期条目比没有名单更坏：它让人以为"这里是有意放开的"，于是再没人去看。
	// 车载上报那两条就是被一句与事实不符的豁免理由藏了很久
	// （写着"走设备凭据"，而它们既没有设备凭据也不在设备侧路由组上）。
	for p := range readAllowed {
		if !seen[p] {
			continue
		}
		if e.call(tok, "GET", p, "").Code == http.StatusForbidden {
			t.Errorf("readAllowed 里登记的 %s 现在已经是 403 了，这条豁免过期了 —— 删掉它。\n"+
				"  名单一旦有一条不可信，整份名单就都不可信了。", p)
		}
	}
}

// TestOrderReadsRequireWaybillView 订单这一面的读要 waybill.view。
//
// 单独钉一条：上面那条是"面"的普查，这条是"点"的回归。
// 前端导航上早就写着 perm: "waybill.view"，后端这 9 条读路由却一条都没执行，
// 其中 /orders/export 一次能带走 5 MB、5 万行 CSV。
func TestOrderReadsRequireWaybillView(t *testing.T) {
	e := newTestEnv(t)
	low := e.mkUser(false, "masterdata.view")
	ok := e.mkUser(false, "waybill.view")

	paths := []string{
		"/api/v1/orders", "/api/v1/orders/export", "/api/v1/orders/funnel",
		"/api/v1/orders/pool", "/api/v1/orders/pool-counts", "/api/v1/orders/dispatched",
		"/api/v1/orders/dispatchers", "/api/v1/orders/customer-addresses",
	}
	for _, p := range paths {
		if code := e.call(low, "GET", p, "").Code; code != http.StatusForbidden {
			t.Errorf("只有 masterdata.view 的账号打 GET %s 拿到 %d，应该是 403", p, code)
		}
		if code := e.call(ok, "GET", p, "").Code; code == http.StatusForbidden {
			t.Errorf("有 waybill.view 的账号打 GET %s 反被 403 挡了 —— 闸挂错了权限点", p)
		}
	}
}

// TestWorkbenchPoolNeedsWaybillView 工作台的池中待派也要 waybill.view。
//
// 同一份数据换个入口就出来了：/orders 规规矩矩 403，工作台却把
// pool_count 和 pool_top（五条完整订单记录）一起给了出去。
func TestWorkbenchPoolNeedsWaybillView(t *testing.T) {
	e := newTestEnv(t)
	pool := func(token string) (int, int) {
		rec := e.call(token, "GET", "/api/v1/workbench", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("工作台返回 %d：%s", rec.Code, rec.Body.String())
		}
		var out struct {
			Data struct {
				Dispatch struct {
					PoolCount int               `json:"pool_count"`
					PoolTop   []json.RawMessage `json:"pool_top"`
				} `json:"dispatch"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析工作台失败：%v", err)
		}
		return out.Data.Dispatch.PoolCount, len(out.Data.Dispatch.PoolTop)
	}
	if c, n := pool(e.mkUser(false, "masterdata.view")); c != 0 || n != 0 {
		t.Errorf("只有 masterdata.view 的账号在工作台看到 pool_count=%d、pool_top %d 条 —— "+
			"它打 /orders 是 403 的，同一份数据不该换个入口就出来", c, n)
	}
	if c, n := pool(e.mkUser(false, "waybill.view")); c == 0 && n == 0 {
		t.Log("提示：有 waybill.view 的账号池中待派为 0（库里可能就没有 pooled 订单）")
	}
}

// TestExceptionListRequiresWaybillView 异常列表的闸不能被自实现路由盖掉。
func TestExceptionListRequiresWaybillView(t *testing.T) {
	e := newTestEnv(t)
	su := e.mkUser(true)
	e.mkException(su, e.mkWaybillRow(), "high")

	if code := e.call(e.mkUser(false, "masterdata.view"), "GET", "/api/v1/exceptions", "").Code; code != http.StatusForbidden {
		t.Errorf("只有 masterdata.view 的账号读异常列表拿到 %d，应该是 403 —— "+
			"异常记录里有责任方、赔付金额和处理结论", code)
	}
	if code := e.call(e.mkUser(false, "waybill.view"), "GET", "/api/v1/exceptions", "").Code; code != http.StatusOK {
		t.Errorf("有 waybill.view 的账号读异常列表拿到 %d，应该放行", code)
	}
}

func head(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// 带路径参数的读也要挂闸。
//
// 上面那条普查只打无参数的集合路由，把 64 条带 {id}/{no} 的跳过了——
// 而它们正是"知道单号就能看"的那一类。这条把那个盲区补上。
//
// 造得出真实 ID 的就用真实 ID；造不出的用一个不存在的 UUID —— 这照样管用，
// **因为挂了闸的路由会在查这个 ID 之前就 403**，没挂闸的才会走到查库、
// 返回 404。403 和 404 的区别在这里就是"有没有闸"。
//
// 实测漏的六条（订单集合读补上闸之后，子资源这一半还开着）：
//
//	/orders/{id}/workflow            各阶段与时间
//	/orders/{id}/timeline            全部流转记录
//	/orders/{id}/lineage             拆并单血缘
//	/orders/{id}/dispatch-suggestion 推荐承运商（3.4 KB）
//	/orders/{id}/ymm-quote           市场报价区间
//	/waybills/{no}/tracking          这台车的轨迹点
//
// 外加 /carriers/{id}/performance：承运商列表要 carrier.view，
// 这条绩效（成交量、异常率、常跑线路）却谁都能看。
var paramReadAllowed = map[string]string{
	"/api/v1/customers/{id}":              "客户档案就是 masterdata.view 的本职",
	"/api/v1/customers/{id}/context":      "同上，客户画像挂在客户档案下",
	"/api/v1/customers/{id}/lane-suggest": "同上",
	"/api/v1/drivers/{id}":                "司机档案，同上",
	"/api/v1/driver-credentials/{id}":     "司机证件档案，同上",
	"/api/v1/vehicles/{id}":               "车辆档案，同上",
	"/api/v1/routes/{id}":                 "线路档案，同上",
	"/api/v1/b2b-partners/{id}":           "B2B 伙伴档案，B2BWrite.ReadPerm 是 masterdata.view",
	"/api/v1/finance/projects/{id}":       "项目档案按主数据管，同 readAllowed 里那条",
}

func TestParamReadsAreGated(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	low := e.mkUser(false, "masterdata.view")
	su := e.mkUser(true)

	orderID := e.mkOrder()
	wbNo := e.mkWaybillAt("arrived")
	excID := e.mkException(su, e.mkWaybillRow(), "high")
	cust := e.mkCustomer()
	const nilUUID = "00000000-0000-0000-0000-000000000000"
	pick := func(prefix string) string {
		one := func(q string) string {
			var v string
			_ = e.pool.QueryRow(ctx, q).Scan(&v)
			if v == "" {
				return nilUUID
			}
			return v
		}
		switch {
		case strings.HasPrefix(prefix, "/api/v1/orders/"):
			return orderID
		case strings.HasPrefix(prefix, "/api/v1/exceptions/"):
			return excID
		case strings.HasPrefix(prefix, "/api/v1/customers/"):
			return cust
		case strings.HasPrefix(prefix, "/api/v1/carriers/"):
			return one(`SELECT id::text FROM md_carrier LIMIT 1`)
		case strings.HasPrefix(prefix, "/api/v1/drivers/"):
			return one(`SELECT id::text FROM md_driver LIMIT 1`)
		case strings.HasPrefix(prefix, "/api/v1/finance/statements/"):
			return one(`SELECT id::text FROM fin_statement LIMIT 1`)
		case strings.HasPrefix(prefix, "/api/v1/org/employees/"):
			return one(`SELECT id::text FROM accounts_user LIMIT 1`)
		}
		return nilUUID
	}

	routes, ok := e.router.(chi.Routes)
	if !ok {
		t.Fatal("路由器不是 chi.Routes")
	}
	walked, seen := 0, map[string]bool{}
	codes := map[string]int{} // 路由 → 返回码，名单自检要用
	_ = chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != "GET" || !strings.HasPrefix(route, "/api/v1/") || !strings.Contains(route, "{") {
			return nil
		}
		p := strings.ReplaceAll(route, "{no}", wbNo)
		p = strings.ReplaceAll(p, "{code}", "order.count")
		if i := strings.Index(p, "{"); i >= 0 {
			j := strings.Index(p[i:], "}")
			p = p[:i] + pick(route) + p[i+j+1:]
		}
		if strings.Contains(p, "{") { // 还有第二个参数填不上，报出来而不是悄悄跳过
			t.Errorf("路由 %s 有填不上的路径参数，这条没被检查到", route)
			return nil
		}
		walked++
		seen[route] = true
		rec := e.call(low, "GET", p, "")
		codes[route] = rec.Code
		if rec.Code == http.StatusForbidden {
			return nil
		}
		if why, okAllow := paramReadAllowed[route]; okAllow {
			_ = why
			return nil
		}
		t.Errorf("GET %s 对只有 masterdata.view 的账号返回 %d（不是 403）—— "+
			"要么补权限闸，要么写进 paramReadAllowed 并说明理由。返回：%s",
			route, rec.Code, head(rec.Body.String(), 140))
		return nil
	})
	t.Logf("带路径参数的 GET 路由走了 %d 条", walked)
	if walked < 30 {
		t.Fatalf("只走到 %d 条 —— 路由走查多半失配了，这次结果不作数", walked)
	}
	for route := range paramReadAllowed {
		if !seen[route] {
			t.Errorf("paramReadAllowed 里登记的 %s 已经不是一条 GET 路由了，名单该清理", route)
		}
	}
	// 同 readAllowed：已经 403 的条目，豁免是过期的
	for route := range paramReadAllowed {
		if code, ok := codes[route]; ok && code == http.StatusForbidden {
			t.Errorf("paramReadAllowed 里登记的 %s 现在已经是 403 了，这条豁免过期了 —— 删掉它", route)
		}
	}
}
