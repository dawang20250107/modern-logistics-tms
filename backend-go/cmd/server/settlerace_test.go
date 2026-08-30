package main

// 并发核销：两个人同时登记同一张对账单的收款。
//
// 核销直接改「已核销金额」，超额核销就是账实不符——多认了钱进来，
// 而应收敞口凭空少一块。现在的实现是对的（事务里 SELECT … FOR UPDATE
// 之后再判未结余额），这条用例把它钉住。
//
// 钉它的理由和抢单那条一样：这种守卫在重构里最容易丢，去掉之后一切照常，
// 只有在真并发下才出错，而那时是生产环境。报销审批那处就是反例——
// 状态检查读在事务外，靠一个不相干的唯一索引碰巧才没出事。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// mkDraftStatement 造一张可核销的对账单：生成 → 确认。
func (e *testEnv) mkDraftStatement(token string) (id string, total string) {
	e.t.Helper()
	cust := e.mkCustomer()
	// 造一条应收，好让对账单有内容
	wbNo := e.mkWaybillAt("arrived")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
		UPDATE ops_waybill SET customer_id=$2::uuid WHERE waybill_no=$1`, wbNo, cust); err != nil {
		e.t.Fatalf("挂客户失败：%v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO fin_expense_record (id, created_at, updated_at, waybill_id, direction,
		  expense_item_code, amount, currency, risk_status, source_system, external_id,
		  payee_type, payee_ref, remark, price_source, quote_id, pricing_rule_id,
		  pricing_rule_name, charge_method, matched_condition,
		  input_snapshot, calculation_detail, rule_snapshot, occurred_at)
		SELECT gen_random_uuid(), now(), now(), id, 'receivable', 'TRANSPORT_FEE', 1000, 'CNY',
		  'normal', 'test', '', 'customer', '', '', '', '', '', '', '', '', '{}', '{}', '{}',
		  -- occurred_at 必须有值：归集条件里写着 e.occurred_at IS NOT NULL，
		  -- 留空的费用永远不会进对账单（这是费用录入时该保证的，不是 bug）
		  now()
		FROM ops_waybill WHERE waybill_no=$1`, wbNo); err != nil {
		e.t.Fatalf("造应收失败：%v", err)
	}

	rec := e.call(token, "POST", "/api/v1/finance/statements/generate", fmt.Sprintf(
		`{"direction":"receivable","counterparty_type":"customer","counterparty_id":"%s",
		  "period_start":"2020-01-01","period_end":"2099-12-31","external_total":0}`, cust))
	if rec.Code >= 300 {
		e.t.Fatalf("生成对账单失败 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			ID          string `json:"id"`
			TotalAmount string `json:"total_amount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析失败：%v — %s", err, rec.Body.String())
	}
	if rec := e.call(token, "POST", "/api/v1/finance/statements/"+out.Data.ID+"/confirm", `{}`); rec.Code >= 300 {
		e.t.Fatalf("确认对账单失败 %d：%s", rec.Code, rec.Body.String())
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx, `DELETE FROM fin_statement_payment WHERE statement_id=$1::uuid`, out.Data.ID)
		_, _ = e.pool.Exec(ctx, `DELETE FROM fin_statement_line WHERE statement_id=$1::uuid`, out.Data.ID)
		_, _ = e.pool.Exec(ctx, `DELETE FROM fin_statement WHERE id=$1::uuid`, out.Data.ID)
	})
	return out.Data.ID, out.Data.TotalAmount
}

func TestConcurrentSettleCannotExceedTotal(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	id, total := e.mkDraftStatement(token)
	if total == "" || total == "0.00" {
		t.Fatalf("前提不成立：对账单金额是 %q", total)
	}

	// 并发数取 12 而不是 6：回测时 6 个并发只有一半的运行能复现
	// （竞态本来就看时序），12 个连跑四次都稳定复现。
	// 一条只有一半概率变红的并发用例，等于把问题留给运气。
	const n = 12
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = e.call(token, "POST", "/api/v1/finance/statements/"+id+"/settle",
				`{"amount":"`+total+`","method":"bank","reference":"并发用例"}`).Code
		}(i)
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, c := range codes {
		if c < 300 {
			ok++
		}
		if c >= 500 {
			t.Errorf("并发核销出现 %d —— 冲突不该表现成服务端故障", c)
		}
	}
	t.Logf("并发核销返回码：%v", codes)

	var settled, tot string
	var payments int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT settled_amount::text, total_amount::text,
		       (SELECT count(*) FROM fin_statement_payment WHERE statement_id=$1::uuid)
		FROM fin_statement WHERE id=$1::uuid`, id).Scan(&settled, &tot, &payments); err != nil {
		t.Fatalf("查对账单失败：%v", err)
	}
	if settled != tot {
		t.Errorf("已核销 %s ≠ 应结 %s", settled, tot)
	}
	if payments != 1 {
		t.Errorf("%d 个并发全额核销落了 %d 条流水（应为 1）—— 多认了钱进来，应收敞口凭空少一块",
			n, payments)
	}
	if ok != 1 {
		t.Errorf("%d 个并发核销里有 %d 个成功（应恰好 1 个）", n, ok)
	}
}
