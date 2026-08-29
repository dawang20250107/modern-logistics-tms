package main

// 回单核验：/receipts/{id}/confirm。
//
// 这个端点前端目前没有调用方（是给对接方/后台脚本用的），但它对所有
// 已鉴权调用方开放，而且它写的是**结算闸门**：运单的 receipt_status
// 一旦变成"已核销"，回单付的单子就可以据此结算。

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

// mkReceipt 在一张运单下造一张回单，返回回单 id。
func (e *testEnv) mkReceipt(token, waybillID string) string {
	e.t.Helper()
	rec := e.call(token, "POST", "/api/v1/receipts",
		`{"waybill":"`+waybillID+`","file_url":"https://example.com/pod.png"}`)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("造回单失败 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析失败：%v", err)
	}
	return out.Data.ID
}

func (e *testEnv) waybillReceiptStatus(id string) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT receipt_status FROM ops_waybill WHERE id=$1::uuid`, id).Scan(&s); err != nil {
		e.t.Fatalf("查运单回单状态失败：%v", err)
	}
	return s
}

// TestReceiptRejectClearsWaybillAudited 把已核验的回单改判为不通过，
// 运单上的「已核销」必须跟着撤销。
//
// 原实现只写"核验通过 → 运单标已核销"这一个方向。改判为不通过时，
// 运单上那个"已核销"留在原地：回单已经被否了，而运单看起来仍然凭证齐全，
// 回单付的单子就凭这个放行结算——钱付出去了，凭证其实不成立。
func TestReceiptRejectClearsWaybillAudited(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbID := e.mkWaybillRow()
	rid := e.mkReceipt(token, wbID)

	if rec := e.call(token, "POST", "/api/v1/receipts/"+rid+"/confirm", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("核验通过返回 %d：%s", rec.Code, rec.Body.String())
	}
	if got := e.waybillReceiptStatus(wbID); got != wbstatus.ReceiptAudited {
		t.Fatalf("核验通过后运单回单状态=%q，期望 %q", got, wbstatus.ReceiptAudited)
	}

	if rec := e.call(token, "POST", "/api/v1/receipts/"+rid+"/confirm",
		`{"status":"rejected"}`); rec.Code != http.StatusOK {
		t.Fatalf("改判返回 %d：%s", rec.Code, rec.Body.String())
	}
	if got := e.waybillReceiptStatus(wbID); got == wbstatus.ReceiptAudited {
		t.Error("回单已被改判为不通过，运单上仍然写着「已核销」—— " +
			"回单付的单子会凭一个不成立的凭证放行结算")
	}
}

// TestReceiptConfirmRejectsUnknownStatus 状态取值必须在词表里。
//
// 原先是照单全收：传什么写什么，可以往库里写一个 'banana'，
// 界面上就露出这个英文串（渲染一律是 LABEL[x] ?? x），
// 而且再没有任何流程认得它。
func TestReceiptConfirmRejectsUnknownStatus(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbID := e.mkWaybillRow()
	rid := e.mkReceipt(token, wbID)

	rec := e.call(token, "POST", "/api/v1/receipts/"+rid+"/confirm", `{"status":"banana"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法状态应 400，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var st string
	_ = e.pool.QueryRow(context.Background(),
		`SELECT status FROM ops_receipt WHERE id=$1::uuid`, rid).Scan(&st)
	if st == "banana" {
		t.Error("非法状态被写进库里了")
	}
}

// TestReceiptStillAuditedWhenAnotherConfirmedExists 一张运单可以有多张回单：
// 否掉其中一张，只要还有别的通过了，运单就仍然算凭证齐全。
func TestReceiptStillAuditedWhenAnotherConfirmedExists(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbID := e.mkWaybillRow()
	r1 := e.mkReceipt(token, wbID)
	r2 := e.mkReceipt(token, wbID)

	_ = e.call(token, "POST", "/api/v1/receipts/"+r1+"/confirm", `{}`)
	_ = e.call(token, "POST", "/api/v1/receipts/"+r2+"/confirm", `{}`)
	if rec := e.call(token, "POST", "/api/v1/receipts/"+r1+"/confirm", `{"status":"rejected"}`); rec.Code != http.StatusOK {
		t.Fatalf("改判返回 %d", rec.Code)
	}
	if got := e.waybillReceiptStatus(wbID); got != wbstatus.ReceiptAudited {
		t.Errorf("还有一张回单是通过的，运单不该被撤销核销：实际 %q", got)
	}
}
