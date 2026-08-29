package waybills

// 运单剩余写动作：
//   POST /waybills/{no}/dispatch          派车受理（可选带状态推进）
//   GET·POST /waybills/{no}/events        事件台账 / 手工补事件
//   POST /waybills/{no}/add-expense       手工加费用明细
//   POST /waybills/{no}/contract/send     发合同给司机
//   POST /waybills/{no}/contract/confirm  司机确认/拒签
//   POST /waybills/{no}/partial-sign      部分签收（货损货差）
//   POST /waybills/{no}/reject            整车拒收
//   POST /waybills/{no}/collect-cod       司机确认代收
//   POST /waybills/{no}/remit-cod         财务确认回款
//   POST /waybills/{no}/split             按货量拆单
//   POST /waybills/merge                  合单
//
// 对齐 apps/ops/services.{partial_sign_waybill, reject_waybill, collect_cod,
// remit_cod, split_waybill, merge_waybills} 与 contracts.{send_contract, confirm_contract}。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/contracts"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

// splittableFrom 只有还没进入执行的运单可拆/可合
var splittableFrom = map[string]bool{"draft": true, "pending_dispatch": true}

// signableFrom 签收/拒收的合法起点
func signableFrom(status string) bool {
	return status == "in_transit" || status == "arrived" || status == "partially_signed"
}

func alreadySigned(status string) bool {
	return status == "signed" || status == "delivered" || status == "settled"
}

// Dispatch POST /api/v1/waybills/{no}/dispatch {dispatch_status, status}
//
// 受理状态直写，目标状态一律走状态机——绕开流转校验直改 status，是里程碑与
// 事件链后来对不上的根因。
func (h *Handler) Dispatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := h.Svc.UserByID(ctx, auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		DispatchStatus *string `json:"dispatch_status"`
		Status         string  `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ds := "accepted"
	if body.DispatchStatus != nil {
		ds = *body.DispatchStatus
	}
	no := chi.URLParam(r, "no")
	err := h.inTx(ctx, func(tx pgx.Tx) error {
		wb, err := lockWaybill(ctx, tx, no)
		if err != nil || wb == nil {
			return httpErr{http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在。"}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE ops_waybill SET dispatch_status=$2, updated_at=now() WHERE id=$1::uuid`, wb.ID, ds); err != nil {
			return err
		}
		if body.Status != "" && body.Status != wb.Status {
			if !canGo(wb.Status, body.Status) {
				return httpErr{http.StatusConflict, "INVALID_TRANSITION",
					fmt.Sprintf("不允许从 %s 流转到 %s。合法：%v", wb.Status, body.Status, allowedTransitions[wb.Status])}
			}
			if code, appCode, msg := doTransition(ctx, tx, wb, body.Status, "派车受理"); appCode != "" {
				return httpErr{code, appCode, msg}
			}
		}
		return nil
	})
	if !h.wrote(w, err) {
		return
	}
	h.respondWaybill(w, r, no)
}

// httpErr 让事务闭包能把业务错误码带回外层
type httpErr struct {
	Status  int
	Code    string
	Message string
}

func (e httpErr) Error() string { return e.Message }

func (h *Handler) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// wrote 统一把事务错误翻成响应；返回 true 表示没出错、可以继续写成功响应
func (h *Handler) wrote(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	if he, ok := err.(httpErr); ok {
		httpx.Err(w, he.Status, he.Code, he.Message)
		return false
	}
	httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "处理失败："+err.Error())
	return false
}

// respondWaybill 回一份运单列表列面（WaybillSerializer 口径）
func (h *Handler) respondWaybill(w http.ResponseWriter, r *http.Request, no string) {
	it, err := SerializeByNo(r.Context(), h.DB, no)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// Events GET·POST /api/v1/waybills/{no}/events
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, no, ok := h.resolve(w, r, "waybill.view")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := h.DB.Query(ctx, eventSelect+" WHERE e.waybill_id=$1::uuid ORDER BY e.event_time, e.id", id)
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取事件失败")
			return
		}
		httpx.JSON(w, http.StatusOK, scanEvents(rows))
		return
	}
	var body struct {
		EventType string         `json:"event_type"`
		EventTime string         `json:"event_time"`
		Resource  *string        `json:"resource"`
		Source    *string        `json:"source"`
		Payload   map[string]any `json:"payload"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.EventType == "" {
		body.EventType = "manual_event"
	}
	resource := no
	if body.Resource != nil {
		resource = *body.Resource
	}
	source := "api"
	if body.Source != nil {
		source = *body.Source
	}
	if body.Payload == nil {
		body.Payload = map[string]any{}
	}
	pj, _ := json.Marshal(body.Payload)
	eid, _ := uuid.NewV7()
	// 非法/缺失时间回落 now()（对齐 parse_datetime(...) or timezone.now()）
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type, event_time,
		  source, resource, payload)
		VALUES ($1, now(), now(), $2::uuid, $3, COALESCE($4::timestamptz, now()), $5, $6, $7)`,
		eid.String(), id, body.EventType, nilIfBadTime(body.EventTime), source, resource, pj); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败："+err.Error())
		return
	}
	rows, err := h.DB.Query(ctx, eventSelect+" WHERE e.id=$1::uuid", eid.String())
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	list := scanEvents(rows)
	if len(list) == 0 {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, list[0])
}

