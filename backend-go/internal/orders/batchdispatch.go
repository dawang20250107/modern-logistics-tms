package orders

// 批量派承运商：POST /orders/batch-dispatch，对齐 order_dispatch.batch_dispatch_orders。
// 一批订单委托同一承运商/网货平台 → 派车批次 + N 张独立运单 + 应付分摊快照。
// 批次通道仅 third_party/platform（无车辆/司机，不涉及运力占用与合同生成）。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

type batchReq struct {
	IDs            []string          `json:"ids"`
	DispatchType   string            `json:"dispatch_type"`
	Carrier        string            `json:"carrier"`
	PlatformName   string            `json:"platform_name"`
	TotalPayable   json.Number       `json:"total_payable"`
	Allocation     string            `json:"allocation"`
	ManualPayables map[string]string `json:"manual_payables"`
	Note           string            `json:"note"`
}

type dispatchableOrder struct {
	ID, OrderNo, Status, CustomerName, Origin, Destination, AIConvID string
	CustomerID, ClaimedBy, AssignedTo, ProjectID                     *string
	Weight                                                           decimal.Decimal
	Quantity                                                         int
	VolumeCbm, CodAmount                                             decimal.Decimal
	FreightTerm, FreightPayer                                        string
	ExpectedDeliveryAt                                               *time.Time
}

// allocatePayable 分摊：按吨占比 / 均摊 / 逐单指定；末单吸收舍入误差（对齐 _allocate_payable）
func allocatePayable(total decimal.Decimal, orders []dispatchableOrder, allocation string, manual map[string]string) map[string]decimal.Decimal {
	out := map[string]decimal.Decimal{}
	if allocation == "manual" && manual != nil {
		for _, o := range orders {
			v, _ := decimal.NewFromString(manual[o.ID])
			out[o.ID] = v
		}
		return out
	}
	if total.LessThanOrEqual(decimal.Zero) || len(orders) == 0 {
		for _, o := range orders {
			out[o.ID] = decimal.Zero
		}
		return out
	}
	wsum := decimal.Zero
	for _, o := range orders {
		wsum = wsum.Add(o.Weight)
	}
	running := decimal.Zero
	for i, o := range orders {
		if i == len(orders)-1 {
			out[o.ID] = total.Sub(running)
			break
		}
		var part decimal.Decimal
		if allocation == "by_weight" && wsum.GreaterThan(decimal.Zero) {
			part = total.Mul(o.Weight).Div(wsum).Round(2)
		} else {
			part = total.Div(decimal.NewFromInt(int64(len(orders)))).Round(2)
		}
		out[o.ID] = part
		running = running.Add(part)
	}
	return out
}

// carrierBlockReason 对齐 Carrier.dispatch_block_reason（黑名单/停用/资质过期）
func (h *Handler) carrierBlockReason(ctx context.Context, carrierID string) (name, reason string, found bool) {
	var blacklisted, active bool
	var blReason string
	var qualExpiry *time.Time
	err := h.DB.QueryRow(ctx, `
		SELECT name, blacklisted, COALESCE(blacklist_reason,''), is_active, qualification_expiry::timestamptz
		FROM md_carrier WHERE id=$1::uuid`, carrierID).Scan(&name, &blacklisted, &blReason, &active, &qualExpiry)
	if err != nil {
		return "", "", false
	}
	today := time.Now().In(cstZone).Truncate(24 * time.Hour)
	switch {
	case blacklisted:
		reason = "承运商 " + name + " 已列入黑名单"
		if blReason != "" {
			reason += "（" + blReason + "）"
		}
	case !active:
		reason = "承运商 " + name + " 已停用"
	case qualExpiry != nil && qualExpiry.Before(today):
		reason = "承运商 " + name + " 承运资质已于 " + qualExpiry.Format("2006-01-02") + " 到期"
	}
	return name, reason, true
}

