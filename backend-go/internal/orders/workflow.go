package orders

// 订单全流程总览与单据血缘（OrderDetailPage 每次打开必打）：
//   GET /orders/{id}/workflow —— 建单→确认→派单→合同→司机注册→在途→签收→报销→付款→对账→完成
//   GET /orders/{id}/lineage  —— 订单(DD) → 运单(YD) → 对账单(ST) 全链路关系图
// 二者均为纯读聚合，各用一条主 SQL + json_agg 带出，无 N+1。

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

var orderStatusLabel = map[string]string{
	"draft": "草稿", "pending_confirm": "待确认", "confirmed": "已确认", "pooled": "订单池",
	"dispatching": "调度中", "converted": "已派单", "completed": "已完成", "cancelled": "已取消",
}
var contractConfirmLabel = map[string]string{
	"pending": "待发送", "sent": "已发送", "confirmed": "已确认", "rejected": "已拒签",
}

// resolveOrder 鉴权 + 数据范围校验，返回订单主键；失败时已写响应
func (h *Handler) resolveOrder(w http.ResponseWriter, r *http.Request) (string, bool) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return "", false
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return "", false
	}
	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return "", false
	}
	q := `SELECT o.id::text FROM ops_order o
	      LEFT JOIN accounts_user cb ON cb.id = o.created_by_id
	      WHERE o.id=$1::uuid AND NOT o.is_deleted`
	args := []any{id}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
			return "", false
		}
		q += ` AND cb.organization_id::text = ANY($2)`
		args = append(args, scopeIDs)
	}
	var out string
	if err := h.DB.QueryRow(ctx, q, args...).Scan(&out); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return "", false
	}
	return out, true
}

func stage(key, name string, done bool, detail string, at *time.Time) map[string]any {
	var atOut any
	if at != nil {
		atOut = at.Format(time.RFC3339Nano)
	}
	return map[string]any{"key": key, "name": name, "done": done, "detail": detail, "at": atOut}
}