const eventSelect = `
SELECT e.id::text, e.event_type, e.event_time, e.source, e.resource, e.payload
FROM ops_waybill_event e`

func scanEvents(rows pgx.Rows) []map[string]any {
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, et, source, resource string
		var at time.Time
		var payload map[string]any
		if rows.Scan(&id, &et, &at, &source, &resource, &payload) != nil {
			break
		}
		out = append(out, map[string]any{
			"id": id, "event_type": et, "event_time": httpx.Micros(at),
			"source": source, "resource": resource, "payload": payload,
		})
	}
	return out
}

func nilIfBadTime(s string) any {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if _, err := time.Parse(layout, s); err == nil {
			return s
		}
	}
	return nil
}

// AddExpense POST /api/v1/waybills/{no}/add-expense
func (h *Handler) AddExpense(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _, ok := h.resolve(w, r, "waybill.manage")
	if !ok {
		return
	}
	var body struct {
		Direction       string `json:"direction"`
		ExpenseItemCode string `json:"expense_item_code"`
		Amount          any    `json:"amount"`
		PayeeType       string `json:"payee_type"`
		PayeeRef        string `json:"payee_ref"`
		Remark          string `json:"remark"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Direction != "receivable" && body.Direction != "payable" {
		httpx.Err(w, http.StatusBadRequest, "INVALID_DIRECTION", "direction 取值 receivable|payable。")
		return
	}
	// 科目白名单取 cost_items 的静态目录（与 /waybills/cost-catalog 同一份），
	// 不是 fin_expense_item 表——后者是可配置的价目项，两者不是一回事
	_, isCost := costItems[body.ExpenseItemCode]
	_, isIncome := incomeItems[body.ExpenseItemCode]
	if !isCost && !isIncome {
		httpx.Err(w, http.StatusBadRequest, "INVALID_EXPENSE_ITEM", "费用科目非法。")
		return
	}
	amt, err := toDecimal(body.Amount)
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, "INVALID_AMOUNT", "金额非法。")
		return
	}
	eid, _ := uuid.NewV7()
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO fin_expense_record (id, created_at, updated_at, waybill_id, direction,
		  expense_item_code, amount, currency, risk_status, source_system, external_id,
		  payee_type, payee_ref, remark, charge_method, matched_condition, price_source,
		  pricing_rule_id, pricing_rule_name, quote_id,
		  calculation_detail, input_snapshot, rule_snapshot)
		VALUES ($1, now(), now(), $2::uuid, $3, $4, $5::numeric, 'CNY', 'normal', 'manual', '',
		  $6, $7, $8, '', '', '', '', '', '', '{}'::jsonb, '{}'::jsonb, '{}'::jsonb)`,
		eid.String(), id, body.Direction, body.ExpenseItemCode, amt.String(),
		body.PayeeType, body.PayeeRef, body.Remark); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败："+err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func toDecimal(v any) (decimal.Decimal, error) {
	switch x := v.(type) {
	case nil:
		return decimal.Zero, nil
	case float64:
		return decimal.NewFromFloat(x), nil
	case string:
		if strings.TrimSpace(x) == "" {
			return decimal.Zero, nil
		}
		return decimal.NewFromString(x)
	}
	return decimal.Zero, fmt.Errorf("bad decimal")
}

// latestContract 取该运单最新一份合同（Contract.Meta.ordering = -created_at）
func (h *Handler) latestContract(ctx context.Context, waybillID string) (id, status, driverID string, ok bool) {
	var drv *string
	err := h.DB.QueryRow(ctx, `
		SELECT id::text, confirm_status, driver_id::text FROM ops_contract
		WHERE waybill_id=$1::uuid ORDER BY created_at DESC, id DESC LIMIT 1`, waybillID).
		Scan(&id, &status, &drv)
	if err != nil {
		return "", "", "", false
	}
	if drv != nil {
		driverID = *drv
	}
	return id, status, driverID, true
}

// Patch PATCH /api/v1/waybills/{no} —— 只开放回单状态一个字段。
//
// 运单列表上的批量「标记回单已回收」就是打这里。此前这条路径只注册了 GET，
// 前端每一条都吃 405，而失败被 catch 吞掉，最后照样弹一个**成功**提示
// 「已标记 0/5 条运单回单为「已回收」」——绿色对勾、语气笃定，什么都没发生。
// 回单是回单付结算的前提，操作员以为标完了，财务那边却一条都催不动。
//
// 字段白名单而不是通用 PATCH：运单上挂着状态机、司机车辆、金额，
// 开一个什么都能改的 PATCH 等于绕开状态机和所有业务校验。
// 批量标回单这一个需求，不值这个代价。
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _, ok := h.resolve(w, r, "waybill.manage")
	if !ok {
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.Err(w, http.StatusBadRequest, "INVALID_BODY", "请求体不是合法 JSON。")
		return
	}
	// 多传一个字段就整体拒绝，不做"忽略未知字段"。
	// 静默忽略会让调用方以为改成功了——这正是这一条要修的那个毛病。
	for k := range body {
		if k != "receipt_status" {
			httpx.Err(w, http.StatusBadRequest, "FIELD_NOT_PATCHABLE",
				"该接口只支持修改 receipt_status，不接受字段："+k)
			return
		}
	}
	rs, _ := body["receipt_status"].(string)
	if !wbstatus.ValidReceipt(rs) {
		httpx.Err(w, http.StatusBadRequest, "INVALID_RECEIPT_STATUS",
			"回单状态取值非法："+rs)
		return
	}
	if _, err := h.DB.Exec(ctx,
		`UPDATE ops_waybill SET receipt_status=$2, updated_at=now() WHERE id=$1::uuid`,
		id, rs); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败："+err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"id": id, "receipt_status": rs, "receipt_status_label": wbstatus.ReceiptLabelOf(rs),
	})
}

