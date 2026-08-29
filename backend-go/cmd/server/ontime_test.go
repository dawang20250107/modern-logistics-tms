package main

// 准班率不能把「没送到的货」算成准点交付。
//
// 由来（实测，不是读代码推的）：
//
// 运单状态机里 departed / in_transit 没有通往 cancelled / voided 的边——
// 实测 POST transition 回 409。车发出去之后要作废这一单，系统允许的唯一路径是
//   departed → in_transit → arrived → rejected → cancelled
// 也就是必须先记一次**没有发生过的到达**，而 arrived 会写 arrived_at 里程碑。
//
// 承运商评分卡的准班率按 arrived_at IS NOT NULL 取样，状态上只排除了 voided、
// 没排除 cancelled。于是那张被取消的运单既进分母又进分子：
// 实测取消前 分母=0 分子=0，走完上面那条路径后 分母=1 分子=1，
// 该承运商准班率 100%——一单根本没送到的货，成了一次准点交付。
//
// analytics 里的 ops.on_time_rate 本来就是对的（按 status IN (…) 取样），
// 是评分卡这三处跑偏了。同一个承运商在看板和评分卡上给出两个准班率，
// 比给错一个更伤信任。
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

// mkCarrierWithWaybill 造一个承运商 + 一张运单，运单状态与 arrived_at 由调用方给。
// 返回承运商 id。
func (e *testEnv) mkCarrierWithWaybill(status string, arrivedOnTime bool) string {
	e.t.Helper()
	ctx := context.Background()
	cid := uuid.NewString()
	code := "OT" + uuid.NewString()[:8]
	// 列按库里的真实非空列写。第一版是照着印象写的（cooperation_level /
	// service_type / qualification_status 都不存在），直接 42703。
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO md_carrier (id, created_at, updated_at, is_deleted, code, name,
		  contact_name, contact_phone, settlement_type, is_active, billing_day,
		  blacklist_reason, blacklisted, business_license_no, credit_days, credit_limit,
		  grade, carrier_type, city, service_area, tax_no, transport_license_no)
		VALUES ($1::uuid, now(), now(), false, $2, $2, '', '', 'monthly', true, 1,
		  '', false, '', 0, 0, 'A', 'company', '', '', '', '')`, cid, code); err != nil {
		e.t.Fatalf("造承运商失败：%v", err)
	}
	e.t.Cleanup(func() {
		ctx := context.Background()
		_, _ = e.pool.Exec(ctx, `DELETE FROM ops_waybill WHERE carrier_id=$1::uuid`, cid)
		_, _ = e.pool.Exec(ctx, `DELETE FROM md_carrier WHERE id=$1::uuid`, cid)
	})

	// planned_arrival 在未来，arrived_at 取 planned 之前/之后，用来控制"准不准点"
	arrivedExpr := "planned_arrival + interval '3 hours'"
	if arrivedOnTime {
		arrivedExpr = "planned_arrival - interval '1 hour'"
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, route_name, origin,
		  destination, status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
		  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type, ai_conversation_id,
		  cod_amount, cod_status, freight_payer, freight_term, platform_name, platform_order_no,
		  carrier_id, planned_arrival, arrived_at)
		SELECT gen_random_uuid(), now(), now(), $2, '甲 → 乙', '甲', '乙', $3,
		  'assigned', 'low', 'pending', 0, 1, 1, 1, 'outsource', '', 0, 'none',
		  'consignor', 'prepaid', '', '', $1::uuid, pa, `+arrivedExpr+`
		FROM (SELECT now() + interval '5 hours' AS planned_arrival, now() + interval '5 hours' AS pa) t`,
		cid, "OTWB"+uuid.NewString()[:8], status); err != nil {
		e.t.Fatalf("造运单失败：%v", err)
	}
	return cid
}

