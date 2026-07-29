package waybills

// 运单状态机：POST /waybills/{no}/transition + /sign + /stop-event。
// 对齐 apps/ops/services.{transition_waybill, sign_waybill} 与 views.stop_event。
// emit_event 对外 Webhook 与 publish_event(SSE) 属阶段 3 平台域，随 SSE 移植补齐。

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// 合法状态流转表（对齐 ALLOWED_TRANSITIONS + _VOIDABLE_FROM）
var allowedTransitions = map[string][]string{
	"draft":            {"pending_dispatch", "cancelled", "voided"},
	"pending_dispatch": {"dispatched", "cancelled", "voided"},
	"dispatched":       {"loaded", "pending_dispatch", "cancelled", "voided"},
	"loaded":           {"departed", "voided"},
	"departed":         {"in_transit"},
	"in_transit":       {"arrived"},
	"arrived":          {"signed", "partially_signed", "rejected"},
	"partially_signed": {"signed", "delivered", "rejected"},
	"rejected":         {"settled", "cancelled"},
	"signed":           {"delivered"},
	"delivered":        {"settled"},
	"settled":          {},
	"cancelled":        {},
	"voided":           {},
}

var milestoneField = map[string]string{
	"loaded": "loaded_at", "departed": "departed_at", "arrived": "arrived_at", "signed": "signed_at",
}