// ContractGenerate POST /api/v1/waybills/{no}/contract —— 按需生成承运合同。
//
// 派单时带司机会自动生成一份，但派单时**没有**司机的运单（后补司机是常规操作）
// 就永远拿不到合同——「发送给司机」「司机确认」两步也跟着走不到，
// 工作流面板上「承运合同 未生成」会一直挂着。
// 前端早就写好了 genContract/sendContract/confirmContract 三个 mutation
// （genContract 正是 POST 到这个地址），但它们一个都没有被渲染出来，
// 而后端这条路由此前也只注册了 GET——两边各缺一半，
// 于是「承运合同」这一整段功能从界面上完全够不着。
//
// 正文模板与派单那条路共用 internal/contracts.Generate ——
// 出事时双方拿的是同一份东西，不能因为入口不同而条款不同。
func (h *Handler) ContractGenerate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, no, ok := h.resolve(w, r, "waybill.manage")
	if !ok {
		return
	}
	// 已确认的那份是双方已经达成的约定，不能一键覆盖——
	// 否则就有了「先让司机签，再改运费」这条路。
	if _, status, _, found := h.latestContract(ctx, id); found && status == "confirmed" {
		httpx.Err(w, http.StatusConflict, "CONTRACT_CONFIRMED",
			"该运单的合同已由司机确认，不能重新生成。如需变更请走变更流程。")
		return
	}

	var in contracts.Input
	// 这些列都可能是 NULL（司机未指派、没挂车、没有费用记录），
	// 一律扫进指针再取值——直接扫进 string 会让整条查询报错，
	// 而报出来的是"读取运单失败"，看不出真正缺的是哪一样。
	var driverID, driverName, plate, trailer *string
	var weight, quantity, agreed *string
	err := h.DB.QueryRow(ctx, `
		SELECT wb.id::text, wb.waybill_no, wb.driver_id::text, d.name, v.plate_no,
		       wb.trailer_id::text, COALESCE(wb.origin,'')||'→'||COALESCE(wb.destination,''),
		       COALESCE(o.cargo_desc,''), wb.cargo_weight_ton::text, wb.cargo_quantity::text,
		       (SELECT sum(c.amount)::text FROM fin_expense_record c
		         WHERE c.waybill_id = wb.id AND c.direction='payable')
		FROM ops_waybill wb
		LEFT JOIN md_driver d ON d.id = wb.driver_id
		LEFT JOIN md_vehicle v ON v.id = wb.vehicle_id
		LEFT JOIN ops_order o ON o.id = wb.order_id
		WHERE wb.id=$1::uuid`, id).
		Scan(&in.WaybillID, &in.WaybillNo, &driverID, &driverName, &plate,
			&trailer, &in.Route, &in.CargoDesc, &weight, &quantity, &agreed)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取运单失败："+err.Error())
		return
	}
	in.DriverID = deref(driverID)
	if in.DriverID == "" {
		// 承运人空着的合同不是合同。宁可不生成，也不出一份签不了的。
		httpx.Err(w, http.StatusBadRequest, "NO_DRIVER",
			"该运单还没有指派司机，无法生成承运合同。请先指派司机。")
		return
	}
	in.DriverName = deref(driverName)
	in.VehiclePlate = deref(plate)
	in.TrailerID = trailer
	in.Weight = decFrom(weight)
	in.Quantity = intFrom(quantity)
	if agreed != nil {
		d := decFrom(agreed)
		in.Agreed = &d
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := contracts.Generate(ctx, tx, in); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "合同生成失败："+err.Error())
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	cid, _, _, found := h.latestContract(ctx, id)
	if !found {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	_ = no
	var out json.RawMessage
	if err := h.DB.QueryRow(ctx, contractJSON+" WHERE c.id=$1::uuid", cid).Scan(&out); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func decFrom(s *string) decimal.Decimal {
	if s == nil {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(*s)
	if err != nil {
		return decimal.Zero
	}
	return d
}

func intFrom(s *string) int {
	if s == nil {
		return 0
	}
	d, err := decimal.NewFromString(*s)
	if err != nil {
		return 0
	}
	return int(d.IntPart())
}

// ContractSend POST /api/v1/waybills/{no}/contract/send
func (h *Handler) ContractSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _, ok := h.resolve(w, r, "waybill.manage")
	if !ok {
		return
	}
	cid, status, _, found := h.latestContract(ctx, id)
	if !found {
		httpx.Err(w, http.StatusNotFound, "NO_CONTRACT", "请先生成合同。")
		return
	}
	if status == "confirmed" {
		httpx.Err(w, http.StatusConflict, "CONTRACT_CONFIRMED", "合同已确认，无需重复发送。")
		return
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE ops_contract SET sent_at=now(), confirm_status='sent', updated_at=now()
		WHERE id=$1::uuid`, cid); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败："+err.Error())
		return
	}
	h.contractRecord(ctx, id, "contract_sent", cid, "")
	h.respondContract(w, r, cid)
}

// ContractConfirm POST /api/v1/waybills/{no}/contract/confirm {accepted, reply}
func (h *Handler) ContractConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _, ok := h.resolve(w, r, "waybill.manage")
	if !ok {
		return
	}
	var body struct {
		Accepted *bool  `json:"accepted"`
		Reply    string `json:"reply"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	accepted := true
	if body.Accepted != nil {
		accepted = *body.Accepted
	}
	cid, _, driverID, found := h.latestContract(ctx, id)
	if !found {
		httpx.Err(w, http.StatusNotFound, "NO_CONTRACT", "无可确认的合同。")
		return
	}
	newStatus, eventType := "confirmed", "contract_confirmed"
	if !accepted {
		newStatus, eventType = "rejected", "contract_rejected"
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE ops_contract SET confirm_status=$2, confirmed_at=now(), driver_reply=$3, updated_at=now()
		WHERE id=$1::uuid`, cid, newStatus, body.Reply); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败："+err.Error())
		return
	}
	h.contractRecord(ctx, id, eventType, cid, body.Reply)
	// 合同确认 → 司机入驻完成 + 刷新累计（工作流编排的一环）
	if accepted && driverID != "" {
		_, _ = h.DB.Exec(ctx, `
			UPDATE md_driver SET app_registered=true, app_registered_at=now(), updated_at=now()
			WHERE id=$1::uuid AND NOT app_registered`, driverID)
		_, _ = h.DB.Exec(ctx, `
			UPDATE md_driver d SET
			  cumulative_waybills = (SELECT count(*) FROM ops_waybill x
			     WHERE x.driver_id=d.id AND x.status IN ('signed','delivered','settled')),
			  cumulative_freight = COALESCE((SELECT sum(e.amount) FROM fin_expense_record e
			     JOIN ops_waybill x ON x.id=e.waybill_id
			     WHERE x.driver_id=d.id AND e.direction='payable'), 0),
			  updated_at = now()
			WHERE d.id=$1::uuid`, driverID)
	}
	h.respondContract(w, r, cid)
}

// contractRecord 合同动作的运单事件（对齐 contracts._record）
func (h *Handler) contractRecord(ctx context.Context, waybillID, eventType, contractID, reply string) {
	var no string
	_ = h.DB.QueryRow(ctx, `SELECT contract_no FROM ops_contract WHERE id=$1::uuid`, contractID).Scan(&no)
	payload := map[string]any{"contract_no": no}
	if eventType != "contract_sent" {
		payload["reply"] = reply
	}
	pj, _ := json.Marshal(payload)
	eid, _ := uuid.NewV7()
	_, _ = h.DB.Exec(ctx, `
		INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type, event_time,
		  source, resource, payload)
		SELECT $1, now(), now(), $2::uuid, $3, clock_timestamp(), 'contract', w.waybill_no, $4
		FROM ops_waybill w WHERE w.id = $2::uuid`,
		eid.String(), waybillID, eventType, pj)
}

func (h *Handler) respondContract(w http.ResponseWriter, r *http.Request, contractID string) {
	var out json.RawMessage
	err := h.DB.QueryRow(r.Context(), contractJSON+" WHERE c.id=$1::uuid", contractID).Scan(&out)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// contractJSON 与 GET /waybills/{no}/contract 同一份列面（ContractSerializer）
const contractJSON = `
SELECT json_build_object(
  'id', c.id::text, 'contract_no', c.contract_no, 'waybill', c.waybill_id::text,
  'driver', c.driver_id::text, 'driver_name', COALESCE(d.name,''),
  'template_code', c.template_code, 'content', c.content, 'sent_at', c.sent_at,
  'driver_reply', c.driver_reply, 'confirm_status', c.confirm_status,
  'status_label', (CASE c.confirm_status WHEN 'pending' THEN '待发送' WHEN 'sent' THEN '已发送'
                   WHEN 'confirmed' THEN '已确认' WHEN 'rejected' THEN '已拒签' ELSE c.confirm_status END),
  'confirmed_at', c.confirmed_at, 'pdf_url', (CASE WHEN c.pdf <> '' THEN '/media/' || c.pdf ELSE '' END), 'created_at', c.created_at)
FROM ops_contract c LEFT JOIN md_driver d ON d.id=c.driver_id`

// PartialSign POST /api/v1/waybills/{no}/partial-sign
func (h *Handler) PartialSign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		TotalQuantity    any    `json:"total_quantity"`
		SignedQuantity   any    `json:"signed_quantity"`
		DamagedQuantity  any    `json:"damaged_quantity"`
		ShortageQuantity any    `json:"shortage_quantity"`
		Signatory        string `json:"signatory"`
		Signature        string `json:"signature"`
		FileURL          string `json:"file_url"`
		SignSource       string `json:"sign_source"`
		Note             string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.SignSource == "" {
		body.SignSource = "driver"
	}
	total, _ := toDecimal(body.TotalQuantity)
	signed, _ := toDecimal(body.SignedQuantity)
	damaged, _ := toDecimal(body.DamagedQuantity)
	shortage, _ := toDecimal(body.ShortageQuantity)

	no := chi.URLParam(r, "no")
	var receiptID string
	var finalStatus string
	err = h.inTx(ctx, func(tx pgx.Tx) error {
		wb, err := lockWaybill(ctx, tx, no)
		if err != nil || wb == nil {
			return httpErr{http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在。"}
		}
		if alreadySigned(wb.Status) {
			return httpErr{http.StatusConflict, "ALREADY_SIGNED", "运单已签收。"}
		}
		if !signableFrom(wb.Status) {
			return httpErr{http.StatusConflict, "NOT_SIGNABLE", "仅在途/已到达运单可签收。"}
		}
		if signed.GreaterThan(total) {
			return httpErr{http.StatusBadRequest, "QTY_INVALID", "实收数量不能大于应收数量。"}
		}
		if signed.GreaterThanOrEqual(total) && damaged.IsZero() && shortage.IsZero() {
			return httpErr{http.StatusBadRequest, "NOT_PARTIAL", "无短少/货损，应走整签。"}
		}
		rid, _ := uuid.NewV7()
		receiptID = rid.String()
		if _, err := tx.Exec(ctx, `
			INSERT INTO ops_receipt (id, created_at, updated_at, waybill_id, receipt_type, status, file_url,
			  ocr_status, ocr_result, signatory, signed_at, signature, sign_source, outcome,
			  total_quantity, signed_quantity, damaged_quantity, shortage_quantity, rejection_reason, uploaded_by_id)
			VALUES ($1, now(), now(), $2::uuid, 'signed_pod', 'confirmed', $3,
			  'pending', '{}'::jsonb, $4, now(), $5, $6, 'partial',
			  $7::numeric, $8::numeric, $9::numeric, $10::numeric, $11, $12::uuid)`,
			receiptID, wb.ID, body.FileURL, body.Signatory, body.Signature, body.SignSource,
			total.String(), signed.String(), damaged.String(), shortage.String(), body.Note, me.ID); err != nil {
			return err
		}
		if wb.Status == "in_transit" {
			if code, appCode, msg := doTransition(ctx, tx, wb, "arrived", "签收回传自动到达"); appCode != "" {
				return httpErr{code, appCode, msg}
			}
		}
		remark := fmt.Sprintf("部分签收 实收%s/%s 货损%s 短少%s",
			pyNum(signed), pyNum(total), pyNum(damaged), pyNum(shortage))
		if code, appCode, msg := doTransition(ctx, tx, wb, "partially_signed", remark); appCode != "" {
			return httpErr{code, appCode, msg}
		}
		finalStatus = wb.Status
		if _, err := tx.Exec(ctx,
			`UPDATE ops_waybill SET receipt_status='`+wbstatus.ReceiptReturned+`', updated_at=now() WHERE id=$1::uuid`, wb.ID); err != nil {
			return err
		}
		level := "medium"
		if total.IsPositive() && damaged.Add(shortage).GreaterThan(total.Div(decimal.NewFromInt(2))) {
			level = "high"
		} else if !total.IsPositive() && damaged.Add(shortage).IsPositive() {
			level = "high" // total=0 时 Django 的比较基准是 0，任何货损都算高
		}
		desc := strings.TrimSpace(fmt.Sprintf("部分签收：应收%s 实收%s 货损%s 短少%s。%s",
			pyNum(total), pyNum(signed), pyNum(damaged), pyNum(shortage), body.Note))
		return openDeliveryException(ctx, tx, wb.ID, "cargo_damage", level, desc, me.ID)
	})
	if !h.wrote(w, err) {
		return
	}
	h.respondSignOutcome(w, r, no, finalStatus, receiptID)
}

// pyNum 复刻 Python Decimal 在 f-string 里的呈现（保留入参刻度）
func pyNum(d decimal.Decimal) string { return d.String() }

// Reject POST /api/v1/waybills/{no}/reject {reason, signatory, sign_source}
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Reason     string `json:"reason"`
		Signatory  string `json:"signatory"`
		SignSource string `json:"sign_source"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.SignSource == "" {
		body.SignSource = "driver"
	}
	no := chi.URLParam(r, "no")
	var receiptID, finalStatus string
	err = h.inTx(ctx, func(tx pgx.Tx) error {
		wb, err := lockWaybill(ctx, tx, no)
		if err != nil || wb == nil {
			return httpErr{http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在。"}
		}
		if alreadySigned(wb.Status) {
			return httpErr{http.StatusConflict, "ALREADY_SIGNED", "运单已签收，无法拒收。"}
		}
		if !signableFrom(wb.Status) {
			return httpErr{http.StatusConflict, "NOT_REJECTABLE", "仅在途/已到达运单可拒收。"}
		}
		if strings.TrimSpace(body.Reason) == "" {
			return httpErr{http.StatusBadRequest, "REASON_REQUIRED", "拒收必须填写原因。"}
		}
		rid, _ := uuid.NewV7()
		receiptID = rid.String()
		if _, err := tx.Exec(ctx, `
			INSERT INTO ops_receipt (id, created_at, updated_at, waybill_id, receipt_type, status, file_url,
			  ocr_status, ocr_result, signatory, signed_at, signature, sign_source, outcome,
			  total_quantity, signed_quantity, damaged_quantity, shortage_quantity, rejection_reason, uploaded_by_id)
			VALUES ($1, now(), now(), $2::uuid, 'rejection', 'rejected', '',
			  'pending', '{}'::jsonb, $3, now(), '', $4, 'rejected', 0, 0, 0, 0, $5, $6::uuid)`,
			receiptID, wb.ID, body.Signatory, body.SignSource, body.Reason, me.ID); err != nil {
			return err
		}
		if wb.Status == "in_transit" {
			if code, appCode, msg := doTransition(ctx, tx, wb, "arrived", "签收回传自动到达"); appCode != "" {
				return httpErr{code, appCode, msg}
			}
		}
		if code, appCode, msg := doTransition(ctx, tx, wb, "rejected", "拒收："+body.Reason); appCode != "" {
			return httpErr{code, appCode, msg}
		}
		finalStatus = wb.Status
		if _, err := tx.Exec(ctx,
			`UPDATE ops_waybill SET receipt_status='`+wbstatus.ReceiptReturned+`', updated_at=now() WHERE id=$1::uuid`, wb.ID); err != nil {
			return err
		}
		return openDeliveryException(ctx, tx, wb.ID, "customer_complaint", "high", "整车拒收："+body.Reason, me.ID)
	})
	if !h.wrote(w, err) {
		return
	}
	h.respondSignOutcome(w, r, no, finalStatus, receiptID)
}

// respondSignOutcome partial-sign / reject 的统一回包：{waybill_no, status, receipt}
func (h *Handler) respondSignOutcome(w http.ResponseWriter, r *http.Request, no, status, receiptID string) {
	var receipt json.RawMessage
	if err := h.DB.QueryRow(r.Context(), receiptJSON, receiptID).Scan(&receipt); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回单回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"waybill_no": no, "status": status, "receipt": receipt,
	})
}

// receiptJSON 与 ReceiptSerializer 逐字段对齐（$1 = 回单主键）
const receiptJSON = `
SELECT json_build_object(
  'id', rc.id::text, 'waybill', rc.waybill_id::text, 'waybill_no', COALESCE(w.waybill_no,''),
  'receipt_type', rc.receipt_type, 'status', rc.status,
  'file', NULLIF(rc.file,''),
  'file_display', (CASE WHEN rc.file <> '' THEN '/media/' || rc.file ELSE rc.file_url END),
  'file_url', rc.file_url, 'ocr_status', rc.ocr_status, 'ocr_result', rc.ocr_result,
  'signatory', rc.signatory, 'signed_at', rc.signed_at, 'created_at', rc.created_at,
  'outcome', rc.outcome,
  'total_quantity', rc.total_quantity::text, 'signed_quantity', rc.signed_quantity::text,
  'damaged_quantity', rc.damaged_quantity::text, 'shortage_quantity', rc.shortage_quantity::text,
  'rejection_reason', rc.rejection_reason)
FROM ops_receipt rc LEFT JOIN ops_waybill w ON w.id = rc.waybill_id
WHERE rc.id = $1::uuid`

// openDeliveryException 签收异常自动立案（对齐 services._open_delivery_exception）
func openDeliveryException(ctx context.Context, tx pgx.Tx, waybillID, excType, level, desc, actorID string) error {
	xid, _ := uuid.NewV7()
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_exception (id, created_at, updated_at, waybill_id, order_id, reported_by_id,
		  exception_type, level, source, description, status, responsibility_party, amount, resolution)
		VALUES ($1, now(), now(), $2::uuid, NULL, NULL, $3, $4, 'ops', $5, 'pending_handle', '', 0, '')`,
		xid.String(), waybillID, excType, level, desc); err != nil {
		return err
	}
	pj, _ := json.Marshal(map[string]any{"source": "ops"})
	eid, _ := uuid.NewV7()
	_, err := tx.Exec(ctx, `
		INSERT INTO ops_exception_event (id, created_at, updated_at, exception_id, event_type,
		  from_status, to_status, actor_id, note, payload, event_time)
		VALUES ($1, now(), now(), $2::uuid, 'create', '', 'pending_handle', $3::uuid, $4, $5, clock_timestamp())`,
		eid.String(), xid.String(), nilIfEmpty(actorID), desc, pj)
	return err
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// codAction collect/remit 共用：改状态 + 落时间戳 + 事件
func (h *Handler) codAction(w http.ResponseWriter, r *http.Request, remit bool) {
	ctx := r.Context()
	if _, err := h.Svc.UserByID(ctx, auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	no := chi.URLParam(r, "no")
	err := h.inTx(ctx, func(tx pgx.Tx) error {
		var id, codStatus, amount string
		err := tx.QueryRow(ctx, `SELECT id::text, cod_status, cod_amount::text
			FROM ops_waybill WHERE waybill_no=$1 FOR UPDATE`, no).Scan(&id, &codStatus, &amount)
		if err != nil {
			return httpErr{http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在。"}
		}
		amt, _ := decimal.NewFromString(amount)
		if remit {
			// 已回款和还没代收是两件事，报错不能都说成"仅已代收的货款可回款"——
			// 那句话会让操作员以为漏了代收那一步，于是回头去点「已代收」，
			// 而钱其实早就回过了。回款不做幂等（重复回款是真金白银），
			// 但至少要把话说清楚。
			if codStatus == "remitted" {
				return httpErr{http.StatusConflict, "COD_ALREADY_REMITTED",
					"该运单的代收货款已经回款过了，无需重复操作。"}
			}
			if codStatus != "collected" {
				return httpErr{http.StatusConflict, "COD_NOT_COLLECTED",
					"该运单还没有确认代收，请先确认收到货款再回款。"}
			}
			if _, err := tx.Exec(ctx, `UPDATE ops_waybill SET cod_status='remitted',
				cod_remitted_at=now(), updated_at=now() WHERE id=$1::uuid`, id); err != nil {
				return err
			}
			wbEvent(ctx, tx, id, "cod_remitted", no,
				map[string]any{"amount": amount, "__source": "settlement"})
			return nil
		}
		if !amt.IsPositive() {
			return httpErr{http.StatusConflict, "NO_COD", "该运单无代收货款。"}
		}
		if codStatus == "remitted" {
			return httpErr{http.StatusConflict, "COD_REMITTED", "代收货款已回款，不能重复代收。"}
		}
		// 已经代收过就什么都不做。
		//
		// 原先这里只挡了"已回款"，于是再点一次「已代收」会重写 cod_collected_at：
		// 司机 10:00 收的现金，下午有人手滑再点一次，记录变成 15:00——
		// 而这条时间戳正是现金纠纷时唯一能拿出来的东西。顺带时间线上还会多出
		// 一条「已代收」，看起来像收了两次。
		// 不报错而是直接返回当前状态：重复提交多半是网络重试或手滑，
		// 报一个错只会让人以为出了问题，幂等地什么都不做才是对的。
		if codStatus == "collected" {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE ops_waybill SET cod_status='collected',
			cod_collected_at=now(), updated_at=now() WHERE id=$1::uuid`, id); err != nil {
			return err
		}
		wbEvent(ctx, tx, id, "cod_collected", no,
			map[string]any{"amount": amount, "__source": "settlement"})
		return nil
	})
	if !h.wrote(w, err) {
		return
	}
	h.Detail(w, r) // 两个动作都回 WaybillDetailSerializer
}