// BatchDispatch POST /api/v1/orders/batch-dispatch
func (h *Handler) BatchDispatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body batchReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.Err(w, http.StatusBadRequest, "VALIDATION", "请求体不是合法 JSON")
		return
	}
	if body.DispatchType == "" {
		body.DispatchType = "third_party"
	}
	if body.Allocation == "" {
		body.Allocation = "by_weight"
	}
	if body.DispatchType != "third_party" && body.DispatchType != "platform" {
		httpx.Err(w, http.StatusBadRequest, "INVALID_BATCH_DISPATCH_TYPE", "批次派单仅支持外包承运商 / 网货平台。")
		return
	}
	var carrierName string
	if body.DispatchType == "third_party" {
		if body.Carrier == "" {
			httpx.Err(w, http.StatusBadRequest, "CARRIER_REQUIRED", "外包批次需选择承运商。")
			return
		}
		name, blockReason, found := h.carrierBlockReason(ctx, body.Carrier)
		if !found {
			httpx.Err(w, http.StatusBadRequest, "CARRIER_REQUIRED", "外包批次需选择承运商。")
			return
		}
		if blockReason != "" {
			httpx.Err(w, http.StatusConflict, "CARRIER_BLOCKED", blockReason)
			return
		}
		carrierName = name
	} else if body.PlatformName == "" {
		httpx.Err(w, http.StatusBadRequest, "PLATFORM_REQUIRED", "网货批次需填写平台名称。")
		return
	}
	if len(body.IDs) == 0 {
		httpx.Err(w, http.StatusBadRequest, "IDS_REQUIRED", "请选择要批派的订单。")
		return
	}
	if len(body.IDs) > 200 {
		httpx.Err(w, http.StatusBadRequest, "BATCH_TOO_LARGE", "单个批次最多 200 单，请分批。")
		return
	}
	isChief, _ := h.isChiefDispatcher(ctx, me)
	totalPayable, _ := decimal.NewFromString(body.TotalPayable.String())

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 预筛：状态 + 归属（逐单行锁）
	dispatchable := []dispatchableOrder{}
	skipped := []map[string]any{}
	for _, id := range body.IDs {
		o := dispatchableOrder{}
		err := tx.QueryRow(ctx, `
			SELECT o.id::text, o.order_no, o.status, COALESCE(c.name,''), o.origin, o.destination, o.ai_conversation_id,
			       o.customer_id::text, o.claimed_by_id::text, o.assigned_to_id::text,
			       o.cargo_weight_ton, o.cargo_quantity, o.cargo_volume_cbm, o.cod_amount,
			       o.freight_term, o.freight_payer, o.expected_delivery_at, o.project_id::text
			FROM ops_order o LEFT JOIN md_customer c ON c.id=o.customer_id
			WHERE o.id=$1::uuid FOR UPDATE OF o`, id,
		).Scan(&o.ID, &o.OrderNo, &o.Status, &o.CustomerName, &o.Origin, &o.Destination, &o.AIConvID,
			&o.CustomerID, &o.ClaimedBy, &o.AssignedTo,
			&o.Weight, &o.Quantity, &o.VolumeCbm, &o.CodAmount,
			&o.FreightTerm, &o.FreightPayer, &o.ExpectedDeliveryAt, &o.ProjectID)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取订单失败")
			return
		}
		if o.Status != "pooled" && o.Status != "dispatching" && o.Status != "confirmed" {
			skipped = append(skipped, map[string]any{"order_no": o.OrderNo, "reason": "状态不可派单"})
			continue
		}
		if !isChief {
			mine := (o.ClaimedBy != nil && *o.ClaimedBy == me.ID) || (o.AssignedTo != nil && *o.AssignedTo == me.ID)
			if !mine {
				skipped = append(skipped, map[string]any{"order_no": o.OrderNo, "reason": "未分派/锁定给你"})
				continue
			}
		}
		dispatchable = append(dispatchable, o)
	}
	if len(dispatchable) == 0 {
		httpx.Err(w, http.StatusConflict, "NO_DISPATCHABLE", "所选订单均不可批派（状态或归属不满足）。")
		return
	}

	alloc := allocatePayable(totalPayable, dispatchable, body.Allocation, body.ManualPayables)

	batchNo, err := nextNoScoped(ctx, tx, "PC", "batch")
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "批次取号失败")
		return
	}
	bid, _ := uuid.NewV7()
	batchID := bid.String()
	var carrierIDArg *string
	if body.DispatchType == "third_party" {
		carrierIDArg = &body.Carrier
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_dispatch_batch (id, created_at, updated_at, batch_no, dispatch_type, carrier_id, platform_name,
		       status, allocation, total_payable, order_count, total_weight_ton, note, statement_no, created_by_id, organization_id)
		VALUES ($1, now(), now(), $2, $3, $4::uuid, $5, 'dispatched', $6, $7, 0, 0, $8, '', $9::uuid, NULL)`,
		batchID, batchNo, body.DispatchType, carrierIDArg, body.PlatformName,
		body.Allocation, totalPayable, body.Note, me.ID); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "批次创建失败")
		return
	}

	ok := []map[string]any{}
	failed := []map[string]any{}
	totalWeight := decimal.Zero
	for _, o := range dispatchable {
		// 每单一个 savepoint。这一层不是装饰：批派本来就要「一单出问题，其余照发」
		// （响应里的 ok/failed 就是这个语义），但 Postgres 事务里任何一句报错都会让
		// 整个事务进 aborted 态——不隔离的话，第一单出错会把后面每一单连同 COMMIT
		// 一起带走，界面上看到的是「整批失败」。
		sp, err := tx.Begin(ctx)
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
			return
		}
		wbNo, pay, err := dispatchOneInBatch(ctx, sp, o, body, batchID, batchNo, carrierName, carrierIDArg, alloc[o.ID], me.ID)
		if err != nil {
			_ = sp.Rollback(ctx)
			slog.Error("批派单单失败", "batch", batchNo, "order", o.OrderNo, "err", err)
			failed = append(failed, map[string]any{"order_no": o.OrderNo, "reason": err.Error()})
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			slog.Error("批派单单提交失败", "batch", batchNo, "order", o.OrderNo, "err", err)
			failed = append(failed, map[string]any{"order_no": o.OrderNo, "reason": err.Error()})
			continue
		}
		totalWeight = totalWeight.Add(o.Weight)
		ok = append(ok, map[string]any{
			"order_no": o.OrderNo, "waybill_no": wbNo,
			"payable": pay.InexactFloat64(), "customer": o.CustomerName,
		})
	}
	if len(ok) == 0 {
		httpx.Err(w, http.StatusConflict, "BATCH_DISPATCH_FAILED", "批次派单全部失败。")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ops_dispatch_batch SET order_count=$2, total_weight_ton=$3, updated_at=now() WHERE id=$1::uuid`,
		batchID, len(ok), totalWeight); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "批次汇总失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	display := carrierName
	if body.DispatchType == "platform" {
		display = body.PlatformName
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"batch_no": batchNo, "batch_id": batchID, "carrier": display,
		"dispatch_type": body.DispatchType, "allocation": body.Allocation,
		"total_payable": totalPayable.InexactFloat64(), "order_count": len(ok),
		"ok": ok, "failed": failed, "skipped": skipped,
	})
}

