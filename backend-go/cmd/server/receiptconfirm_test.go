package main

// 回单核验：/receipts/{id}/confirm。
//
// 它写的是**结算闸门**：运单的 receipt_status 一旦变成"已核销"，
// 回单付的单子就可以据此结算。
//
// 这段注释原先写的是"前端目前没有调用方（是给对接方/后台脚本用的）"。
// 那句话曾经是真的，也正因为它是真的，界面上一直没人发现回单区
// 少了整块核验入口——**注释里那种"目前没人用"的说法会自己过期，
// 而过期之后它比没写更误导**：读的人会以为这里是有意不做界面的。
// 运单详情的回单区现在有「核验通过」「驳回」两颗按钮，
// reachable-actions.py 盯着它不再消失。

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
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

// TestReceiptConfirmRejectRaceStaysConsistent 两个人同时核验同一张回单，
// 一个点通过、一个点驳回。
//
// txn-guard.py 把这个 handler 登记成"裸守卫但无害"，理由是：后写覆盖前写
// 正是想要的语义，而回写运单的回单状态是按「这张运单还有没有通过核验的
// 回单」在同一条 SQL 里重算的，不依赖前面那次读到的旧值。
//
// 那条理由现在是这个用例在撑着。界面补上两颗按钮之后，"两个人同时点"
// 从假想变成了日常——单据员和财务对着同一张回单各按各的，是真会发生的。
// 一旦有人把那条重算改回"先读再写"，这里就会留下
// 「回单已驳回、运单仍是已核销」的账：凭证不成立而钱照付。
func TestReceiptConfirmRejectRaceStaysConsistent(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)

	// 轮数是量出来的，不是拍的。把那条重算 SQL 改成"用刚写进去的值"
	// （正是豁免理由排除的那种写法），再数它多少次会红：
	// 单轮 12 并发约 21% 检出，5 轮 7/10，20 轮才到 99% 以上。
	//
	// **一条八次只红一次的并发用例等于把问题留给运气**：它在 CI 上会以
	// "偶发失败"的面目出现，几次之后就被当成抖动重跑掉——比没有这条用例
	// 更坏，因为它还消耗了一次"这里已经测过了"的信任。
	for round := 0; round < 20; round++ {
		wbID := e.mkWaybillRow()
		rid := e.mkReceipt(token, wbID)

		var wg sync.WaitGroup
		for i := 0; i < 12; i++ {
			body := `{"status":"confirmed"}`
			if i%2 == 1 {
				body = `{"status":"rejected"}`
			}
			wg.Add(1)
			go func(b string) {
				defer wg.Done()
				_ = e.call(token, "POST", "/api/v1/receipts/"+rid+"/confirm", b)
			}(body)
		}
		wg.Wait()

		var confirmed int
		if err := e.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM ops_receipt WHERE waybill_id=$1::uuid AND status=$2`,
			wbID, wbstatus.PODConfirmed).Scan(&confirmed); err != nil {
			t.Fatalf("第 %d 轮统计已核验回单失败：%v", round, err)
		}

		// 要钉的不变量只有一条：**没有一张通过核验的回单时，
		// 运单不能写着「已核销」**。
		//
		// 这条用例的第一版把期望写成"没有通过的就该是 returned"，跑八遍红两遍：
		// 全被驳回时运单停在 pending。去看那条重算 SQL——它只在运单当前
		// 就是 audited 时才退回 returned，否则原样不动。这是对的：
		// 一张从没核销过的运单，把它的回单驳回，不该凭空多出一个"已回收"。
		// **红的是我写的期望，不是被测的代码**；照着改代码就会为了让用例
		// 变绿而写进一个假状态。
		got := e.waybillReceiptStatus(wbID)
		if confirmed == 0 && got == wbstatus.ReceiptAudited {
			t.Fatalf("第 %d 轮并发核验后账对不上：这张运单一张通过核验的回单都没有，"+
				"运单却写着「已核销」（%q）—— 凭证不成立而钱照付", round, got)
		}
		if confirmed > 0 && got != wbstatus.ReceiptAudited {
			t.Fatalf("第 %d 轮并发核验后账对不上：这张运单有 %d 张已核验回单，运单却是 %q",
				round, confirmed, got)
		}
	}
}