// Workflow GET /api/v1/orders/{id}/workflow
func (h *Handler) Workflow(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.view") {
		return
	}
	id, ok := h.resolveOrder(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var orderNo, orderStatus string
	var createdAt time.Time
	// 最新运单（-created_at）及其合同/司机
	var wbNo, wbStatus, wbReceiptStatus, contractStatus, driverName *string
	var wbID *string
	var driverRegistered *bool
	if err := h.DB.QueryRow(ctx, `
		SELECT o.order_no, o.status, o.created_at,
		       w.id::text, w.waybill_no, w.status, w.receipt_status,
		       (SELECT c.confirm_status FROM ops_contract c WHERE c.waybill_id=w.id
		        ORDER BY c.created_at DESC LIMIT 1),
		       d.name, d.app_registered
		FROM ops_order o
		LEFT JOIN LATERAL (
		  SELECT * FROM ops_waybill x WHERE x.order_id=o.id ORDER BY x.created_at DESC LIMIT 1
		) w ON true
		LEFT JOIN md_driver d ON d.id = w.driver_id
		WHERE o.id=$1::uuid`, id).
		Scan(&orderNo, &orderStatus, &createdAt, &wbID, &wbNo, &wbStatus, &wbReceiptStatus,
			&contractStatus, &driverName, &driverRegistered); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}

	// 报销 / 下游付款 / 上游对账（均以该运单为锚）
	var reimbTotal, reimbOpen, payTotal, payUnpaid, stmtCount int
	if wbID != nil {
		_ = h.DB.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM fin_reimbursement WHERE waybill_id=$1::uuid),
			       (SELECT count(*) FROM fin_reimbursement WHERE waybill_id=$1::uuid AND status NOT IN ('paid','rejected')),
			       (SELECT count(*) FROM fin_payment_request WHERE waybill_id=$1::uuid),
			       (SELECT count(*) FROM fin_payment_request WHERE waybill_id=$1::uuid AND status <> 'paid'),
			       (SELECT count(*) FROM fin_statement_line l WHERE l.waybill_no=$2)`,
			*wbID, deref(wbNo)).Scan(&reimbTotal, &reimbOpen, &payTotal, &payUnpaid, &stmtCount)
	}

	hasWb := wbID != nil
	receiptOK := hasWb && (deref(wbReceiptStatus) == "received" || deref(wbReceiptStatus) == "confirmed")
	if hasWb {
		switch deref(wbStatus) {
		case "signed", "delivered", "settled":
			receiptOK = true
		}
	}
	transitDone := false
	if hasWb {
		switch deref(wbStatus) {
		case "signed", "delivered", "settled":
			transitDone = true
		}
	}
	transitLabel := "—"
	if hasWb {
		transitLabel = wbstatus.Label[deref(wbStatus)]
	}
	dispatchDetail := "待派单"
	if hasWb {
		dispatchDetail = deref(wbNo)
	}
	contractDetail := "未生成"
	if contractStatus != nil {
		contractDetail = contractConfirmLabel[*contractStatus]
	}
	onboardDetail := "未指派"
	if driverName != nil {
		onboardDetail = *driverName
	}
	reimbDetail := "无"
	if reimbTotal > 0 {
		reimbDetail = strconv.Itoa(reimbTotal) + " 笔"
	}
	payOK := hasWb && payTotal > 0 && payUnpaid == 0
	payDetail := "待付款"
	if payOK {
		payDetail = "已付清"
	}
	settled := hasWb && stmtCount > 0
	reconcileDetail := "待对账"
	if settled {
		reconcileDetail = "已对账"
	}

	stages := []map[string]any{
		stage("created", "建单", true, orderNo, &createdAt),
		stage("confirmed", "确认", orderStatus != "draft" && orderStatus != "pending_confirm", "", nil),
		stage("dispatched", "派单", hasWb, dispatchDetail, nil),
		stage("contract", "承运合同", contractStatus != nil && *contractStatus == "confirmed", contractDetail, nil),
		stage("onboard", "司机注册", driverRegistered != nil && *driverRegistered, onboardDetail, nil),
		stage("transit", "在途", transitDone, transitLabel, nil),
		stage("pod", "签收回单", receiptOK, "", nil),
		stage("reimburse", "报销", reimbTotal == 0 || reimbOpen == 0, reimbDetail, nil),
		stage("payment", "下游付款", payOK, payDetail, nil),
		stage("reconcile", "上游对账", settled, reconcileDetail, nil),
		stage("completed", "完成", orderStatus == "completed", "", nil),
	}
	current := "completed"
	for _, s := range stages {
		if !s["done"].(bool) {
			current = s["key"].(string)
			break
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"order_no": orderNo, "current": current, "stages": stages,
	})
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Lineage GET /api/v1/orders/{id}/lineage —— 订单→运单→对账单全链路
func (h *Handler) Lineage(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.view") {
		return
	}
	id, ok := h.resolveOrder(w, r)
	if !ok {
		return
	}
	var out json.RawMessage
	// 一条主 SQL：订单头 + 运单（含费用明细与所属对账单）+ 批次 + AR/AP 对账单 + 汇总
	err := h.DB.QueryRow(r.Context(), `
