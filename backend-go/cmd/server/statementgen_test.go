package main

// 生成对账单：对账中心那颗「生成」按钮。
//
// 用例发的 body **逐字段照抄前端真正发的那一组**（见 ReconciliationPage 的
// generate mutation）。这一条是这么来的：前端发的是
//   {..., period_start, period_end, due_date: null, external_total: 0}
// 而后端结构体上写的是 `json:"start"` / `json:"end"`，
// 并且 ExternalTotal 是 string——传 JSON 数字 0 进去，整个 Decode 直接失败。
//
// 结果是**点「生成」恒定 400「请求体不是合法 JSON」**：对账中心
// 从第一步就走不下去，而报错文案对财务人员毫无意义。
//
// 后端要求带账期这件事本身是对的（PERIOD_REQUIRED），
// 坏的是两边字段名对不上——不校验的话会更糟：账期被忽略、
// 一张对账单把所有历史费用全捞进来。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func (e *testEnv) mkCustomer() string {
	e.t.Helper()
	id := uuid.NewString()
	code := "STC" + id[:8]
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO md_customer (id, created_at, updated_at, is_deleted, code, name,
		  contact_name, contact_phone, settlement_type, is_active, wechat_group,
		  billing_day, credit_days, credit_limit, category, level)
		VALUES ($1::uuid, now(), now(), false, $2, '对账用例客户', '', '', 'monthly', true, '',
		        1, 30, 0, 'enterprise', 'A')`, id, code); err != nil {
		// 不能 Skip：列名对不上时 Skip 会让这一组用例悄悄不跑，
		// 而"悄悄不跑"正是这一轮反复吃亏的那件事。
		e.t.Fatalf("造客户失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM md_customer WHERE id=$1::uuid`, id)
	})
	return id
}

// mkBillableCustomer 造一个"这个账期里确实有账可对"的客户。
//
// 原先这组用例直接拿空客户去生成对账单——归集不到任何费用，
// 后端照样建了一张金额 0、明细 0 的单，用例还判它成功。
// 于是每跑一轮就往库里留 4 张空对账单（清理语句写的是 counterparty_ref，
// 而表上根本没有这一列，`_, _ =` 又把错误吞了，删除从来没生效过）。
// 演示库里攒到 235 张才被资金不变量用例逮住。
func (e *testEnv) mkBillableCustomer(start, end string) string {
	e.t.Helper()
	cust := e.mkCustomer()
	ctx := context.Background()
	wbNo := e.mkWaybillAt("arrived")
	if _, err := e.pool.Exec(ctx,
		`UPDATE ops_waybill SET customer_id=$2::uuid WHERE waybill_no=$1`, wbNo, cust); err != nil {
		e.t.Fatalf("把运单挂到客户上失败：%v", err)
	}
	// occurred_at 必须落在账期内，否则归集条件筛不到，又变成空单。
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO fin_expense_record (id, created_at, updated_at, waybill_id, direction,
		  expense_item_code, amount, currency, risk_status, source_system, external_id,
		  payee_type, payee_ref, remark, price_source, quote_id, pricing_rule_id,
		  pricing_rule_name, charge_method, matched_condition,
		  input_snapshot, calculation_detail, rule_snapshot, occurred_at)
		SELECT gen_random_uuid(), now(), now(), id, 'receivable', 'TRANSPORT_FEE', 1000, 'CNY',
		  'normal', 'test', '', 'customer', '', '', '', '', '', '', '', '', '{}', '{}', '{}',
		  ($2::date + interval '1 day')
		FROM ops_waybill WHERE waybill_no=$1`, wbNo, start); err != nil {
		e.t.Fatalf("造应收失败：%v", err)
	}
	_ = end
	e.t.Cleanup(func() { e.dropStatementsOf(cust) })
	return cust
}

// dropStatementsOf 把这个对手方名下的对账单连明细一起删干净。
//
// 删除**不能**用 `_, _ =` 吞错误：列名写错时删除从来没生效，
// 而用例照样绿——库里的垃圾就是这么攒起来的。
func (e *testEnv) dropStatementsOf(custID string) {
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM fin_statement_payment WHERE statement_id IN
		   (SELECT id FROM fin_statement WHERE counterparty_id=$1::text)`,
		`DELETE FROM fin_statement_line WHERE statement_id IN
		   (SELECT id FROM fin_statement WHERE counterparty_id=$1::text)`,
		`DELETE FROM fin_statement WHERE counterparty_id=$1::text`,
		`DELETE FROM fin_expense_record WHERE source_system='test' AND waybill_id IN
		   (SELECT id FROM ops_waybill WHERE customer_id=$1::uuid)`,
	} {
		if _, err := e.pool.Exec(ctx, q, custID); err != nil {
			e.t.Errorf("清理对账单失败，垃圾留在库里了：%v", err)
		}
	}
}

