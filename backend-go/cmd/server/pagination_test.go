package main

// 列表分页：先分页后关联，结果必须和先关联后分页**逐行一致**。
//
// 由来：5 万单压测时发现订单列表深翻页要 212ms——查询是一句到底，
// 关联全做完（含两个按行 LATERAL：聚合异常、聚合运单号）最后才 LIMIT/OFFSET，
// 于是 LATERAL 为「翻过去要丢掉的那 5000 行」也各跑了一遍。
// 改成先在主表上定出这一页的 20 个 id，再拿这 20 个 id 去关联，
// 同样的查询 15ms。
//
// 但这是个**改写查询形状**的优化，风险在于悄悄改了返回内容：
// 少了几行、顺序变了、或者 LATERAL 派生的字段（运单号数组、异常计数）
// 变成空的——前两个走查时看得出来，第三个看不出来，
// 因为大部分订单本来就没有异常，字段是 0 很正常。
//
// 所以这里造有异常、有运单的订单，逐页断言。
//
// 需要真实 Postgres；没有 DATABASE_URL 就跳过。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// seedOrders 造 n 条可控顺序的订单：created_at 依次递减，
// 于是默认排序（created_at DESC）下的顺序就是 idx 0,1,2,…，可直接断言。
// 第 0 条挂 2 条未关闭异常（1 高 1 低）和 1 条运单，用来验 LATERAL。
func (e *testEnv) seedOrders(n int) (tag string, wantFirstNo string) {
	e.t.Helper()
	ctx := context.Background()
	tag = "PGT" + uuid.NewString()[:8]
	for i := 0; i < n; i++ {
		oid := uuid.NewString()
		no := fmt.Sprintf("%s-%04d", tag, i)
		if i == 0 {
			wantFirstNo = no
		}
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO ops_order (id, created_at, updated_at, order_no, source, status, remark,
			  cargo_desc, cargo_quantity, cargo_volume_cbm, cargo_weight_ton, channel,
			  contact_name, contact_phone, destination, origin, parse_meta, raw_text,
			  business_type, cargo_value, delivery_address, delivery_contact_name,
			  delivery_contact_phone, is_deleted, is_hazardous, package_type, pickup_address,
			  pickup_contact_name, pickup_contact_phone, priority, quoted_amount, settlement_type,
			  source_type, temperature_range, sla_status, approval_remark, approval_status,
			  ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term)
			VALUES ($1::uuid, now() - ($2 || ' minutes')::interval, now(), $3, 'cs', 'pooled', '',
			  '测试货', 1, 1, 1, 'cs', '张三', '13800000000', '上海', '杭州', '{}'::jsonb, '',
			  'ftl', 0, '收货地址', '李四', '13900000000', false, false, '纸箱', '发货地址',
			  '王五', '13700000000', 'normal', 0, 'monthly', 'enterprise', '', 'pending',
			  '', 'none', '', 0, 'none', 'consignor', 'prepaid')`,
			oid, fmt.Sprint(i), no); err != nil {
			e.t.Fatalf("造订单 %s 失败：%v", no, err)
		}
		e.t.Cleanup(func() {
			ctx := context.Background()
			_, _ = e.pool.Exec(ctx, `DELETE FROM ops_exception WHERE order_id=$1::uuid`, oid)
			_, _ = e.pool.Exec(ctx, `DELETE FROM ops_waybill WHERE order_id=$1::uuid`, oid)
			_, _ = e.pool.Exec(ctx, `DELETE FROM ops_order WHERE id=$1::uuid`, oid)
		})

		if i != 0 {
			continue
		}
		// 只给第一条挂 LATERAL 要聚合的东西：两条未关闭异常 + 一条运单。
		// 级别一高一低，才能验出 exception_level 取的是**最高**级别而不是随便一条。
		for _, lv := range []string{"high", "low"} {
			if _, err := e.pool.Exec(ctx, `
				INSERT INTO ops_exception (id, created_at, updated_at, exception_type, description,
				  status, responsibility_party, amount, level, resolution, source, order_id)
				VALUES ($1::uuid, now(), now(), 'cargo_damage', '分页测试', 'open', '', 0, $2, '', 'manual', $3::uuid)`,
				uuid.NewString(), lv, oid); err != nil {
				e.t.Fatalf("造异常失败：%v", err)
			}
		}
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, route_name, origin,
			  destination, status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
			  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type, ai_conversation_id,
			  cod_amount, cod_status, freight_payer, freight_term, platform_name, platform_order_no, order_id)
			VALUES ($1::uuid, now(), now(), $2, '杭州 → 上海', '杭州', '上海', 'dispatched',
			  'assigned', 'low', 'pending', 0, 1, 1, 1, 'outsource', '', 0, 'none',
			  'consignor', 'prepaid', '', '', $3::uuid)`,
			uuid.NewString(), tag+"-WB", oid); err != nil {
			e.t.Fatalf("造运单失败：%v", err)
		}
	}
	return tag, wantFirstNo
}