// dispatchOneInBatch 批次里单张订单转运单的全部写入，跑在自己的 savepoint 里。
// 返回运单号与该单分摊到的应付；任何一步出错都原样往上抛，由调用方回滚这一单。
func dispatchOneInBatch(ctx context.Context, tx pgx.Tx, o dispatchableOrder, body batchReq,
	batchID, batchNo, carrierName string, carrierIDArg *string,
	pay decimal.Decimal, meID string) (string, decimal.Decimal, error) {
	// 批次隐含锁定：待分配订单先锁给操作人
	if o.ClaimedBy == nil && o.AssignedTo == nil {
		if _, err := tx.Exec(ctx, `
			UPDATE ops_order SET claimed_by_id=$2::uuid, claimed_at=now(),
			       status=(CASE WHEN status='pooled' THEN 'dispatching' ELSE status END), updated_at=now()
			WHERE id=$1::uuid`, o.ID, meID); err != nil {
			return "", pay, fmt.Errorf("锁定订单失败：%w", err)
		}
		if err := txEvent(ctx, tx, o.ID, "claimed", "", "dispatching", meID, "batch",
			map[string]any{"note": "批次 " + batchNo + " 锁定"}); err != nil {
			return "", pay, fmt.Errorf("锁定事件落库失败：%w", err)
		}
	}
	wbNo, err := nextNoScoped(ctx, tx, "YD", "waybill")
	if err != nil {
		return "", pay, fmt.Errorf("运单取号失败：%w", err)
	}
	wid, _ := uuid.NewV7()
	codStatus := "none"
	if o.CodAmount.GreaterThan(decimal.Zero) {
		codStatus = "pending"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, dispatch_type, platform_name, platform_order_no,
		  order_id, customer_id, carrier_id, batch_id, route_name, ai_conversation_id, origin, destination,
		  status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
		  cargo_quantity, cargo_weight_ton, cargo_volume_cbm,
		  freight_term, freight_payer, cod_amount, cod_status, planned_arrival, project_id)
		VALUES ($1, now(), now(), $2, $3, $4, '', $5::uuid, $6::uuid, $7::uuid, $8::uuid, $9, $10, $11, $12,
		  'pending_dispatch', 'pending_accept', 'none', 'not_due', 0, $13, $14, $15, $16, $17, $18, $19, $20, $21::uuid)`,
		wid.String(), wbNo, body.DispatchType, body.PlatformName,
		o.ID, o.CustomerID, carrierIDArg, batchID,
		o.Origin+"→"+o.Destination, o.AIConvID, o.Origin, o.Destination,
		o.Quantity, o.Weight, o.VolumeCbm,
		o.FreightTerm, o.FreightPayer, o.CodAmount, codStatus, o.ExpectedDeliveryAt, o.ProjectID); err != nil {
		return "", pay, fmt.Errorf("运单写入失败：%w", err)
	}
	// 点位拷贝进执行层
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_waybill_stop (id, created_at, updated_at, waybill_id, seq, stop_type, city, address,
		       contact_name, contact_phone, lat, lng, radius_m, planned_eta, arrival_source, status, note)
		SELECT gen_random_uuid(), now(), now(), $2::uuid, seq, stop_type, city, address,
		       contact_name, contact_phone, NULL, NULL, 0, COALESCE(expected_end, expected_start), '', 'pending', cargo_note
		FROM ops_order_stop WHERE order_id=$1::uuid ORDER BY seq`, o.ID, wid.String()); err != nil {
		return "", pay, fmt.Errorf("点位拷贝失败：%w", err)
	}
	// 订单回写已派单
	if _, err := tx.Exec(ctx, "UPDATE ops_order SET status='converted', updated_at=now() WHERE id=$1::uuid", o.ID); err != nil {
		return "", pay, fmt.Errorf("订单状态回写失败：%w", err)
	}
	// 应付快照
	if pay.GreaterThan(decimal.Zero) {
		payeeType, payeeRef := "carrier", carrierName
		if body.DispatchType == "platform" {
			payeeType = "platform"
			payeeRef = body.PlatformName
			if payeeRef == "" {
				payeeRef = "网货平台"
			}
		}
		snapIn, _ := json.Marshal(map[string]any{
			"weight_ton": o.Weight.InexactFloat64(), "volume_cbm": o.VolumeCbm.InexactFloat64(),
			"quantity": o.Quantity, "route": o.Origin + "→" + o.Destination,
		})
		snapCalc, _ := json.Marshal(map[string]any{"agreed_payable": pay.InexactFloat64(), "note": "派单议定应付金额快照"})
		eid, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `
			INSERT INTO fin_expense_record (id, created_at, updated_at, waybill_id, direction, expense_item_code,
			  amount, currency, occurred_at, risk_status, source_system, external_id, payee_type, payee_ref,
			  remark, price_source, quote_id, pricing_rule_id, pricing_rule_name, charge_method, matched_condition,
			  input_snapshot, calculation_detail, rule_snapshot)
			VALUES ($1, now(), now(), $2::uuid, 'payable', 'freight', $3, 'CNY', now(), 'normal', '', '',
			  $4, $5, $6, 'batch', '', '', '', '', '', $7, $8, '{}'::jsonb)`,
			eid.String(), wid.String(), pay, payeeType, payeeRef, "批次 "+batchNo+" 分摊应付", snapIn, snapCalc); err != nil {
			// 应付快照落不下就整单退回：运单发出去了却没有成本，对账那头会凭空少一笔
			return "", pay, fmt.Errorf("应付快照落库失败：%w", err)
		}
	}
	// 事件：订单 + 运单
	if err := txEvent(ctx, tx, o.ID, "dispatched", "", "converted", meID, "dispatch",
		map[string]any{"waybill_no": wbNo, "dispatch_type": body.DispatchType}); err != nil {
		return "", pay, fmt.Errorf("订单派单事件落库失败：%w", err)
	}
	wevID, _ := uuid.NewV7()
	res := carrierName
	if body.DispatchType == "platform" {
		res = body.PlatformName
	}
	wp, _ := json.Marshal(map[string]any{
		"dispatch_type": body.DispatchType, "dispatch_status": "pending_accept",
		"price_source": "batch", "agreed_payable": pay.InexactFloat64(), "quote_id": "",
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type, event_time, source, resource, payload)
		VALUES ($1, now(), now(), $2::uuid, 'dispatched', now(), 'dispatch', $3, $4)`,
		wevID.String(), wid.String(), res, wp); err != nil {
		return "", pay, fmt.Errorf("运单派单事件落库失败：%w", err)
	}
	return wbNo, pay, nil
}

// nextNoScoped 与 nextNo 同机制，scope 名可指定（batch/waybill 等）
func nextNoScoped(ctx context.Context, tx pgx.Tx, prefix, scopeName string) (string, error) {
	day := time.Now().In(cstZone).Format("20060102")
	var v int
	err := tx.QueryRow(ctx, `
		INSERT INTO ops_number_counter (scope, value) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET value = ops_number_counter.value + 1
		RETURNING value`, scopeName+":"+day).Scan(&v)
	if err != nil {
		return "", err
	}
	return prefix + day + padSeq(v), nil
}

func padSeq(v int) string {
	s := ""
	for n := 100000; n >= 1; n /= 10 {
		s += string(rune('0' + (v/n)%10))
	}
	return s
}