// TestGenerateEmptyStatementIsRefused 归集不到任何费用时不该建单。
//
// 一张有单号、有账期、金额 0、明细 0 的草稿对账单会出现在客户的对账中心里。
// 财务选错账期点一下，就得去解释并作废一张正式编号的单据——
// 而对账单号是连号的，那个号就这么烧掉了。
func TestGenerateEmptyStatementIsRefused(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	cust := e.mkCustomer()
	t.Cleanup(func() { e.dropStatementsOf(cust) })

	before := e.statementCount()
	rec := e.call(token, "POST", "/api/v1/finance/statements/generate",
		fmt.Sprintf(`{"direction":"receivable","counterparty_type":"customer",
			"counterparty_id":"%s","period_start":"2026-08-01","period_end":"2026-08-31",
			"due_date":null,"external_total":0}`, cust))
	if rec.Code != http.StatusConflict {
		t.Errorf("空账期生成对账单返回 %d，应该是 409：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "STATEMENT_EMPTY" {
		t.Errorf("报错码是 %q，期望 STATEMENT_EMPTY", out.Error.Code)
	}
	if after := e.statementCount(); after != before {
		t.Errorf("被拒绝的生成仍然往库里留了 %d 张对账单", after-before)
	}
}

func (e *testEnv) statementCount() int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM fin_statement`).Scan(&n); err != nil {
		e.t.Fatalf("数对账单失败：%v", err)
	}
	return n
}

// TestGenerateStatementAcceptsFrontendPayload 前端那一组字段必须能被后端收下。
func TestGenerateStatementAcceptsFrontendPayload(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	cust := e.mkBillableCustomer("2026-08-01", "2026-08-31")

	// 逐字段照抄 ReconciliationPage：period_start/period_end、due_date 为 null、
	// external_total 是数字 0（用户没填时前端就是这么发的）
	body := fmt.Sprintf(`{"direction":"receivable","counterparty_type":"customer",
		"counterparty_id":"%s","period_start":"2026-08-01","period_end":"2026-08-31",
		"due_date":null,"external_total":0}`, cust)

	rec := e.call(token, "POST", "/api/v1/finance/statements/generate", body)
	if rec.Code >= 400 {
		t.Fatalf("按前端真正发的那组字段生成对账单，返回 %d：%s\n"+
			"  对账中心那颗「生成」按钮点下去就是这个结果——从第一步就走不下去。",
			rec.Code, rec.Body.String())
	}
}

// TestGenerateStatementStillRequiresPeriod 账期仍然必填。
//
// 修字段名对不上的时候最容易顺手把校验也放宽了。账期不能省：
// 少了它，一张对账单会把该对手方**所有历史费用**一次性捞进来。
func TestGenerateStatementStillRequiresPeriod(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	cust := e.mkCustomer()

	rec := e.call(token, "POST", "/api/v1/finance/statements/generate",
		fmt.Sprintf(`{"direction":"receivable","counterparty_type":"customer","counterparty_id":"%s"}`, cust))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("不带账期应 400，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "PERIOD_REQUIRED" {
		t.Errorf("报错码是 %q，期望 PERIOD_REQUIRED", out.Error.Code)
	}
}

// TestGenerateStatementExternalTotalAcceptsBothShapes
// external_total 数字和字符串两种写法都要收下。
//
// JSON 里金额写成数字是最自然的，而结构体上是 string——
// 一个数字就让整个请求体解不开，报错还是"请求体不是合法 JSON"，
// 排查方向完全被带偏。
func TestGenerateStatementExternalTotalAcceptsBothShapes(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)

	for _, shape := range []string{`0`, `"0"`, `1234.56`, `"1234.56"`} {
		cust := e.mkBillableCustomer("2026-08-01", "2026-08-31")
		body := fmt.Sprintf(`{"direction":"receivable","counterparty_type":"customer",
			"counterparty_id":"%s","period_start":"2026-08-01","period_end":"2026-08-31",
			"external_total":%s}`, cust, shape)
		rec := e.call(token, "POST", "/api/v1/finance/statements/generate", body)
		if rec.Code >= 400 {
			t.Errorf("external_total=%s 时返回 %d：%s", shape, rec.Code, rec.Body.String())
		}
	}
}