// CollectCOD POST /api/v1/waybills/{no}/collect-cod
func (h *Handler) CollectCOD(w http.ResponseWriter, r *http.Request) { h.codAction(w, r, false) }

// RemitCOD POST /api/v1/waybills/{no}/remit-cod
func (h *Handler) RemitCOD(w http.ResponseWriter, r *http.Request) { h.codAction(w, r, true) }

// waybillCopyCols split/merge 复制的表头列（对齐 services._copy_fields）
const waybillCopyCols = `order_id, customer_id, carrier_id, route_name, planned_route_id, origin, destination, organization_id`

// waybillSpawnDefaults 拆/合出的新单要显式补上模型层默认值：Django 的
// objects.create() 由模型填这些列，原生 INSERT 不走模型层就得自己给
const waybillSpawnDefaults = `dispatch_status, risk_level, receipt_status, eta_drift_minutes,
  dispatch_type, ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term,
  platform_name, platform_order_no`
const waybillSpawnDefaultVals = `'pending_accept', 'none', 'not_due', 0,
  '', '', 0, 'none', 'shipper', 'prepaid', '', ''`

// Split POST /api/v1/waybills/{no}/split {splits:[{cargo_quantity, cargo_weight_ton, cargo_volume_cbm}]}
func (h *Handler) Split(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := h.Svc.UserByID(ctx, auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Splits []map[string]any `json:"splits"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	no := chi.URLParam(r, "no")
	children := []string{}
	err := h.inTx(ctx, func(tx pgx.Tx) error {
		wb, err := lockWaybill(ctx, tx, no)
		if err != nil || wb == nil {
			return httpErr{http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在。"}
		}
		if !splittableFrom[wb.Status] {
			return httpErr{http.StatusConflict, "INVALID_SPLIT", "仅待调度前的运单可拆单。"}
		}
		if len(body.Splits) < 2 {
			return httpErr{http.StatusBadRequest, "INVALID_SPLIT", "拆单至少需要 2 个子单。"}
		}
		for idx, part := range body.Splits {
			q, _ := toDecimal(part["cargo_quantity"])
			wt, _ := toDecimal(part["cargo_weight_ton"])
			v, _ := toDecimal(part["cargo_volume_cbm"])
			childNo := fmt.Sprintf("%s-S%d", wb.No, idx+1)
			cid, _ := uuid.NewV7()
			if _, err := tx.Exec(ctx, `
				INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, parent_id, status,
				  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, `+waybillCopyCols+`,
				  `+waybillSpawnDefaults+`)
				SELECT $1::uuid, now(), now(), $2, $3::uuid, 'pending_dispatch',
				  $4::int, $5::numeric, $6::numeric, `+waybillCopyCols+`,
				  `+waybillSpawnDefaultVals+`
				FROM ops_waybill WHERE id = $3::uuid`,
				cid.String(), childNo, wb.ID, q.IntPart(), wt.String(), v.String()); err != nil {
				return err
			}
			wbEvent(ctx, tx, cid.String(), "split_from", wb.No,
				map[string]any{"parent": wb.No, "__source": "split"})
			children = append(children, childNo)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE ops_waybill SET status='voided', updated_at=now() WHERE id=$1::uuid`, wb.ID); err != nil {
			return err
		}
		wbEvent(ctx, tx, wb.ID, "split", wb.No, map[string]any{"children": children, "__source": "split"})
		return nil
	})
	if !h.wrote(w, err) {
		return
	}
	h.respondWaybillList(w, r, children, http.StatusCreated, map[string]any{"parent": no})
}

