package main

// 给司机发作业提醒。
//
// 运单详情页上有一颗「发送提醒」按钮（这一颗是真渲染出来的），
// 它 POST 到 /waybills/{no}/reminders —— 而后端只注册了 GET，恒定 405。
//
// 司机端那一侧是全的：/driver/tasks 会把待确认的提醒当强制弹窗推给司机，
// /driver/reminders/{id}/ack 收确认。也就是说收的一头做好了，发的一头没有。
// 「装车前先拍照」「这批货不能倒放」这类提醒发不出去，
// 调度员只能打电话——而电话说过的话，出事时谁都不认。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

// TestSendReminderFromWaybill 从运单页发提醒，必须真的落到司机的待办里。
func TestSendReminderFromWaybill(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillWithDriver()

	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/reminders",
		`{"content":"装车前先拍一张货物照片","ack_required":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("发提醒返回 %d：%s", rec.Code, rec.Body.String())
	}

	// 列表里要看得到
	got := e.call(token, "GET", "/api/v1/waybills/"+no+"/reminders", "")
	if got.Code != http.StatusOK {
		t.Fatalf("查提醒返回 %d", got.Code)
	}
	var out struct {
		Data []struct {
			Content     string `json:"content"`
			AckRequired bool   `json:"ack_required"`
			DriverName  string `json:"driver_name"`
			Status      string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v — %s", err, got.Body.String())
	}
	if len(out.Data) == 0 {
		t.Fatal("发完之后列表里一条都没有")
	}
	r0 := out.Data[0]
	if !strings.Contains(r0.Content, "装车前") {
		t.Errorf("提醒内容不对：%q", r0.Content)
	}
	if !r0.AckRequired {
		t.Error("ack_required 没传下去——司机端就不会强制弹窗，提醒等于没发")
	}
	// 提醒必须绑到这张运单的司机身上，否则司机端拉不到
	if r0.DriverName == "" {
		t.Error("提醒没有绑定司机——司机端按 driver_id 拉待办，绑不上就永远收不到")
	}
	// 状态必须是司机端拉待办用的那个值。
	//
	// 这一条是端到端跑出来的：第一版把状态写成了 'sent'，而司机端拉的是
	// status='pending'——接口返回 201、运单页的列表里也有那一条，
	// 只有司机那边什么都没收到。发完之后司机的待确认列表还是 0 条。
	// 只断言"发送成功"的用例，对这种错完全免疫。
	if r0.Status != wbstatus.ReminderPending {
		t.Errorf("提醒状态是 %q，而司机端只拉 %q —— 提醒发出去了，司机永远看不到",
			r0.Status, wbstatus.ReminderPending)
	}
}

// TestReminderReachesDriverQueue 直接按司机端那条查询验一遍：发完必须进待办。
//
// 上一条是白盒（比状态字面量），这一条是黑盒：不管状态叫什么，
// 司机那边必须真的能拉到。两条一起，改状态词表时不会两边同时被绕过去。
func TestReminderReachesDriverQueue(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillWithDriver()

	if rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/reminders",
		`{"content":"这批货不能倒放","ack_required":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("发提醒返回 %d：%s", rec.Code, rec.Body.String())
	}

	var n int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM ops_driver_reminder dr
		WHERE dr.driver_id = (SELECT driver_id FROM ops_waybill WHERE waybill_no=$1)
		  AND dr.status = $2`, no, wbstatus.ReminderPending).Scan(&n); err != nil {
		t.Fatalf("查司机待办失败：%v", err)
	}
	if n == 0 {
		t.Error("提醒没有进司机的待确认队列 —— 司机端那个强制弹窗不会弹")
	}
}

// TestSendReminderNeedsContent 空内容不能发。
func TestSendReminderNeedsContent(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillWithDriver()

	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/reminders", `{"content":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("空内容应 400，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

// TestSendReminderNeedsDriver 没司机的运单发不了提醒——发给谁？
func TestSendReminderNeedsDriver(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillAt("dispatched")

	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/reminders", `{"content":"注意安全"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("没司机时应 400，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM ops_driver_reminder
		WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)`, no).Scan(&n)
	if n != 0 {
		t.Errorf("没司机却落了 %d 条提醒 —— 那是永远没人会看到的记录", n)
	}
}