type pageResp struct {
	Data struct {
		Items []struct {
			OrderNo    string   `json:"order_no"`
			WaybillNos []string `json:"waybill_nos"`
			ExcCount   int      `json:"exception_count"`
			ExcLevel   string   `json:"exception_level"`
		} `json:"items"`
		Total int `json:"total"`
	} `json:"data"`
}

func (e *testEnv) page(token, path string) pageResp {
	e.t.Helper()
	rec := e.call(token, "GET", path, "")
	if rec.Code != http.StatusOK {
		e.t.Fatalf("%s → %d：%s", path, rec.Code, rec.Body.String())
	}
	var out pageResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析 %s 响应失败：%v", path, err)
	}
	return out
}

// TestPaginationOrderAndCompleteness 翻完所有页，应当不重不漏且顺序正确。
//
// 「不重不漏」是分页改写最容易破的一条：内层排序键不唯一时，
// 两页之间会重复或者丢行——而且只在数据量刚好跨页时才看得出来。
// 排序键末尾必须带 o.id 兜底，这个用例就是钉住它的。
func TestPaginationOrderAndCompleteness(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	const n = 25
	tag, _ := e.seedOrders(n)

	seen := map[string]int{}
	var order []string
	for page := 1; page <= 3; page++ {
		got := e.page(tok, fmt.Sprintf(
			"/api/v1/orders?page=%d&page_size=10&ordering=-created_at&search=%s", page, tag))
		for _, it := range got.Data.Items {
			seen[it.OrderNo]++
			order = append(order, it.OrderNo)
		}
	}
	if len(seen) != n {
		t.Fatalf("翻三页只见到 %d 条，应为 %d（漏行或重复）", len(seen), n)
	}
	for no, c := range seen {
		if c != 1 {
			t.Errorf("%s 出现了 %d 次，跨页重复", no, c)
		}
	}
	// created_at 依次递减 → -created_at 排序下就是造的顺序
	for i, no := range order {
		if want := fmt.Sprintf("%s-%04d", tag, i); no != want {
			t.Errorf("第 %d 条是 %s，应为 %s（顺序没保住）", i, no, want)
		}
	}
}

// TestPaginationKeepsLateralFields LATERAL 派生的字段不能因为改写查询而丢。
//
// 这是最阴的一种回归：运单号数组变成空、异常计数恒为 0，
// 页面照样渲染、接口照样 200，只是「有没有异常」这一列永远是干净的。
func TestPaginationKeepsLateralFields(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	tag, firstNo := e.seedOrders(3)

	got := e.page(tok, "/api/v1/orders?page=1&page_size=10&ordering=-created_at&search="+tag)
	if len(got.Data.Items) == 0 {
		t.Fatal("一条都没查到")
	}
	head := got.Data.Items[0]
	if head.OrderNo != firstNo {
		t.Fatalf("第一条是 %s，应为 %s", head.OrderNo, firstNo)
	}
	if head.ExcCount != 2 {
		t.Errorf("exception_count=%d，应为 2（LATERAL 聚合掉了）", head.ExcCount)
	}
	if head.ExcLevel != "high" {
		t.Errorf("exception_level=%q，应为 high（要取最高级别，不是任意一条）", head.ExcLevel)
	}
	if len(head.WaybillNos) != 1 || head.WaybillNos[0] != tag+"-WB" {
		t.Errorf("waybill_nos=%v，应为 [%s-WB]", head.WaybillNos, tag)
	}
	// 没挂东西的那些，字段要是干净的零值，不能被第一条的值串到
	for _, it := range got.Data.Items[1:] {
		if it.ExcCount != 0 || it.ExcLevel != "" || len(it.WaybillNos) != 0 {
			t.Errorf("%s 不该有异常/运单，实得 count=%d level=%q nos=%v",
				it.OrderNo, it.ExcCount, it.ExcLevel, it.WaybillNos)
		}
	}
}

