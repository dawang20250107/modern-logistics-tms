package main

// 计价规则这条链：调度在「计价规则」页存了一条阶梯价，报价到底按不按它算。
//
// 为什么要单独跑一遍：finance 里 17 条计价用例都是**直接构造 Rule 结构体**
// 去验算法（阶梯边界、最低收费、体积重取大者），验的是"算得对不对"。
// 但页面上那条规则要经过另一段路才会变成算法的输入：
//
//   POST /api/v1/finance/pricing-rules   ← 页面存规则（走通用 CRUD 引擎）
//   POST /api/v1/orders/quote            ← 报价时 MatchRule 去把它捞回来
//
// 写进去的列和捞回来的条件对不上，算法用例照样全绿，而现实中
// **页面上存的价格永远不生效，报价一直是 0（"未匹配到规则"）**。
// 钱的路径上，这种"存了但不生效"比算错更难发现：没有报错，只是数字不对，
// 而看到数字的人默认它就是系统算出来的。
//
// 这条用例只走 HTTP：存一条阶梯价 → 拿一个落在第二档的重量去报价 →
// 金额必须等于那一档的单价 × 重量。

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestPricingRuleSavedInUITakesEffectInQuote(t *testing.T) {
	e := newTestEnv(t)
	admin := e.mkUser(true)

	// ── 建一个客户（规则要挂在它身上，报价也按它匹配）──
	cid, _ := uuid.NewV7()
	code := "PRT" + cid.String()[:8]
	if _, err := e.pool.Exec(t.Context(), `
		INSERT INTO md_customer (id, created_at, updated_at, is_deleted, code, name,
		  contact_name, contact_phone, settlement_type, is_active, wechat_group,
		  billing_day, credit_days, credit_limit, category, level)
		VALUES ($1::uuid, now(), now(), false, $2, $3, '', '', 'monthly', true, '',
		        1, 30, 0, 'normal', 'A')`,
		cid.String(), code, "计价回环用例客户"); err != nil {
		t.Fatalf("建客户失败：%v（表结构变了就跟着改，别改成跳过）", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := e.pool.Exec(ctx, `DELETE FROM md_customer WHERE id=$1::uuid`, cid.String()); err != nil {
			t.Logf("清理客户失败：%v", err)
		}
	})

	// ── 先报价一次留个底 ──
	// 这一步不能省。库里通常已经有通配的演示规则（priority=10），
	// 少了这个基线，后面那个金额可能来自一条和本次完全无关的规则，
	// 用例会因为别人的规则而变绿。基线记下"现在匹配的是谁、算出多少"，
	// 存完规则之后要求匹配到的**换成本用例这一条**。
	quoteBody := `{"customer":"` + cid.String() + `","cargo_weight_ton":8,"cargo_volume_cbm":0,"cargo_quantity":1}`
	rec := e.call(admin, "POST", "/api/v1/orders/quote", quoteBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("报价接口返回 %d：%s", rec.Code, rec.Body.String())
	}
	var q0 struct {
		Data struct {
			Amount   float64 `json:"amount"`
			Matched  bool    `json:"matched"`
			RuleName string  `json:"rule_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &q0); err != nil {
		t.Fatalf("报价响应解不开：%v\n%s", err, rec.Body.String())
	}
	t.Logf("基线：matched=%v rule=%q amount=%v", q0.Data.Matched, q0.Data.RuleName, q0.Data.Amount)
	const ruleName = "计价回环用例-阶梯价"
	if q0.Data.RuleName == ruleName {
		t.Fatalf("建规则之前就匹配到了同名规则 %q——上一轮的残留没清干净，"+
			"这一轮验不出东西", ruleName)
	}

	// ── 页面上存一条阶梯价（字段与 PricingPage.tsx 的 payload() 逐个对齐）──
	// 阶梯：0~5 吨 260/吨，5~20 吨 220/吨。8 吨落在第二档。
	//
	// 键名必须是 min_ton / max_ton / price。第一版我写成了 min / max，
	// 结果 decFrom 取不到值、min 落成 0、max 落成默认的 999999，
	// **第一档匹配了全部重量**，报价 260×8=2080 而不是 220×8=1760。
	// 用例红了，但红的原因是我自己的样本写错了字段名——产品是对的。
	// 这也正说明这条链值得钉：这三个键名在前后端之间没有任何编译期约束，
	// 写错了不报错，只是价算错。
	// 科目用 TRANSPORT_INCOME。第一版这里写的是 "FREIGHT"——从前端表单里抄来的，
	// 而 FREIGHT 不在任何一份费用科目词表里。加上枚举校验之后这条用例立刻 400，
	// 这正说明那个写死值一直在往库里塞不存在的科目。
	rulePayload := `{
		"name":"` + ruleName + `",
		"price_type":"income",
		"charge_method":"tiered_weight",
		"expense_item_code":"TRANSPORT_INCOME",
		"customer":"` + cid.String() + `",
		"carrier":null,
		"route_name":"",
		"base_price":0,
		"unit_price":0,
		"min_charge_qty":0,
		"min_price":0,
		"tier_prices":[{"min_ton":0,"max_ton":5,"price":260},{"min_ton":5,"max_ton":20,"price":220}],
		"volumetric_factor":0.3333,
		"fuel_surcharge_pct":0,
		"priority":100,
		"is_active":true
	}`
	rec = e.call(admin, "POST", "/api/v1/finance/pricing-rules", rulePayload)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("存计价规则返回 %d：%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct{ ID string } `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Data.ID == "" {
		t.Fatalf("存规则的响应里没有 id：%v\n%s", err, rec.Body.String())
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := e.pool.Exec(ctx, `DELETE FROM fin_pricing_rule WHERE id=$1::uuid`, created.Data.ID); err != nil {
			t.Logf("清理计价规则失败：%v", err)
		}
	})

	// ── 同一个报价请求：必须按刚存的那条规则算 ──
	rec = e.call(admin, "POST", "/api/v1/orders/quote", quoteBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("报价接口返回 %d：%s", rec.Code, rec.Body.String())
	}
	var q1 struct {
		Data struct {
			Amount           float64 `json:"amount"`
			Matched          bool    `json:"matched"`
			RuleName         string  `json:"rule_name"`
			ChargeableWeight float64 `json:"chargeable_weight"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &q1); err != nil {
		t.Fatalf("报价响应解不开：%v\n%s", err, rec.Body.String())
	}
	if !q1.Data.Matched {
		t.Fatalf("页面上存的计价规则没有被报价匹配到：\n"+
			"  规则存进去了（201 + id），报价却说「未匹配到规则」、金额 %v。\n"+
			"  也就是说客户在页面上配好的合同价永远不生效，报价一直是 0。\n"+
			"  查 PricingRuleWrite 写进去的列，和 finance.MatchRule 的 WHERE 条件对不对得上。\n"+
			"  响应：%s", q1.Data.Amount, rec.Body.String())
	}
	// 8 吨落在 5~20 那一档：220 × 8 = 1760
	const want = 1760.0
	if math.Abs(q1.Data.Amount-want) > 0.01 {
		t.Fatalf("报价金额 %v，期望 %v（8 吨落在 5~20 档，220 元/吨）：\n"+
			"  规则匹配上了但算出来的钱不对——tier_prices 存进去的形状"+
			"和算法读的形状可能不一致。\n  响应：%s", q1.Data.Amount, want, rec.Body.String())
	}
	// 这一条才是本用例的核心断言：匹配到的必须是**刚在页面上存的那条**。
	// 只看金额是不够的——别的规则碰巧算出同一个数，用例就绿得没有道理。
	if q1.Data.RuleName != ruleName {
		t.Fatalf("报价匹配到的是「%s」，不是刚存的「%s」：\n"+
			"  页面上存了一条 priority=100 的客户专属价，报价却还在用原来那条。\n"+
			"  客户在页面上改价改了个寂寞——存成功了，报价不认。\n  响应：%s",
			q1.Data.RuleName, ruleName, rec.Body.String())
	}
}
