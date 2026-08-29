package main

// 中止运输（aborted）的行为。
//
// 加这个状态之前，departed / in_transit 没有任何出口：想作废一张已发车的运单，
// 系统允许的唯一路径是 departed → in_transit → arrived → rejected → cancelled，
// 必须先记一次**没有发生过的到达**。那个假 arrived_at 会永久留在运单时间线上，
// 而准班率正是按 arrived_at 取样的——一单没送到的货于是算成了准点交付。
//
// 这一组钉的是：出口存在、不写假里程碑、必须留下原因、且不进送达统计。

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

// mkWaybillAt 造一张指定状态的运单，返回运单号。
func (e *testEnv) mkWaybillAt(status string) string {
	e.t.Helper()
	no := "ABT" + uuid.NewString()[:8]
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, route_name, origin,
		  destination, status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
		  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type, ai_conversation_id,
		  cod_amount, cod_status, freight_payer, freight_term, platform_name, platform_order_no,
		  planned_arrival, departed_at)
		VALUES (gen_random_uuid(), now(), now(), $1, '甲 → 乙', '甲', '乙', $2,
		  'assigned', 'low', 'pending', 0, 1, 1, 1, 'outsource', '', 0, 'none',
		  'consignor', 'prepaid', '', '', now() + interval '5 hours', now())`, no, status); err != nil {
		e.t.Fatalf("造运单失败：%v", err)
	}
	e.t.Cleanup(func() {
		ctx := context.Background()
		_, _ = e.pool.Exec(ctx, `DELETE FROM ops_waybill_event WHERE waybill_id IN
			(SELECT id FROM ops_waybill WHERE waybill_no=$1)`, no)
		_, _ = e.pool.Exec(ctx, `DELETE FROM ops_waybill WHERE waybill_no=$1`, no)
	})
	return no
}

func (e *testEnv) transition(tok, no, to, remark string) *httpRec {
	e.t.Helper()
	body, _ := json.Marshal(map[string]string{"to_status": to, "remark": remark})
	rec := e.call(tok, "POST", "/api/v1/waybills/"+no+"/transition", string(body))
	return &httpRec{code: rec.Code, body: rec.Body.String()}
}

type httpRec struct {
	code int
	body string
}

// TestAbortRequiresReason 中止必须写明原因。
//
// 别的流转事后看状态本身就说明了发生过什么；中止不是——它意味着一趟已经
// 出车的运输被人为终止，后面必然跟着费用争议（空驶费怎么算、半程运费给不给）
// 和责任认定。那时候唯一能还原现场的就是这条原因。
func TestAbortRequiresReason(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	no := e.mkWaybillAt("departed")

	if rec := e.transition(tok, no, wbstatus.Aborted, ""); rec.code != http.StatusBadRequest {
		t.Errorf("不填原因中止 → %d，期望 400。响应：%s", rec.code, truncate(rec.body, 160))
	}
	if rec := e.transition(tok, no, wbstatus.Aborted, "   "); rec.code != http.StatusBadRequest {
		t.Errorf("原因只填空白 → %d，期望 400", rec.code)
	}
	if rec := e.transition(tok, no, wbstatus.Aborted, "G60 追尾，车辆拖修"); rec.code != http.StatusOK {
		t.Fatalf("填了原因仍失败 → %d：%s", rec.code, truncate(rec.body, 200))
	}
}

// TestAbortDoesNotWriteFakeArrival 中止**不能**写 arrived_at。
//
// 这是整件事的要害：原先绕路作废必须先记一次假到达，
// 而准班率按 arrived_at 取样——假到达会让一单没送到的货算成准点交付。
func TestAbortDoesNotWriteFakeArrival(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)

	for _, from := range []string{"departed", "in_transit"} {
		t.Run(from, func(t *testing.T) {
			no := e.mkWaybillAt(from)
			if rec := e.transition(tok, no, wbstatus.Aborted, "客户取消"); rec.code != http.StatusOK {
				t.Fatalf("%s → aborted 失败 %d：%s", from, rec.code, truncate(rec.body, 160))
			}
			var arrived, signed *string
			var status string
			if err := e.pool.QueryRow(context.Background(),
				`SELECT status, arrived_at::text, signed_at::text FROM ops_waybill WHERE waybill_no=$1`,
				no).Scan(&status, &arrived, &signed); err != nil {
				t.Fatal(err)
			}
			if status != wbstatus.Aborted {
				t.Errorf("状态是 %s，期望 aborted", status)
			}
			if arrived != nil {
				t.Errorf("中止写了 arrived_at=%s —— 这正是要消灭的假到达，"+
					"它会让这单进准班率取样", *arrived)
			}
			if signed != nil {
				t.Errorf("中止写了 signed_at=%s", *signed)
			}
		})
	}
}

// TestAbortRecordsReasonInTimeline 原因要落在运单时间线上，事后查得到。
func TestAbortRecordsReasonInTimeline(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	no := e.mkWaybillAt("in_transit")
	const reason = "装错货，需返厂重装"
	if rec := e.transition(tok, no, wbstatus.Aborted, reason); rec.code != http.StatusOK {
		t.Fatalf("中止失败 %d：%s", rec.code, truncate(rec.body, 160))
	}
	var got string
	if err := e.pool.QueryRow(context.Background(), `
		SELECT COALESCE(payload->>'remark','') FROM ops_waybill_event
		WHERE waybill_id=(SELECT id FROM ops_waybill WHERE waybill_no=$1)
		  AND event_type=$2 ORDER BY created_at DESC LIMIT 1`,
		no, "status_changed:"+wbstatus.Aborted).Scan(&got); err != nil {
		t.Fatalf("查不到中止事件：%v", err)
	}
	if got != reason {
		t.Errorf("时间线里记的原因是 %q，期望 %q", got, reason)
	}
}

// TestAbortLeadsToSettlement 中止之后要能结算——中止基本都有钱要算。
func TestAbortLeadsToSettlement(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	no := e.mkWaybillAt("departed")
	if rec := e.transition(tok, no, wbstatus.Aborted, "事故"); rec.code != http.StatusOK {
		t.Fatalf("中止失败 %d", rec.code)
	}
	if rec := e.transition(tok, no, "settled", "仅结空驶费"); rec.code != http.StatusOK {
		t.Errorf("中止后结算 → %d：%s —— 中止单必须能走完财务，"+
			"否则它会永远挂在那里", rec.code, truncate(rec.body, 160))
	}
}

// TestAbortNotAllowedBeforeDeparture 车还没走的时候不该用「中止」。
//
// 那些状态本来就有 cancelled / voided 可用，再给一条中止只会让操作员
// 在三个意思相近的动作之间犹豫。中止专指"车走了但没送到"。
func TestAbortNotAllowedBeforeDeparture(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	for _, from := range []string{"draft", "pending_dispatch", "dispatched", "loaded"} {
		t.Run(from, func(t *testing.T) {
			no := e.mkWaybillAt(from)
			if rec := e.transition(tok, no, wbstatus.Aborted, "原因"); rec.code != http.StatusConflict {
				t.Errorf("%s → aborted 得到 %d，期望 409（这些状态该用取消/作废）", from, rec.code)
			}
		})
	}
}