// TestPaginationDeepOffset 深翻页要和逐页走到同一位置的结果一致。
//
// 深翻页正是这次优化的动机（212ms → 15ms），所以更要钉住它的正确性：
// 快了但翻错页，比慢着翻对页糟糕得多。
func TestPaginationDeepOffset(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	const n = 30
	tag, _ := e.seedOrders(n)

	base := "/api/v1/orders?ordering=-created_at&search=" + tag
	last := e.page(tok, fmt.Sprintf("%s&page=3&page_size=10", base))
	if len(last.Data.Items) != 10 {
		t.Fatalf("第 3 页取到 %d 条，应为 10", len(last.Data.Items))
	}
	// 同样的位置换一种取法：一页 30 条取全量，切最后 10 条
	all := e.page(tok, fmt.Sprintf("%s&page=1&page_size=30", base))
	if len(all.Data.Items) != n {
		t.Fatalf("全量取到 %d 条，应为 %d", len(all.Data.Items), n)
	}
	for i := range last.Data.Items {
		got, want := last.Data.Items[i].OrderNo, all.Data.Items[20+i].OrderNo
		if got != want {
			t.Errorf("深翻页第 %d 条是 %s，逐页走到同一位置是 %s", i, got, want)
		}
	}
	if last.Data.Total != all.Data.Total {
		t.Errorf("两种取法的 total 不一致：%d vs %d", last.Data.Total, all.Data.Total)
	}
}

