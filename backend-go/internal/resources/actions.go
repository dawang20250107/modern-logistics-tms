package resources

// 标准资源上挂的少量 @action 自定义动作（回单确认 / 提醒确认 / 报警处置 /
// 报销提交-审批-驳回-付款）。通用引擎只覆盖 CRUD，这些状态跃迁需要业务语义。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
	MD  *masterdata.Handler
}

// allow 权限闸：这个动作要不要这个权限点。
//
// 由来是发布前的一次系统性排查：拿一个只有 masterdata.view + waybill.view 的
// 客服账号，把 100 条动作路由挨个打了一遍，**53 条没有被 403 挡住**。
// 挡住其中一部分的只是数据范围——而数据范围管的是"看得见谁的单"，
// 不是"能不能做这件事"：同一个网点的客服照样能派单、能签收、能核销。
//
// 这个包里本来就有带权限的入口（resolve(w, r, "waybill.manage")），
// 只是那些自己取参数、不走 resolve 的 handler 全都漏了。
// 现在缺的那些补上，并逐条登记进 cmd/server/authz_test.go 的清单。
func (h *Handler) allow(w http.ResponseWriter, r *http.Request, want string) bool {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return false
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取权限失败")
		return false
	}
	for _, p := range perms {
		if p == "*" || p == want {
			return true
		}
	}
	httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "缺少所需权限。")
	return false
}

func ocrProvider() string { return os.Getenv("OCR_PROVIDER") }