// Merge POST /api/v1/waybills/merge {waybill_nos:[...], route_name}
func (h *Handler) Merge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := h.Svc.UserByID(ctx, auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		WaybillNos []string `json:"waybill_nos"`
		RouteName  string   `json:"route_name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.WaybillNos) < 2 {
		httpx.Err(w, http.StatusBadRequest, "INVALID_MERGE", "waybill_nos 至少 2 个。")
		return
	}
	var mergedNo string
	err := h.inTx(ctx, func(tx pgx.Tx) error {
		type row struct{ id, no, status string }
		rows, err := tx.Query(ctx, `
			SELECT id::text, waybill_no, status FROM ops_waybill
			WHERE waybill_no = ANY($1) ORDER BY created_at DESC, id FOR UPDATE`, body.WaybillNos)
		if err != nil {
			return err
		}
		list := []row{}
		for rows.Next() {
			var x row
			if rows.Scan(&x.id, &x.no, &x.status) != nil {
				break
			}
			list = append(list, x)
		}
		rows.Close()
		if len(list) != len(uniq(body.WaybillNos)) {
			return httpErr{http.StatusNotFound, "WAYBILL_NOT_FOUND", "部分运单不存在或无权限。"}
		}
		for _, x := range list {
			if !splittableFrom[x.status] {
				return httpErr{http.StatusConflict, "INVALID_MERGE", x.no + " 非待调度前状态，不可合单。"}
			}
		}
		first := list[0]
		mergedNo = first.no + "-M"
		mid, _ := uuid.NewV7()
		ids := make([]string, len(list))
		nos := make([]string, len(list))
		for i, x := range list {
			ids[i], nos[i] = x.id, x.no
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, status,
			  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, `+waybillCopyCols+`,
			  `+waybillSpawnDefaults+`)
			SELECT $1::uuid, now(), now(), $2, 'pending_dispatch',
			  (SELECT COALESCE(sum(cargo_quantity),0) FROM ops_waybill WHERE id::text = ANY($4)),
			  (SELECT COALESCE(sum(cargo_weight_ton),0) FROM ops_waybill WHERE id::text = ANY($4)),
			  (SELECT COALESCE(sum(cargo_volume_cbm),0) FROM ops_waybill WHERE id::text = ANY($4)),
			  order_id, customer_id, carrier_id,
			  CASE WHEN $5 <> '' THEN $5 ELSE route_name END,
			  planned_route_id, origin, destination, organization_id,
			  `+waybillSpawnDefaultVals+`
			FROM ops_waybill WHERE id = $3::uuid`,
			mid.String(), mergedNo, first.id, ids, body.RouteName); err != nil {
			return err
		}
		for _, x := range list {
			if _, err := tx.Exec(ctx, `
				UPDATE ops_waybill SET status='voided', parent_id=$2::uuid, updated_at=now()
				WHERE id=$1::uuid`, x.id, mid.String()); err != nil {
				return err
			}
			wbEvent(ctx, tx, x.id, "merged_into", mergedNo,
				map[string]any{"merged": mergedNo, "__source": "merge"})
		}
		wbEvent(ctx, tx, mid.String(), "merge", mergedNo,
			map[string]any{"sources": nos, "__source": "merge"})
		return nil
	})
	if !h.wrote(w, err) {
		return
	}
	it, err := SerializeByNo(ctx, h.DB, mergedNo)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, it)
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// respondWaybillList 回一组运单序列化，可外挂额外键
func (h *Handler) respondWaybillList(w http.ResponseWriter, r *http.Request, nos []string, code int, extra map[string]any) {
	ctx := r.Context()
	out := make([]map[string]any, 0, len(nos))
	for _, no := range nos {
		it, err := SerializeByNo(ctx, h.DB, no)
		if err != nil || it == nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
			return
		}
		out = append(out, it)
	}
	body := map[string]any{"children": out}
	for k, v := range extra {
		body[k] = v
	}
	httpx.JSON(w, code, body)
}