// TestPoolCountsMatchListTotals 计数端点报的数，必须等于列表端点能翻到的数。
//
// 由来：调度工作台顶部的「待派 / 紧急」和三个池的角标，原先是前端拿
// items.length 数出来的——数的是**取回来的那一页**。演示库十几单时
// 一页装得下全部，数出来正好对；5 万单时订单池 8336 条、前端只拿 20 条，
// 于是「待分配 20」其实是 8336，「紧急 40」其实是两千多。
//
// 这种错不报错、不变形，只是安静地把全量说成一页。而调度员正是照着
// 这几个数决定今天先派哪一批。所以这里钉死：计数 == 列表 total。
//
// 顺带钉住 urgent=1 的筛选口径：计数里的「紧急」和列表筛出来的「紧急」
// 必须是同一个定义，否则会出现「说有 7 条紧急，点进去列出 3 条」。
func TestPoolCountsMatchListTotals(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)

	var counts struct {
		Data struct {
			Unassigned   int `json:"unassigned"`
			Dispatchable int `json:"dispatchable"`
			Dispatched   int `json:"dispatched"`
			Pending      int `json:"pending"`
			Urgent       int `json:"urgent"`
		} `json:"data"`
	}
	rec := e.call(tok, "GET", "/api/v1/orders/pool-counts?scope=all", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pool-counts → %d：%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}

	// page_size=1：这里要比的是 total，不是取回来几条。
	// 如果计数端点坏成了「数当前页」，这一条就会把它抓出来。
	for _, c := range []struct {
		name string
		path string
		want int
	}{
		{"待分配", "/api/v1/orders/pool?scope=free&page_size=1", counts.Data.Unassigned},
		{"可调派", "/api/v1/orders/pool?scope=all&page_size=1", counts.Data.Dispatchable},
		{"已调派", "/api/v1/orders/dispatched?scope=all&page_size=1", counts.Data.Dispatched},
	} {
		got := e.page(tok, c.path)
		if got.Data.Total != c.want {
			t.Errorf("%s：计数端点说 %d，列表 total 是 %d", c.name, c.want, got.Data.Total)
		}
		if c.want > 1 && len(got.Data.Items) != 1 {
			t.Errorf("%s：page_size=1 却回了 %d 条", c.name, len(got.Data.Items))
		}
	}

	// pending 必须是两池的**并集**，不是相加。
	//
	// 超管选「全部」时，可调派池就是整个订单池，把待分配池整个包含在内；
	// 相加等于每张单数两遍（实测 8336 + 8336 = 16672，而池里只有 8336 张）。
	// 前端原来就是这么加的，演示库里 12 + 12 = 24 一样是错的，
	// 只是没人会去核对那个数——所以这条断言的意义正是"替人去核对"。
	poolTotal := e.page(tok, "/api/v1/orders/pool?scope=all&page_size=1").Data.Total
	if counts.Data.Pending != poolTotal {
		t.Errorf("pending=%d，应等于整池 %d（超管看全部时两池重叠，不能相加）",
			counts.Data.Pending, poolTotal)
	}
	if poolTotal > 0 && counts.Data.Pending == counts.Data.Unassigned+counts.Data.Dispatchable &&
		counts.Data.Unassigned > 0 && counts.Data.Dispatchable > 0 &&
		counts.Data.Unassigned+counts.Data.Dispatchable != poolTotal {
		t.Errorf("pending 看起来是两池相加（%d + %d），而不是取并集",
			counts.Data.Unassigned, counts.Data.Dispatchable)
	}

	// 计数里的「紧急」和列表按 urgent=1 筛出来的，必须是同一个定义。
	// 同样按整池比，不是两池相加——第一版这里就是加出来的，
	// 于是断言 1389 != 2778 自己把自己抓了出来。
	urgentPool := e.page(tok, "/api/v1/orders/pool?scope=all&urgent=1&page_size=1")
	if urgentPool.Data.Total != counts.Data.Urgent {
		t.Errorf("紧急：计数端点说 %d，列表按 urgent=1 筛出 %d —— 两处口径不一致",
			counts.Data.Urgent, urgentPool.Data.Total)
	}
	// urgent=1 必须真的收窄，否则上面那条是在比两个「全量」，等于没测
	if poolTotal > 0 && urgentPool.Data.Total == poolTotal {
		t.Logf("提示：urgent=1 没有收窄（%d == %d），"+
			"可能这批数据恰好全是紧急单，也可能筛选没生效",
			urgentPool.Data.Total, poolTotal)
	}
}

// TestPoolSearchIsServerSide 检索必须在库里做，不能只在当前页里做。
//
// 前端原先是 rows.filter(...)——搜的是已经取回来的 20 条。
// 想搜第 500 条上的单号，永远搜不到，而且界面看起来完全正常：
// 它会理直气壮地告诉你"没有匹配的订单"。
func TestPoolSearchIsServerSide(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	const n = 25
	tag, _ := e.seedOrders(n)

	// 造的单都是 pooled、未锁定未分派 → 落在「待分配」池
	// 精确搜最后一条：它在默认排序下排在第 25 位，第一页（20 条）里没有它。
	last := fmt.Sprintf("%s-%04d", tag, n-1)
	got := e.page(tok, "/api/v1/orders/pool?scope=free&page_size=20&search="+last)
	if got.Data.Total != 1 {
		t.Fatalf("搜 %s 得到 total=%d，应为 1 —— 检索没走到库里，"+
			"只在当前页里找（那条单在第 25 位，第一页看不见）", last, got.Data.Total)
	}
	if len(got.Data.Items) != 1 || got.Data.Items[0].OrderNo != last {
		t.Errorf("搜 %s 返回 %+v", last, got.Data.Items)
	}
}