// decodeBody 宽松解析请求体（DRF 允许空体调用 @action）
func decodeBody(r *http.Request) map[string]any {
	var m map[string]any
	_ = json.NewDecoder(r.Body).Decode(&m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func str(m map[string]any, k string) (string, bool) {
	v, ok := m[k].(string)
	return v, ok
}

// pathUUID 取路径参数并校验；非法时写 404 并返回空串
func pathUUID(w http.ResponseWriter, r *http.Request, model string) string {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No "+model+" matches the given query.")
		return ""
	}
	return id
}

// echo 回读并输出（写路径统一复用列表列面，保证读写序列化一致）
func (h *Handler) echo(w http.ResponseWriter, r *http.Request, cfg masterdata.ResourceCfg, where, id string) {
	it, err := h.MD.OneDetail(r.Context(), cfg, where, id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// ── 回单确认 POST /receipts/{id}/confirm ──

func (h *Handler) ReceiptConfirm(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	ctx := r.Context()
	id := pathUUID(w, r, "Receipt")
	if id == "" {
		return
	}
	var curStatus, curSignatory string
	var waybillID *string
	if err := h.DB.QueryRow(ctx, `
		SELECT status, COALESCE(signatory,''), waybill_id::text
		FROM ops_receipt WHERE id=$1::uuid`, id).Scan(&curStatus, &curSignatory, &waybillID); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Receipt matches the given query.")
		return
	}
	body := decodeBody(r)
	status := wbstatus.PODConfirmed
	if v, ok := str(body, "status"); ok {
		status = v
	}
	// 取值必须在词表里。原先是照单全收：传什么写什么，
	// 于是可以往库里写一个 'banana'，界面上就露出这个英文串
	// （渲染处一律是 LABEL[x] ?? x），而且再也没有任何流程认得它。
	if !wbstatus.ValidPOD(status) {
		httpx.Err(w, http.StatusBadRequest, "INVALID_RECEIPT_STATUS",
			"回单状态取值非法："+status)
		return
	}
	_ = curStatus
	signatory := curSignatory
	if v, ok := str(body, "signatory"); ok {
		signatory = v
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE ops_receipt SET status=$2, signatory=$3, updated_at=now() WHERE id=$1::uuid`,
		id, status, signatory); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	// 回写运单的回单状态——**两个方向都要写**。
	//
	// 原先只有"核验通过 → 运单标已核销"这一个方向。把一张已核验的回单改判为
	// 不通过时，运单上那个"已核销"留在原地不动：回单已经被否了，
	// 而运单看起来仍然凭证齐全——回单付的单子就凭这个放行结算。
	// 按"这张运单还有没有通过核验的回单"重算，而不是按刚改的这一张。
	if waybillID != nil {
		// 占位符要显式 ::text：CASE 各分支全是参数时 Postgres 推不出类型。
		// 而这个错误原先被 `_, _ =` 丢掉了，表现是"接口 200、状态没变"——
		// 查起来毫无线索。回写失败必须留下痕迹，不能装作成功。
		if _, err := h.DB.Exec(ctx, `
			UPDATE ops_waybill w SET receipt_status = CASE
			    WHEN EXISTS (SELECT 1 FROM ops_receipt rc
			                 WHERE rc.waybill_id = w.id AND rc.status = $2::text)
			      THEN $3::text
			    WHEN w.receipt_status = $3::text THEN $4::text
			    ELSE w.receipt_status
			  END, updated_at=now()
			WHERE w.id = $1::uuid`,
			*waybillID, wbstatus.PODConfirmed, wbstatus.ReceiptAudited,
			wbstatus.ReceiptReturned); err != nil {
			slog.Error("回写运单回单状态失败", "waybill", *waybillID, "err", err)
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回写运单回单状态失败")
			return
		}
	}
	h.echo(w, r, ReceiptsCfg, "rc.id = $1::uuid", id)
}

// ── 司机确认提醒 POST /reminders/{id}/acknowledge ──

func (h *Handler) ReminderAcknowledge(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	ctx := r.Context()
	id := pathUUID(w, r, "DriverReminder")
	if id == "" {
		return
	}
	var status string
	var waybillID, waybillNo *string
	if err := h.DB.QueryRow(ctx, `
		SELECT dr.status, dr.waybill_id::text, wb.waybill_no
		FROM ops_driver_reminder dr LEFT JOIN ops_waybill wb ON wb.id = dr.waybill_id
		WHERE dr.id=$1::uuid`, id).Scan(&status, &waybillID, &waybillNo); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No DriverReminder matches the given query.")
		return
	}
	if status != "acknowledged" { // 幂等：已确认则原样返回
		if _, err := h.DB.Exec(ctx, `
			UPDATE ops_driver_reminder SET status='`+wbstatus.ReminderAcknowledged+`', acknowledged_at=now(), updated_at=now()
			WHERE id=$1::uuid`, id); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
			return
		}
		if waybillID != nil {
			eid, _ := uuid.NewV7()
			pj, _ := json.Marshal(map[string]any{"reminder_id": id})
			res := ""
			if waybillNo != nil {
				res = *waybillNo
			}
			if _, err := h.DB.Exec(ctx, `
				INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type,
				  event_time, source, resource, payload)
				VALUES ($1, now(), now(), $2::uuid, 'reminder_acknowledged', clock_timestamp(), 'driver', $3, $4)`,
				eid.String(), *waybillID, res, pj); err != nil {
				slog.Warn("资源动作写库失败", "err", err)
			}
		}
	}
	h.echo(w, r, DriverRemindersCfg, "dr.id = $1::uuid", id)
}

// ── 报警处置 POST /alerts/{id}/ack|close ──

// AlertTransition 对齐 AlertViewSet._transition（status + handled_by + handled_at）
func (h *Handler) AlertTransition(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.allow(w, r, "telematics.manage") {
			return
		}
		ctx := r.Context()
		id := pathUUID(w, r, "Alert")
		if id == "" {
			return
		}
		var uid any
		if v := auth.UserID(r); v != "" {
			uid = v
		}
		ct, err := h.DB.Exec(ctx, `
			UPDATE tel_alert SET status=$2, handled_by_id=$3::uuid, handled_at=now(), updated_at=now()
			WHERE id=$1::uuid`, id, target, uid)
		if err != nil || ct.RowsAffected() == 0 {
			httpx.Err(w, http.StatusNotFound, "error", "No Alert matches the given query.")
			return
		}
		h.echo(w, r, AlertsCfg, "al.id = $1::uuid", id)
	}
}

// ── 报销：提交 / 审批 / 驳回 / 付款 ──

// reimbCategoryLabel 与 SELECT 里的标签列同一份口径
var reimbCategoryLabel = map[string]string{
	"freight_advance": "运费垫付", "toll": "过路费", "fuel": "油费",
	"loading": "装卸费", "lodging": "食宿", "other": "其他",
}

// reimbCategoryItem 报销类别 → 费用科目（对齐 finance.reimbursement._CATEGORY_ITEM）
var reimbCategoryItem = map[string]string{
	"freight_advance": "TRANSPORT_COST", "toll": "TOLL", "fuel": "FUEL_CARD",
	"loading": "LOADING", "lodging": "OTHER_COST", "other": "OTHER_COST",
}

// genReimbNo 对齐 gen_reimb_no：BX + 本地日期 + 6 位大写 hex
func genReimbNo() string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	u := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:6]
	return "BX" + time.Now().In(loc).Format("20060102") + u
}

// ReimbursementCreate POST /reimbursements —— ViewSet.create 完全重写，走 submit_reimbursement
func (h *Handler) ReimbursementCreate(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	ctx := r.Context()
	body := decodeBody(r)

	// 运单：优先按业务单号解析，其次按 UUID
	var waybillID *string
	if no, _ := str(body, "waybill_no"); no != "" {
		var id string
		if h.DB.QueryRow(ctx, "SELECT id::text FROM ops_waybill WHERE waybill_no=$1", no).Scan(&id) == nil {
			waybillID = &id
		}
	}
	if waybillID == nil {
		if wb, _ := str(body, "waybill"); wb != "" {
			if _, err := uuid.Parse(wb); err == nil {
				var id string
				if h.DB.QueryRow(ctx, "SELECT id::text FROM ops_waybill WHERE id=$1::uuid", wb).Scan(&id) == nil {
					waybillID = &id
				}
			}
		}
	}

	amount := decimal.Zero
	switch v := body["amount"].(type) {
	case float64:
		amount = decimal.NewFromFloat(v)
	case string:
		if d, err := decimal.NewFromString(strings.TrimSpace(v)); err == nil {
			amount = d
		}
	}
	if !amount.IsPositive() {
		httpx.Err(w, http.StatusBadRequest, "REIMB_AMOUNT", "报销金额必须大于 0。")
		return
	}

	orderNo, _ := str(body, "order_no")
	if orderNo == "" && waybillID != nil {
		_ = h.DB.QueryRow(ctx, `
			SELECT COALESCE(o.order_no,'') FROM ops_waybill w
			LEFT JOIN ops_order o ON o.id = w.order_id WHERE w.id=$1::uuid`, *waybillID).Scan(&orderNo)
	}
	category, _ := str(body, "category")
	if category == "" {
		category = "other"
	}
	reason, _ := str(body, "reason")

	var submitter any
	if v := auth.UserID(r); v != "" {
		submitter = v
	}
	id, _ := uuid.NewV7()
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO fin_reimbursement (id, created_at, updated_at, reimb_no, waybill_id, order_no,
		  category, amount, reason, status, submitted_by_id, remark)
		VALUES ($1, now(), now(), $2, $3::uuid, $4, $5, $6::numeric, $7, 'submitted', $8::uuid, '')`,
		id.String(), genReimbNo(), waybillID, orderNo, category, amount.String(), reason, submitter); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败："+err.Error())
		return
	}
	it, err := h.MD.OneDetail(ctx, ReimbursementsCfg, "rb.id = $1::uuid", id.String())
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, it)
}