WITH wb AS (
  SELECT w.*, COALESCE(ca.name,'') AS carrier_nm,
         COALESCE(b.batch_no,'') AS batch_nm, b.id AS bid
  FROM ops_waybill w
  LEFT JOIN md_carrier ca ON ca.id = w.carrier_id
  LEFT JOIN ops_dispatch_batch b ON b.id = w.batch_id
  WHERE w.order_id = $1::uuid
), exp AS (
  SELECT e.*, w.id AS wid FROM fin_expense_record e JOIN wb w ON w.id = e.waybill_id
), pair AS (          -- （对账单, 运单）配对：同一对账单可挂多张运单
  SELECT DISTINCT s.id AS sid, e.wid
  FROM fin_statement_line l
  JOIN exp e ON e.id = l.expense_record_id
  JOIN fin_statement s ON s.id = l.statement_id
), sj AS (            -- 每张对账单一份 JSON（status 有 choices，取中文标签）
  SELECT s.id AS sid, s.direction, json_build_object(
    'id', s.id::text, 'statement_no', s.statement_no, 'direction', s.direction,
    'counterparty_type', s.counterparty_type, 'counterparty_name', s.counterparty_name,
    'status', s.status,
    'status_label', (CASE s.status WHEN 'draft' THEN '草稿' WHEN 'confirmed' THEN '已确认'
                     WHEN 'partial' THEN '部分结算' WHEN 'settled' THEN '已结算' ELSE s.status END),
    'total_amount', s.total_amount::float8, 'settled_amount', s.settled_amount::float8,
    'outstanding', (s.total_amount - s.settled_amount)::float8,
    'period_start', s.period_start, 'period_end', s.period_end) AS j
  FROM fin_statement s WHERE s.id IN (SELECT sid FROM pair)
)
SELECT json_build_object(
  'order', (SELECT json_build_object(
      'id', o.id::text, 'order_no', o.order_no, 'status', o.status,
      -- 这里原先是 'status_label', o.status —— 直接把原始枚举值当标签返回。
      -- 那是从 Django 照抄过来的缺陷：get_FOO_display() 在字段没绑 choices 时
      -- 回落原始值，Order.status 恰好没绑。结果订单详情的单据血缘里显示
      -- 「pending_confirm」而不是「待确认」。运单和对账单这两行都有 CASE 映射，
      -- 只有订单这行没有，所以它一直没被发现。
      'status_label', (CASE o.status WHEN 'draft' THEN '草稿' WHEN 'pending_confirm' THEN '待确认'
        WHEN 'confirmed' THEN '已确认' WHEN 'pooled' THEN '已进池' WHEN 'dispatching' THEN '调度中'
        WHEN 'converted' THEN '已转运单' WHEN 'completed' THEN '已完成'
        WHEN 'cancelled' THEN '已取消' ELSE o.status END),
      'customer_name', COALESCE(c.name,'散客'), 'business_type', o.business_type,
      'quoted_amount', COALESCE(o.quoted_amount,0)::float8, 'created_at', o.created_at)
    FROM ops_order o LEFT JOIN md_customer c ON c.id=o.customer_id WHERE o.id=$1::uuid),
  'waybills', COALESCE((SELECT json_agg(json_build_object(
      'id', w.id::text, 'waybill_no', w.waybill_no, 'status', w.status,
      -- 第 6 份状态词表原本就写死在这段 SQL 里。它比 Go 里那 5 份更难发现：
      -- grep 状态标签时不会想到去翻查询字符串，加了新状态这里会静默漏掉，
      -- 表现是同一个状态在别处显示「已中止」、在这个接口里显示 aborted。
      'status_label', `+wbstatus.LabelCaseSQL("w.status")+`,
      'carrier_name', w.carrier_nm, 'dispatch_type', w.dispatch_type, 'batch_no', w.batch_nm,
      'receivable', COALESCE((SELECT sum(amount) FROM exp e WHERE e.wid=w.id AND e.direction='receivable'),0)::float8,
      'payable', COALESCE((SELECT sum(amount) FROM exp e WHERE e.wid=w.id AND e.direction='payable'),0)::float8,
      'expenses', COALESCE((SELECT json_agg(json_build_object(
          'direction', e.direction, 'expense_item_code', e.expense_item_code,
          'amount', e.amount::float8, 'payee_type', e.payee_type, 'payee_ref', e.payee_ref,
          'risk_status', e.risk_status) ORDER BY e.direction, e.amount DESC)
        FROM exp e WHERE e.wid=w.id), '[]'::json),
      'statements', COALESCE((SELECT json_agg(sj.j) FROM pair p JOIN sj ON sj.sid=p.sid
        WHERE p.wid=w.id), '[]'::json)
    ) ORDER BY w.created_at, w.id) FROM wb w), '[]'::json),
  'batches', COALESCE((SELECT json_agg(json_build_object(
      'batch_no', b.batch_no, 'carrier_name', COALESCE(bc.name,''), 'status', b.status,
      'statement_no', b.statement_no, 'order_count', b.order_count,
      'total_payable', b.total_payable::float8))
    FROM (SELECT DISTINCT bid FROM wb WHERE bid IS NOT NULL) d
    JOIN ops_dispatch_batch b ON b.id = d.bid
    LEFT JOIN md_carrier bc ON bc.id = b.carrier_id), '[]'::json),
  'ar_statements', COALESCE((SELECT json_agg(j) FROM sj WHERE direction='receivable'), '[]'::json),
  'ap_statements', COALESCE((SELECT json_agg(j) FROM sj WHERE direction='payable'), '[]'::json),
  'summary', json_build_object(
      'waybill_count', (SELECT count(*) FROM wb),
      'receivable_total', COALESCE((SELECT sum(amount) FROM exp WHERE direction='receivable'),0)::float8,
      'payable_total', COALESCE((SELECT sum(amount) FROM exp WHERE direction='payable'),0)::float8,
      'gross', (COALESCE((SELECT sum(amount) FROM exp WHERE direction='receivable'),0)
              - COALESCE((SELECT sum(amount) FROM exp WHERE direction='payable'),0))::float8,
      'statement_count', (SELECT count(*) FROM sj))
)`, id).Scan(&out)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
