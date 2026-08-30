package main

// 运单列表上的批量「回单已回收」。
//
// 前端对每条选中的运单发 PATCH /waybills/{no} {receipt_status:"returned"}，
// 而后端这条路径只注册了 GET —— 每一条都 405。更糟的是失败被
// `catch { /* 单条失败不阻断整批 */ }` 吞掉，最后照样弹一个**成功**提示
// 「已标记 0/5 条运单回单为「已回收」」：绿色对勾、语气笃定，而什么都没发生。
//
// 回单状态是财务催收的依据（回单付的单子，回单没回收就不能结）。
// 操作员以为标完了，实际一条没标。

import (
	"context"
	"net/http"
	"testing"
)

func (e *testEnv) receiptStatusOf(no string) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT receipt_status FROM ops_waybill WHERE waybill_no=$1`, no).Scan(&s); err != nil {
		e.t.Fatalf("查回单状态失败：%v", err)
	}
	return s
}

// TestWaybillPatchReceiptStatus 批量标记回单必须真的改到库里。
func TestWaybillPatchReceiptStatus(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillAt("arrived")

	rec := e.call(token, "PATCH", "/api/v1/waybills/"+no, `{"receipt_status":"returned"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH 返回 %d：%s", rec.Code, rec.Body.String())
	}
	if got := e.receiptStatusOf(no); got != "returned" {
		t.Errorf("库里的回单状态还是 %q —— 界面上却报告已标记成功", got)
	}
}

// TestWaybillPatchRejectsUnknownFields 这条 PATCH 只开放回单状态，别的一概不认。
//
// 运单上挂着状态机、司机车辆、金额。开一个什么都能改的 PATCH，
// 等于绕开状态机和所有业务校验——批量标回单这一个需求，不值这个代价。
func TestWaybillPatchRejectsUnknownFields(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillAt("dispatched")

	for _, body := range []string{
		`{"status":"settled"}`,
		`{"driver_id":"00000000-0000-0000-0000-000000000000"}`,
		`{"receipt_status":"returned","status":"settled"}`,
	} {
		rec := e.call(token, "PATCH", "/api/v1/waybills/"+no, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s 应被拒（400），实际 %d：%s", body, rec.Code, rec.Body.String())
		}
	}
	// 状态一个都不能被改动
	var st string
	_ = e.pool.QueryRow(context.Background(),
		`SELECT status FROM ops_waybill WHERE waybill_no=$1`, no).Scan(&st)
	if st != "dispatched" {
		t.Errorf("运单状态被 PATCH 改成了 %q —— 状态机被绕过去了", st)
	}
}

// TestWaybillPatchRejectsBadReceiptStatus 回单状态取值也要在词表里。
func TestWaybillPatchRejectsBadReceiptStatus(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillAt("arrived")

	rec := e.call(token, "PATCH", "/api/v1/waybills/"+no, `{"receipt_status":"whatever"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法回单状态应 400，实际 %d：%s", rec.Code, rec.Body.String())
	}
}
