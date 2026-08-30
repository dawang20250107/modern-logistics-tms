package main

// 关闭异常。
//
// 关闭这一步不是改个状态就完了：责任金额 > 0 时会**落一条应付费用**，
// 把异常成本带进对账。也就是说这颗按钮直接生成钱。

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func (e *testEnv) mkException(token, waybillID, level string) string {
	e.t.Helper()
	rec := e.call(token, "POST", "/api/v1/exceptions",
		`{"waybill":"`+waybillID+`","exception_type":"cargo_damage","level":"`+level+
			`","description":"用例：异常关闭","source":"manual"}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		e.t.Fatalf("造异常失败 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析失败：%v — %s", err, rec.Body.String())
	}
	e.t.Cleanup(func() {
		ctx := context.Background()
		_, _ = e.pool.Exec(ctx, `DELETE FROM fin_expense_record WHERE external_id=$1`, out.Data.ID)
		_, _ = e.pool.Exec(ctx, `DELETE FROM ops_exception_event WHERE exception_id=$1::uuid`, out.Data.ID)
		_, _ = e.pool.Exec(ctx, `DELETE FROM ops_exception WHERE id=$1::uuid`, out.Data.ID)
	})
	return out.Data.ID
}

func (e *testEnv) exceptionCosts(excID string) (n int, sum string) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*), COALESCE(sum(amount),0)::text FROM fin_expense_record
		WHERE source_system='exception' AND external_id=$1`, excID).Scan(&n, &sum); err != nil {
		e.t.Fatalf("查异常费用失败：%v", err)
	}
	return n, sum
}

// TestCloseExceptionTwiceDoesNotDoubleCharge 重复关闭不能重复计费。
//
// 关闭时带责任金额会落一条应付。而这一步原先没有任何状态守卫：
// 同一条异常连关三次，就生成三条应付——实测 800 元的赔付变成 2400 元。
// 操作员双击一下、或者网络重试一次，承运商就被多扣一倍。
func TestCloseExceptionTwiceDoesNotDoubleCharge(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbID := e.mkWaybillRow()
	excID := e.mkException(token, wbID, "high")

	body := `{"amount":800,"responsibility_party":"carrier","resolution":"承运商赔付"}`
	if rec := e.call(token, "POST", "/api/v1/exceptions/"+excID+"/close", body); rec.Code >= 300 {
		t.Fatalf("首次关闭返回 %d：%s", rec.Code, rec.Body.String())
	}
	n, sum := e.exceptionCosts(excID)
	if n != 1 || sum != "800.00" {
		t.Fatalf("首次关闭应生成 1 条 800.00 的应付，实际 %d 条 / %s", n, sum)
	}

	// 再关两次：可以拒绝，也可以幂等，但绝不能再生成应付
	for i := 0; i < 2; i++ {
		rec := e.call(token, "POST", "/api/v1/exceptions/"+excID+"/close", body)
		if rec.Code >= 500 {
			t.Fatalf("重复关闭返回 %d：%s", rec.Code, rec.Body.String())
		}
	}
	n, sum = e.exceptionCosts(excID)
	if n != 1 {
		t.Errorf("连关三次生成了 %d 条应付、合计 %s —— 800 元的赔付被计成了 %s，"+
			"双击一下承运商就被多扣一倍", n, sum, sum)
	}
}

// TestCloseExceptionKeepsFirstResolution 已关闭的异常不该被后一次调用悄悄改写处置结论。
func TestCloseExceptionKeepsFirstResolution(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbID := e.mkWaybillRow()
	excID := e.mkException(token, wbID, "medium")

	if rec := e.call(token, "POST", "/api/v1/exceptions/"+excID+"/close",
		`{"amount":0,"responsibility_party":"carrier","resolution":"承运商已致歉"}`); rec.Code >= 300 {
		t.Fatalf("关闭返回 %d：%s", rec.Code, rec.Body.String())
	}
	_ = e.call(token, "POST", "/api/v1/exceptions/"+excID+"/close",
		`{"amount":0,"responsibility_party":"customer","resolution":"改成客户责任"}`)

	var party, resolution string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT responsibility_party, resolution FROM ops_exception WHERE id=$1::uuid`, excID).
		Scan(&party, &resolution); err != nil {
		t.Fatalf("查异常失败：%v", err)
	}
	if party != "carrier" || resolution != "承运商已致歉" {
		t.Errorf("已关闭的异常被改写成了 责任=%q 处置=%q —— "+
			"定责结论是要拿去跟承运商结算的，不该被后一次调用悄悄改掉", party, resolution)
	}
}
