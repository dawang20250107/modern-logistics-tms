package main

// 报销审批的并发。
//
// 审批这一步会做三件不可逆的事：生成一条应付、开一张付款申请、把报销标为已审批。
// 状态检查（"仅已提交的报销可审批"）读的是**事务外**的一次查询，
// 两个人同时点审批，两边都会读到 submitted、都通过检查、都往下走——
// 司机那笔 158.50 变成两条应付、两张付款申请。这不是数字算错，是钱付两遍。

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

func (e *testEnv) mkReimbursement(token, waybillNo, amount string) string {
	e.t.Helper()
	rec := e.call(token, "POST", "/api/v1/finance/reimbursements",
		`{"waybill_no":"`+waybillNo+`","category":"toll","amount":"`+amount+`","reason":"并发用例"}`)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("造报销失败 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析失败：%v", err)
	}
	e.t.Cleanup(func() {
		ctx := context.Background()
		_, _ = e.pool.Exec(ctx, `DELETE FROM fin_payment_request WHERE request_no LIKE 'PR-%'
			AND waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)`, waybillNo)
		_, _ = e.pool.Exec(ctx, `DELETE FROM fin_expense_record WHERE source_system='reimbursement'
			AND waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)`, waybillNo)
		_, _ = e.pool.Exec(ctx, `DELETE FROM fin_reimbursement WHERE id=$1::uuid`, out.Data.ID)
	})
	return out.Data.ID
}

func TestConcurrentReimbursementApproveOnlyPaysOnce(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbNo := e.mkWaybillAt("arrived")
	rid := e.mkReimbursement(token, wbNo, "158.50")

	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = e.call(token, "POST", "/api/v1/finance/reimbursements/"+rid+"/approve", `{}`).Code
		}(i)
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, c := range codes {
		if c < 300 {
			ok++
		}
	}
	t.Logf("并发审批的返回码：%v", codes)
	// 没抢到的必须拿到 409，而不是 500。
	//
	// 修之前是 [409 409 500 409 500 200]：钱没付两遍，但拦住它的是
	// 付款申请单号上的唯一索引**碰巧**撞了车，而不是审批逻辑本身。
	// 代价是有人收到 500「生成付款申请失败」——那看起来像系统故障，
	// 实际是"别人先批了"，而操作员多半会重试或者报 bug。
	for i, c := range codes {
		if c >= 500 {
			t.Errorf("第 %d 个并发审批拿到 %d —— 并发冲突不该表现成服务端故障", i, c)
		}
	}

	ctx := context.Background()
	var payables, requests int
	var payableSum string
	if err := e.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(amount),0)::text FROM fin_expense_record
		WHERE source_system='reimbursement' AND external_id IN
		  (SELECT reimb_no FROM fin_reimbursement WHERE id=$1::uuid)`, rid).
		Scan(&payables, &payableSum); err != nil {
		t.Fatalf("查应付失败：%v", err)
	}
	if err := e.pool.QueryRow(ctx, `
		SELECT count(*) FROM fin_payment_request WHERE request_no IN
		  (SELECT 'PR-'||reimb_no FROM fin_reimbursement WHERE id=$1::uuid)`, rid).
		Scan(&requests); err != nil {
		t.Fatalf("查付款申请失败：%v", err)
	}

	if payables != 1 {
		t.Errorf("%d 个并发审批生成了 %d 条应付（合计 %s）——同一笔报销被计了多次账",
			n, payables, payableSum)
	}
	if requests != 1 {
		t.Errorf("%d 个并发审批开了 %d 张付款申请 —— 钱会付多遍", n, requests)
	}
	if ok != 1 {
		t.Errorf("%d 个并发审批里有 %d 个返回成功（应恰好 1 个）", n, ok)
	}
}