// reimbStatusLabel 报销状态的中文，用于把"为什么不能审批"说清楚。
func reimbStatusLabel(s string) string {
	switch s {
	case "submitted":
		return "已提交"
	case "approved":
		return "已审批"
	case "rejected":
		return "已驳回"
	case "paid":
		return "已付款"
	}
	return s
}

// ReimbursementApprove 审批通过：生成应付费用（计入毛利）+ 下游付款申请
func (h *Handler) ReimbursementApprove(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "finance.manage") {
		return
	}
	ctx := r.Context()
	id := pathUUID(w, r, "Reimbursement")
	if id == "" {
		return
	}
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 状态检查必须在事务里、并且锁住这一行。
	//
	// 原先这一段读在事务**外面**：两个人同时点审批，两边都读到 submitted、
	// 都通过检查、都往下走。审批会生成一条应付、开一张付款申请——同一笔报销
	// 计两次账、付两遍钱。
	// 实测 6 个并发的返回码是 [409 409 500 409 500 200]：钱确实没付两遍，
	// 但拦住它的是 fin_payment_request.request_no 上的唯一索引**碰巧**撞了车，
	// 而不是这段逻辑。代价是两个人收到 500「生成付款申请失败」——
	// 那看起来像系统故障，实际是"别人先批了"。
	// 加上 FOR UPDATE 之后，后来的请求会等锁、重读到 approved，拿到干净的 409。
	var status, reimbNo, category, amount, reason, orderNo string
	var waybillID *string
	if err := tx.QueryRow(ctx, `
		SELECT status, reimb_no, category, amount::text, COALESCE(reason,''), COALESCE(order_no,''), waybill_id::text
		FROM fin_reimbursement WHERE id=$1::uuid FOR UPDATE`, id).
		Scan(&status, &reimbNo, &category, &amount, &reason, &orderNo, &waybillID); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Reimbursement matches the given query.")
		return
	}
	if status != "submitted" {
		httpx.Err(w, http.StatusConflict, "REIMB_NOT_SUBMITTED", "仅已提交的报销可审批（该报销当前为「"+reimbStatusLabel(status)+"」）。")
		return
	}
	label := reimbCategoryLabel[category]
	if label == "" {
		label = category
	}

	if waybillID != nil {
		eid, _ := uuid.NewV7()
		item := reimbCategoryItem[category]
		if item == "" {
			item = "OTHER_COST"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO fin_expense_record (id, created_at, updated_at, waybill_id, direction,
			  expense_item_code, amount, currency, risk_status, source_system, external_id,
			  payee_type, payee_ref, remark, price_source, quote_id, pricing_rule_id,
			  pricing_rule_name, charge_method, matched_condition,
			  input_snapshot, calculation_detail, rule_snapshot)
			VALUES ($1, now(), now(), $2::uuid, 'payable', $3, $4::numeric, 'CNY', 'normal',
			  'reimbursement', $5, 'driver', '', $6, '', '', '', '', '', '', '{}', '{}', '{}')`,
			eid.String(), *waybillID, item, amount, reimbNo, "报销 "+label); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "生成应付失败："+err.Error())
			return
		}
	}
	prReason := fmt.Sprintf("报销 %s：%s", label, reason)
	prReason = truncRunes(prReason, 255)
	prID, _ := uuid.NewV7()
	if _, err := tx.Exec(ctx, `
		INSERT INTO fin_payment_request (id, created_at, updated_at, request_no, waybill_id,
		  counterparty_type, counterparty_ref, amount, reason, status, external_approval_no)
		VALUES ($1, now(), now(), $2, $3::uuid, 'reimbursement', $4, $5::numeric, $6, 'created', '')`,
		prID.String(), "PR-"+reimbNo, waybillID, orderNo, amount, prReason); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "生成付款申请失败："+err.Error())
		return
	}
	var approver any
	if v := auth.UserID(r); v != "" {
		approver = v
	}
	if _, err := tx.Exec(ctx, `
		UPDATE fin_reimbursement SET status='approved', approved_by_id=$2::uuid, approved_at=now(),
		  payment_request_id=$3::uuid, updated_at=now() WHERE id=$1::uuid`,
		id, approver, prID.String()); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交事务失败")
		return
	}
	h.echo(w, r, ReimbursementsCfg, "rb.id = $1::uuid", id)
}

// ReimbursementReject 驳回：仅已提交可驳回，理由写入 remark
func (h *Handler) ReimbursementReject(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "finance.manage") {
		return
	}
	ctx := r.Context()
	id := pathUUID(w, r, "Reimbursement")
	if id == "" {
		return
	}
	var status string
	if err := h.DB.QueryRow(ctx, "SELECT status FROM fin_reimbursement WHERE id=$1::uuid", id).Scan(&status); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Reimbursement matches the given query.")
		return
	}
	if status != "submitted" {
		httpx.Err(w, http.StatusConflict, "REIMB_NOT_SUBMITTED", "仅已提交的报销可驳回。")
		return
	}
	reason, _ := str(decodeBody(r), "reason")
	if _, err := h.DB.Exec(ctx, `
		UPDATE fin_reimbursement SET status='rejected', remark=$2, updated_at=now() WHERE id=$1::uuid`,
		id, reason); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	h.echo(w, r, ReimbursementsCfg, "rb.id = $1::uuid", id)
}

// ReimbursementPay 付款：同步把下游付款申请置为 paid
func (h *Handler) ReimbursementPay(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "finance.manage") {
		return
	}
	ctx := r.Context()
	id := pathUUID(w, r, "Reimbursement")
	if id == "" {
		return
	}
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// 同审批那条：状态检查要在事务里、并锁住行。
	// 另外这两条 UPDATE（报销置已付款、付款申请置已付款）必须同生共死——
	// 原先后一条的错误被 `_, _ =` 丢掉，失败时报销显示"已付款"、
	// 付款申请还挂在"未付"上，两张表各说各话。
	var status string
	var prID *string
	if err := tx.QueryRow(ctx,
		"SELECT status, payment_request_id::text FROM fin_reimbursement WHERE id=$1::uuid FOR UPDATE", id).
		Scan(&status, &prID); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Reimbursement matches the given query.")
		return
	}
	if status != "approved" {
		httpx.Err(w, http.StatusConflict, "REIMB_NOT_APPROVED",
			"仅已审批的报销可付款（该报销当前为「"+reimbStatusLabel(status)+"」）。")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE fin_reimbursement SET status='paid', paid_at=now(), updated_at=now() WHERE id=$1::uuid`,
		id); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	if prID != nil {
		if _, err := tx.Exec(ctx,
			"UPDATE fin_payment_request SET status='paid', updated_at=now() WHERE id=$1::uuid", *prID); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新付款申请失败："+err.Error())
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交事务失败")
		return
	}
	h.echo(w, r, ReimbursementsCfg, "rb.id = $1::uuid", id)
}

// PaymentResult POST /finance/payment-results —— 外部 OA/ERP 回写付款结果
func (h *Handler) PaymentResult(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "finance.manage") {
		return
	}
	ctx := r.Context()
	body := decodeBody(r)
	updated := false
	if no, _ := str(body, "request_no"); no != "" {
		var cur, curApproval string
		if err := h.DB.QueryRow(ctx, `
			SELECT status, COALESCE(external_approval_no,'') FROM fin_payment_request
			WHERE request_no=$1 ORDER BY created_at DESC, id LIMIT 1`, no).Scan(&cur, &curApproval); err == nil {
			if v, ok := str(body, "status"); ok {
				cur = v
			}
			if v, ok := str(body, "external_approval_no"); ok {
				curApproval = v
			}
			if _, err := h.DB.Exec(ctx, `
				UPDATE fin_payment_request SET status=$2, external_approval_no=$3, updated_at=now()
				WHERE request_no=$1`, no, cur, curApproval); err == nil {
				updated = true
			}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "recorded", "updated": updated})
}

// truncRunes 按字符（非字节）截断，对齐 Python 的 str[:n]
func truncRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

var _ = context.Background // 保持 context 导入（钩子文件与本文件共用）