func canGo(from, to string) bool {
	for _, s := range allowedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

func wbEvent(ctx context.Context, tx pgx.Tx, waybillID, eventType, resource string, payload map[string]any) {
	pj, _ := json.Marshal(payload)
	eid, _ := uuid.NewV7()
	_, _ = tx.Exec(ctx, `
		INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type, event_time, source, resource, payload)
		VALUES ($1, now(), now(), $2::uuid, $3, clock_timestamp(), $4, $5, $6)`,
		eid.String(), waybillID, eventType, payload["__source"], resource, pj)
}

type wbRow struct {
	ID, No, Status  string
	OrderID, Driver *string
}

func lockWaybill(ctx context.Context, tx pgx.Tx, no string) (*wbRow, error) {
	w := &wbRow{}
	err := tx.QueryRow(ctx, `
		SELECT id::text, waybill_no, status, order_id::text, driver_id::text
		FROM ops_waybill WHERE waybill_no=$1 FOR UPDATE`, no,
	).Scan(&w.ID, &w.No, &w.Status, &w.OrderID, &w.Driver)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return w, err
}

// doTransition 状态推进 + 里程碑物化 + 事件 + 订单完成回写（须在事务内）
func doTransition(ctx context.Context, tx pgx.Tx, w *wbRow, to, remark string) (int, string, string) {
	if !canGo(w.Status, to) {
		return 409, "INVALID_TRANSITION", "不允许从 " + w.Status + " 流转到 " + to + "。"
	}
	from := w.Status
	set := "status=$2, updated_at=now()"
	if f, ok := milestoneField[to]; ok {
		set += ", " + f + " = COALESCE(" + f + ", now())"
	}
	if _, err := tx.Exec(ctx, "UPDATE ops_waybill SET "+set+" WHERE id=$1::uuid", w.ID, to); err != nil {
		return 500, "INTERNAL", "更新失败"
	}
	w.Status = to
	pj, _ := json.Marshal(map[string]any{"from": from, "to": to, "remark": remark})
	eid, _ := uuid.NewV7()
	_, _ = tx.Exec(ctx, `
		INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type, event_time, source, resource, payload)
		VALUES ($1, now(), now(), $2::uuid, $3, clock_timestamp(), 'transition', $4, $5)`,
		eid.String(), w.ID, "status_changed:"+to, w.No, pj)
	completeOrderOnDelivery(ctx, tx, w, to)
	return 0, "", ""
}

// completeOrderOnDelivery 签收/送达/结算 → 全部兄弟运单完成才回写订单已完成（幂等）
func completeOrderOnDelivery(ctx context.Context, tx pgx.Tx, w *wbRow, to string) {
	if to != "signed" && to != "delivered" && to != "settled" {
		return
	}
	if w.OrderID == nil {
		return
	}
	_, _ = tx.Exec(ctx, `
		UPDATE ops_order SET status='completed', updated_at=now()
		WHERE id=$1::uuid AND status <> 'completed'
		  AND NOT EXISTS (
		    SELECT 1 FROM ops_waybill s
		    WHERE s.order_id=$1::uuid AND s.status NOT IN ('cancelled','voided','signed','delivered','settled'))`,
		*w.OrderID)
}

// Transition POST /api/v1/waybills/{no}/transition {to_status, remark}
func (h *Handler) Transition(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := h.Svc.UserByID(ctx, auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		ToStatus string `json:"to_status"`
		Remark   string `json:"remark"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ToStatus == "" {
		httpx.Err(w, http.StatusBadRequest, "TO_STATUS_REQUIRED", "to_status 必填。")
		return
	}
	no := chi.URLParam(r, "no")
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	wb, err := lockWaybill(ctx, tx, no)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取运单失败")
		return
	}
	if wb == nil {
		httpx.Err(w, http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在。")
		return
	}
	if code, appCode, msg := doTransition(ctx, tx, wb, body.ToStatus, body.Remark); appCode != "" {
		httpx.Err(w, code, appCode, msg)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waybill_no": wb.No, "status": wb.Status, "next_statuses": allowedTransitions[wb.Status],
	})
}

// Sign POST /api/v1/waybills/{no}/sign —— e-POD 签收：落回单 + 状态推进 + 订单完成 + 司机累计
func (h *Handler) Sign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Signatory  string `json:"signatory"`
		Signature  string `json:"signature"`
		FileURL    string `json:"file_url"`
		SignSource string `json:"sign_source"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.SignSource == "" {
		body.SignSource = "driver"
	}
	no := chi.URLParam(r, "no")
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	wb, err := lockWaybill(ctx, tx, no)
	if err != nil || wb == nil {
		httpx.Err(w, http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在。")
		return
	}
	switch wb.Status {
	case "signed", "delivered", "settled":
		httpx.Err(w, http.StatusConflict, "ALREADY_SIGNED", "运单已签收。")
		return
	case "in_transit", "arrived", "partially_signed":
	default:
		httpx.Err(w, http.StatusConflict, "NOT_SIGNABLE", "仅在途/已到达运单可签收。")
		return
	}
	rid, _ := uuid.NewV7()
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_receipt (id, created_at, updated_at, waybill_id, receipt_type, status, file_url,
		  ocr_status, ocr_result, signatory, signed_at, signature, sign_source, outcome,
		  total_quantity, signed_quantity, damaged_quantity, shortage_quantity, rejection_reason, uploaded_by_id)
		VALUES ($1, now(), now(), $2::uuid, 'signed_pod', 'confirmed', $3,
		  'pending', '{}'::jsonb, $4, now(), $5, $6, 'full', 0, 0, 0, 0, '', $7::uuid)`,
		rid.String(), wb.ID, body.FileURL, body.Signatory, body.Signature, body.SignSource, me.ID); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回单落库失败")
		return
	}
	if wb.Status == "in_transit" {
		if code, appCode, msg := doTransition(ctx, tx, wb, "arrived", "签收回传自动到达"); appCode != "" {
			httpx.Err(w, code, appCode, msg)
			return
		}
	}
	if code, appCode, msg := doTransition(ctx, tx, wb, "signed", "签收人 "+body.Signatory); appCode != "" {
		httpx.Err(w, code, appCode, msg)
		return
	}
	if _, err := tx.Exec(ctx, "UPDATE ops_waybill SET receipt_status='received', updated_at=now() WHERE id=$1::uuid", wb.ID); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回单状态更新失败")
		return
	}
	// 司机累计统计刷新（签收完成后）
	if wb.Driver != nil {
		_, _ = tx.Exec(ctx, `
			UPDATE md_driver d SET
			  cumulative_waybills = (SELECT count(*) FROM ops_waybill x WHERE x.driver_id=d.id AND x.status IN ('signed','delivered','settled')),
			  cumulative_freight = COALESCE((SELECT sum(e.amount) FROM fin_expense_record e JOIN ops_waybill x ON x.id=e.waybill_id
			                                 WHERE x.driver_id=d.id AND e.direction='payable'),0),
			  updated_at = now()
			WHERE d.id=$1::uuid`, *wb.Driver)
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waybill_no": wb.No, "status": wb.Status, "receipt_status": "received",
		"receipt_id": rid.String(), "signed_at": httpx.Micros(time.Now()).In(time.FixedZone("CST", 8*3600)),
	})
}

// StopEvent POST /api/v1/waybills/{no}/stop-event {seq, event: arrived|departed}
func (h *Handler) StopEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := h.Svc.UserByID(ctx, auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Seq   int    `json:"seq"`
		Event string `json:"event"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Event != "arrived" && body.Event != "departed" {
		httpx.Err(w, http.StatusBadRequest, "INVALID_STOP_EVENT", "event 取值 arrived|departed。")
		return
	}
	no := chi.URLParam(r, "no")
	var wbID string
	if err := h.DB.QueryRow(ctx, "SELECT id::text FROM ops_waybill WHERE waybill_no=$1", no).Scan(&wbID); err != nil {
		httpx.Err(w, http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在。")
		return
	}
	var set string
	if body.Event == "arrived" {
		set = "actual_arrival_at=now(), arrival_source='manual', status='arrived'"
	} else {
		set = "actual_depart_at=now(), status='departed'"
	}
	// 与 Django .first() 语义一致：seq 重复时只更新首行
	ct, err := h.DB.Exec(ctx, "UPDATE ops_waybill_stop SET "+set+", updated_at=now() WHERE id=(SELECT id FROM ops_waybill_stop WHERE waybill_id=$1::uuid AND seq=$2 ORDER BY seq, id LIMIT 1)", wbID, body.Seq)
	if err != nil || ct.RowsAffected() == 0 {
		httpx.Err(w, http.StatusNotFound, "STOP_NOT_FOUND", "点位不存在。")
		return
	}
	pj, _ := json.Marshal(map[string]any{"seq": body.Seq})
	eid, _ := uuid.NewV7()
	_, _ = h.DB.Exec(ctx, `
		INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type, event_time, source, resource, payload)
		VALUES ($1, now(), now(), $2::uuid, $3, clock_timestamp(), 'manual', $4, $5)`,
		eid.String(), wbID, "stop_"+body.Event, "stop#"+strconv.Itoa(body.Seq), pj)
	httpx.JSON(w, http.StatusOK, map[string]any{"waybill_no": no, "seq": body.Seq, "event": body.Event})
}