// perf 读承运商评分卡里的准班率
func (e *testEnv) perf(token, carrierID string) (rate float64, deals int) {
	e.t.Helper()
	rec := e.call(token, "GET", "/api/v1/carriers/"+carrierID+"/performance", "")
	if rec.Code != http.StatusOK {
		e.t.Fatalf("performance → %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			OnTimeRate float64 `json:"on_time_rate"`
			Deals      int     `json:"deals"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析失败：%v  体：%s", err, rec.Body.String())
	}
	return out.Data.OnTimeRate, out.Data.Deals
}

// TestOnTimeRateIgnoresCancelledWaybills 被取消的运单即便带着 arrived_at，
// 也不能进准班率的任何一侧。
//
// 无样本时评分卡回基线 0.85（对齐 Django 的 _BASELINE），
// 所以"被正确忽略"的表现就是仍然等于基线，而不是变成 1.0。
func TestOnTimeRateIgnoresCancelledWaybills(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)

	// 一张「发车后被取消」的运单：arrived_at 是那条唯一路径逼出来的假到达，
	// 而且时间上还"准点"——正是最能污染指标的形态。
	cid := e.mkCarrierWithWaybill("cancelled", true)
	rate, deals := e.perf(tok, cid)
	if rate == 1.0 {
		t.Errorf("准班率 100%%：一张 cancelled 的运单被算成了准点交付。"+
			"（deals=%d）车发出后取消，系统唯一允许的路径会写一个假 arrived_at，"+
			"取样时必须按状态排除掉。", deals)
	}
	if rate != 0.85 {
		t.Errorf("准班率 %.4f，期望基线 0.85（该承运商没有任何真实送达样本）", rate)
	}
}

// TestOnTimeRateCountsDeliveredWaybills 反向用例：真的送达了的单必须算进去。
//
// 少了这一条，上一条用「永远返回基线」也能过——那等于把指标关掉。
func TestOnTimeRateCountsDeliveredWaybills(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)

	for _, c := range []struct {
		name   string
		status string
		onTime bool
		want   float64
	}{
		{"按时送达 → 100%", "signed", true, 1.0},
		{"迟到 → 0%", "delivered", false, 0.0},
	} {
		t.Run(c.name, func(t *testing.T) {
			cid := e.mkCarrierWithWaybill(c.status, c.onTime)
			rate, deals := e.perf(tok, cid)
			if rate != c.want {
				t.Errorf("准班率 %.4f，期望 %.4f（deals=%d，status=%s）",
					rate, c.want, deals, c.status)
			}
		})
	}
}

// TestOnTimeRateMixedSample 混合样本：一真一假，结果必须只反映真的那张。
func TestOnTimeRateMixedSample(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	ctx := context.Background()

	cid := e.mkCarrierWithWaybill("signed", true) // 真·准点
	// 往同一个承运商再挂一张「发车后取消」的假到达，同样是准点形态
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, route_name, origin,
		  destination, status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
		  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type, ai_conversation_id,
		  cod_amount, cod_status, freight_payer, freight_term, platform_name, platform_order_no,
		  carrier_id, planned_arrival, arrived_at)
		VALUES (gen_random_uuid(), now(), now(), $2, '甲 → 乙', '甲', '乙', 'cancelled',
		  'assigned', 'low', 'pending', 0, 1, 1, 1, 'outsource', '', 0, 'none',
		  'consignor', 'prepaid', '', '', $1::uuid,
		  -- 迟到 3 小时。这一点是有讲究的：如果这张脏样本本身"准点"，
		  -- 那把它算进来 2/2 仍是 1.0，用例照样绿——只有让它迟到，
		  -- 混进来才会把比率拉到 0.5，对称地去掉分子分母的状态门也能被抓到。
		  now() + interval '5 hours', now() + interval '8 hours')`,
		cid, "OTWB"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("造第二张运单失败：%v", err)
	}

	rate, deals := e.perf(tok, cid)
	if rate != 1.0 {
		t.Errorf("准班率 %.4f，期望 1.0", rate)
	}
	// deals 数的是全部非作废运单，取消的那张仍然计入"合作单量"——
	// 这是有意的：取消也是这个承运商的一次真实合作记录。
	// 但它不该进准班率。两个口径不同，是因为它们回答的问题不同。
	if deals < 2 {
		t.Errorf("deals=%d，期望 ≥2（取消的单仍应计入合作单量）", deals)
	}
	_ = fmt.Sprint()
}

// TestOnTimeRateConsistentAcrossEndpoints 同一个承运商，三个出口给的准班率必须一致。
//
// 这条比单看某一个出口更要紧：碰上问题的这套 SQL 在仓库里有**三份拷贝**——
//
//	· masterdata/actions.go      → GET /carriers/{id}/performance
//	· masterdata/writecfg.go     → GET /carriers/{id} 详情里内嵌的 performance
//	· orders/suggestion.go       → 派单建议里的承运商评分
//
// 三份都只排除了 voided、都没排除 cancelled，所以是同一个错抄了三遍。
// 修的时候也必然要改三处，而只要漏一处，就会出现「详情页说 100%、
// 评分卡说 85%」这种自相矛盾——用户不会知道该信哪个。
//
// 所以这里不去分别断言每个出口的数值，而是断言**它们彼此相等**：
// 这样将来谁再改其中一处而漏掉另一处，这条就会红。
func TestOnTimeRateConsistentAcrossEndpoints(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)

	// 一真（准点送达）+ 一假（发车后取消留下的假到达）
	cid := e.mkCarrierWithWaybill("signed", true)
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, route_name, origin,
		  destination, status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
		  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type, ai_conversation_id,
		  cod_amount, cod_status, freight_payer, freight_term, platform_name, platform_order_no,
		  carrier_id, planned_arrival, arrived_at)
		VALUES (gen_random_uuid(), now(), now(), $2, '甲 → 乙', '甲', '乙', 'cancelled',
		  'assigned', 'low', 'pending', 0, 1, 1, 1, 'outsource', '', 0, 'none',
		  'consignor', 'prepaid', '', '', $1::uuid,
		  -- 迟到 3 小时。这一点是有讲究的：如果这张脏样本本身"准点"，
		  -- 那把它算进来 2/2 仍是 1.0，用例照样绿——只有让它迟到，
		  -- 混进来才会把比率拉到 0.5，对称地去掉分子分母的状态门也能被抓到。
		  now() + interval '5 hours', now() + interval '8 hours')`,
		cid, "OTWB"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("造运单失败：%v", err)
	}

	fromPerf, _ := e.perf(tok, cid)

	// 详情页内嵌的那份
	rec := e.call(tok, "GET", "/api/v1/carriers/"+cid, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("承运商详情 → %d：%s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Data struct {
			Performance struct {
				OnTimeRate float64 `json:"on_time_rate"`
			} `json:"performance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("解析详情失败：%v", err)
	}
	fromDetail := detail.Data.Performance.OnTimeRate

	if fromPerf != fromDetail {
		t.Errorf("同一个承运商，两个出口的准班率不一致：评分卡 %.4f / 详情页 %.4f。"+
			"这套 SQL 在仓库里有三份拷贝，多半是改了一处漏了另一处。",
			fromPerf, fromDetail)
	}
	if fromPerf != 1.0 {
		t.Errorf("准班率 %.4f，期望 1.0（一真一假两张单，假的那张必须被排除）", fromPerf)
	}
}

