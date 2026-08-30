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
		        1, 30, 0, 'direct', 'A')`, id, code); err != nil {
		// 不能 Skip：列名对不上时 Skip 会让这一组用例悄悄不跑，
		// 而"悄悄不跑"正是这一轮反复吃亏的那件事。
		e.t.Fatalf("造客户失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM md_customer WHERE id=$1::uuid`, id)
	})
	return id
}

// TestGenerateStatementAcceptsFrontendPayload 前端那一组字段必须能被后端收下。
func TestGenerateStatementAcceptsFrontendPayload(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	cust := e.mkCustomer()

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
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `
			DELETE FROM fin_statement_line WHERE statement_id IN
			  (SELECT id FROM fin_statement WHERE counterparty_ref=$1::text)`, cust)
		_, _ = e.pool.Exec(context.Background(),
			`DELETE FROM fin_statement WHERE counterparty_ref=$1::text`, cust)
	})
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
		cust := e.mkCustomer()
		body := fmt.Sprintf(`{"direction":"receivable","counterparty_type":"customer",
			"counterparty_id":"%s","period_start":"2026-08-01","period_end":"2026-08-31",
			"external_total":%s}`, cust, shape)
		rec := e.call(token, "POST", "/api/v1/finance/statements/generate", body)
		if rec.Code >= 400 {
			t.Errorf("external_total=%s 时返回 %d：%s", shape, rec.Code, rec.Body.String())
		}
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `
			DELETE FROM fin_statement_line WHERE statement_id IN
			  (SELECT id FROM fin_statement WHERE remark LIKE '%对账用例%')`)
	})
}