// TestOnTimeRateInDispatchSuggestion 第三份拷贝：派单建议里的承运商评分。
//
// 这套准班率 SQL 在仓库里有三份：
//
//	· masterdata/actions.go   → GET /carriers/{id}/performance
//	· masterdata/writecfg.go  → GET /carriers/{id} 详情内嵌
//	· orders/suggestion.go    → GET /orders/{id}/dispatch-suggestion   ← 这条覆盖它
//
// 前两份由上面几条用例守着；这一份要造订单才够得着，所以单独一条。
//
// 一开始我想省事，用"扫源码断言每处准班率旁边都有状态门"来一次盖住三份。
// 试了三个写法都不成立：按文件查太松（同一文件里回单及时率的状态门
// 替准班率背了书），只往前看会误报（analytics 的状态门写在 WHERE 里、
// 在判别式下面），前后都看又回到太松。
// 每一版我都拿变异验过，三版全都在"该红的时候是绿的"或"该绿的时候是红的"。
// 抓不住真问题的检查比没有更坏——它给假的安心，真报的时候也没人信。
// 所以那条删了，改成老老实实把第三个出口也用行为测试盖上。
func TestOnTimeRateInDispatchSuggestion(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	ctx := context.Background()

	// 一个承运商 + 一张真·准点送达 + 一张「发车后取消」留下的假到达
	cid := e.mkCarrierWithWaybill("signed", true)
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, route_name, origin,
		  destination, status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
		  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type, ai_conversation_id,
		  cod_amount, cod_status, freight_payer, freight_term, platform_name, platform_order_no,
		  carrier_id, planned_arrival, arrived_at)
		VALUES (gen_random_uuid(), now(), now(), $2, '甲 → 乙', '甲', '乙', 'cancelled',
		  'assigned', 'low', 'pending', 0, 1, 1, 1, 'outsource', '', 0, 'none',
		  'consignor', 'prepaid', '', '', $1::uuid,
		  -- 迟到 3 小时。这一点是有讲究的：如果这张脏样本本身"准点"，
		  -- 那把它算进来 2/2 仍是 1.0，用例照样绿——只有让它迟到，
		  -- 混进来才会把比率拉到 0.5，对称地去掉分子分母的状态门也能被抓到。
		  now() + interval '5 hours', now() + interval '8 hours')`,
		cid, "OTWB"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("造运单失败：%v", err)
	}

	tag, _ := e.seedOrders(1)
	var orderID string
	if err := e.pool.QueryRow(ctx,
		`SELECT id::text FROM ops_order WHERE order_no LIKE $1`, tag+"%").Scan(&orderID); err != nil {
		t.Fatalf("取订单失败：%v", err)
	}

	rec := e.call(tok, "GET", "/api/v1/orders/"+orderID+"/dispatch-suggestion", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("dispatch-suggestion → %d：%s", rec.Code, truncate(rec.Body.String(), 200))
	}
	var out struct {
		Data struct {
			CarrierRecommendations []struct {
				// 键是 carrier_id，不是 id。第一版写成 id，于是永远匹配不上，
				// 这条用例每次都走 Skip——一个永远跳过的用例等于没有。
				CarrierID  string  `json:"carrier_id"`
				OnTimeRate float64 `json:"on_time_rate"`
			} `json:"carrier_recommendations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}

	var found bool
	for _, c := range out.Data.CarrierRecommendations {
		if c.CarrierID != cid {
			continue
		}
		found = true
		if c.OnTimeRate != 1.0 {
			t.Errorf("派单建议里该承运商准班率 %.4f，期望 1.0 —— "+
				"一真一假两张单，被取消的那张必须排除在取样之外", c.OnTimeRate)
		}
	}
	if !found {
		t.Skipf("派单建议没有推荐到这个承运商（可能被线路/运力条件筛掉），本次未覆盖到")
	}
}
