package finance

// 对账单：生成 / 确认 / 异常审计 / 收付款核销。
//
// 归集维度是「**项目 或 线路** + 账期」，不是合同——一份长期合同可能跑十年，
// 底下是无数订单运单，按合同出账等于把十年流水堆成一张单。合同的职责到定价为止。
//
// 生成时最要命的一条：**同一笔费用只能进一张对账单**。旧实现只按方向+时间窗+对手方
// 捞，不排除已入账记录，账期填重叠或重跑一次就重复计费。这里双保险——
// 查询侧 NOT EXISTS 过滤，库侧 fin_statement_line(expense_record_id) 唯一索引兜底
// （并发两次生成时应用层过滤挡不住，唯一索引挡得住）。

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
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// 异常审计阈值（对齐既有口径）：样本不足 3 笔不下结论；
// 超基线 50% 且绝对差额 ≥50 元才标红——两个条件缺一不可，
// 否则小额科目会因为比例波动被刷成一片红，审计信号就废了。
const (
	anomalyMinSamples = 3
	anomalyRatio      = 1.5
	anomalyFloor      = 50.0
)

type statementReq struct {
	Direction        string `json:"direction"`
	CounterpartyType string `json:"counterparty_type"`
	CounterpartyID   string `json:"counterparty_id"`
	ScopeType        string `json:"scope_type"` // project / route / all
	ScopeRef         string `json:"scope_ref"`  // 项目 id 或线路名
	Start            string `json:"start"`
	End              string `json:"end"`
	// PeriodStart / PeriodEnd 是界面上用的名字（账期），与 Start/End 同义。
	//
	// 两边原先对不上：前端发 period_start / period_end，这里只认 start / end，
	// 于是对账中心那颗「生成」按钮点下去恒定报「start 与 end 必填」——
	// 而用户明明选了账期。两个名字都收下，比逼一边改更稳。
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	DueDate     string `json:"due_date"`
	// ExternalTotal 金额，数字和字符串两种写法都收。
	//
	// JSON 里金额写成数字是最自然的，而这里原先是 string——前端在用户没填时
	// 发的是数字 0，一个数字就让**整个请求体**解不开，报错还是
	// 「请求体不是合法 JSON」，排查方向完全被带偏。
	ExternalTotal json.Number `json:"external_total"`
}

// period 取账期：优先 period_start/period_end（界面用的名字），回落到 start/end。
func (q statementReq) period() (string, string) {
	s, e := q.PeriodStart, q.PeriodEnd
	if s == "" {
		s = q.Start
	}
	if e == "" {
		e = q.End
	}
	return s, e
}

// GenerateStatement POST /finance/statements/generate
func (h *Handler) GenerateStatement(w http.ResponseWriter, r *http.Request) {
	actor := h.Svc.Guard(w, r, PermManage, denyManage)
	if actor == nil {
		return
	}
	ctx := r.Context()
	var req statementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Err(w, http.StatusBadRequest, "INVALID_BODY", "请求体不是合法 JSON。")
		return
	}
	if req.Direction != "receivable" && req.Direction != "payable" {
		httpx.Err(w, http.StatusBadRequest, "INVALID_DIRECTION", "direction 必须是 receivable 或 payable。")
		return
	}
	if req.CounterpartyType != "customer" && req.CounterpartyType != "carrier" {
		httpx.Err(w, http.StatusBadRequest, "INVALID_COUNTERPARTY", "counterparty_type 必须是 customer 或 carrier。")
		return
	}
	if _, err := uuid.Parse(req.CounterpartyID); err != nil {
		httpx.Err(w, http.StatusBadRequest, "INVALID_COUNTERPARTY", "counterparty_id 必须是合法 UUID。")
		return
	}
	periodStart, periodEnd := req.period()
	if periodStart == "" || periodEnd == "" {
		httpx.Err(w, http.StatusBadRequest, "PERIOD_REQUIRED",
			"账期必填（period_start 与 period_end，旧字段名 start/end 亦可）。")
		return
	}
	if req.ScopeType == "" {
		req.ScopeType = "all"
	}

	// 对手方过滤：应收看运单客户，应付看运单承运商
	cpCol := "w.customer_id"
	if req.CounterpartyType == "carrier" {
		cpCol = "w.carrier_id"
	}

	scopeCond, scopeName := "true", ""
	args := []any{req.Direction, periodStart, periodEnd, req.CounterpartyID}
	switch req.ScopeType {
	case "project":
		if _, err := uuid.Parse(req.ScopeRef); err != nil {
			httpx.Err(w, http.StatusBadRequest, "INVALID_SCOPE", "按项目对账时 scope_ref 必须是项目 UUID。")
			return
		}
		args = append(args, req.ScopeRef)
		scopeCond = "w.project_id = $5::uuid"
		_ = h.DB.QueryRow(ctx, "SELECT name FROM fin_project WHERE id=$1::uuid", req.ScopeRef).Scan(&scopeName)
	case "route":
		if strings.TrimSpace(req.ScopeRef) == "" {
			httpx.Err(w, http.StatusBadRequest, "INVALID_SCOPE", "按线路对账时 scope_ref 必须是线路名。")
			return
		}
		args = append(args, req.ScopeRef)
		scopeCond = "w.route_name = $5"
		scopeName = req.ScopeRef
	case "all":
		// 对手方本期全部
	default:
		httpx.Err(w, http.StatusBadRequest, "INVALID_SCOPE", "scope_type 必须是 project / route / all。")
		return
	}

	// 归集也要收窄到调用者的数据范围：否则一个只管本网点的角色，
	// 生成对账单时照样能把全集团的费用收进自己这张单里——读路径拦住了、
	// 写路径没拦，等于没拦。
	scopeSQL := "true"
	if actor.ScopeIDs != nil {
		if len(actor.ScopeIDs) == 0 {
			scopeSQL = "false"
		} else {
			args = append(args, actor.ScopeIDs)
			scopeSQL = fmt.Sprintf("w.organization_id::text = ANY($%d)", len(args))
		}
	}

	rows, err := h.DB.Query(ctx, `
		SELECT e.id::text, COALESCE(w.waybill_no,''), e.expense_item_code, e.amount, e.occurred_at,
		       w.organization_id::text
		FROM fin_expense_record e
		JOIN ops_waybill w ON w.id = e.waybill_id
		WHERE `+scopeSQL+`
		  AND e.direction = $1
		  AND e.occurred_at IS NOT NULL
		  AND (e.occurred_at AT TIME ZONE 'Asia/Shanghai')::date >= $2::date
		  AND (e.occurred_at AT TIME ZONE 'Asia/Shanghai')::date <= $3::date
		  AND `+cpCol+` = $4::uuid
		  AND `+scopeCond+`
		  -- 已进过任何一张对账单的费用不再收录（重复计费的根因）
		  AND NOT EXISTS (SELECT 1 FROM fin_statement_line l WHERE l.expense_record_id = e.id)
		ORDER BY e.occurred_at, e.id`, args...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "归集失败："+err.Error())
		return
	}
	type lineRow struct {
		expenseID, waybillNo, itemCode string
		amount                         decimal.Decimal
		occurredAt                     time.Time
		orgID                          *string
	}
	lines := []lineRow{}
	total := decimal.Zero
	// 对账单的归属组织 = 全部明细所属运单的组织，且必须唯一。
	// 跨组织（或存在无组织归属的运单）时留 NULL —— 在 scope 语义里 NULL 只有
	// all 档看得见，这是保守且正确的答案，比随便挑一个组织"归错档"强。
	orgs := map[string]struct{}{}
	orgUnknown := false
	for rows.Next() {
		var l lineRow
		if err := rows.Scan(&l.expenseID, &l.waybillNo, &l.itemCode, &l.amount, &l.occurredAt, &l.orgID); err != nil {
			rows.Close()
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取失败")
			return
		}
		lines = append(lines, l)
		total = total.Add(l.amount)
		if l.orgID == nil {
			orgUnknown = true
		} else {
			orgs[*l.orgID] = struct{}{}
		}
	}
	rows.Close()
	var stmtOrg any
	if !orgUnknown && len(orgs) == 1 {
		for id := range orgs {
			stmtOrg = id
		}
	}

	var cpName string
	table := "md_customer"
	if req.CounterpartyType == "carrier" {
		table = "md_carrier"
	}
	_ = h.DB.QueryRow(ctx, "SELECT name FROM "+table+" WHERE id=$1::uuid", req.CounterpartyID).Scan(&cpName)

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	stmtNo, err := nextStatementNo(ctx, tx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "取号失败："+err.Error())
		return
	}
	sid, _ := uuid.NewV7()
	if _, err := tx.Exec(ctx, `
		INSERT INTO fin_statement (id, created_at, updated_at, statement_no, direction,
		  counterparty_type, counterparty_id, counterparty_name, period_start, period_end, due_date,
		  total_amount, item_count, external_total, settled_amount, status,
		  scope_type, scope_id, scope_name, organization_id)
		VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7::date, $8::date, $9::date,
		        $10::numeric, $11, $12::numeric, 0, 'draft', $13, $14::uuid, $15, $16::uuid)`,
		sid.String(), stmtNo, req.Direction, req.CounterpartyType, req.CounterpartyID, cpName,
		periodStart, periodEnd, nullIfEmpty(req.DueDate), total.String(), len(lines),
		orZeroStr(req.ExternalTotal.String()), req.ScopeType, scopeIDArg(req), scopeName, stmtOrg); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "建单失败："+err.Error())
		return
	}
	for _, l := range lines {
		lid, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `
			INSERT INTO fin_statement_line (id, created_at, updated_at, statement_id, expense_record_id,
			  waybill_no, expense_item_code, amount, occurred_at, is_anomaly, baseline_avg, deviation_pct)
			VALUES ($1, now(), now(), $2::uuid, $3::uuid, $4, $5, $6::numeric, $7, false, NULL, NULL)`,
			lid.String(), sid.String(), l.expenseID, l.waybillNo, l.itemCode, l.amount.String(), l.occurredAt); err != nil {
			// 唯一索引冲突 = 并发下另一张单抢先收录了这笔费用
			httpx.Err(w, http.StatusConflict, "EXPENSE_ALREADY_BILLED",
				"部分费用已被其他对账单收录，请重新生成："+err.Error())
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	h.respondStatement(w, r, sid.String(), http.StatusCreated)
}

func scopeIDArg(req statementReq) any {
	if req.ScopeType == "project" {
		return req.ScopeRef
	}
	return nil
}

func orZeroStr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}

// nextStatementNo 原子取号 DZ+日期+6 位序（与订单/运单同一套计数器表）
func nextStatementNo(ctx context.Context, tx pgx.Tx) (string, error) {
	day := time.Now().In(shanghai()).Format("20060102")
	scope := "statement:" + day
	var v int
	if err := tx.QueryRow(ctx, `
		INSERT INTO ops_number_counter (scope, value) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET value = ops_number_counter.value + 1
		RETURNING value`, scope).Scan(&v); err != nil {
		return "", err
	}
	// 前缀保持 ST：对账单号是对外单据号，不该因为换了引擎就断成两段序列
	return fmt.Sprintf("ST%s%06d", day, v), nil
}

func shanghai() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

// ConfirmStatement POST /finance/statements/{id}/confirm
func (h *Handler) ConfirmStatement(w http.ResponseWriter, r *http.Request) {
	if h.Svc.Guard(w, r, PermManage, denyManage) == nil {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Statement matches the given query.")
		return
	}
	var status string
	if err := h.DB.QueryRow(ctx, "SELECT status FROM fin_statement WHERE id=$1::uuid", id).Scan(&status); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Statement matches the given query.")
		return
	}
	if status != "draft" {
		httpx.Err(w, http.StatusConflict, "INVALID_STATEMENT_STATUS", "仅草稿对账单可确认。")
		return
	}
	var actor any
	if v := auth.UserID(r); v != "" {
		actor = v
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE fin_statement SET status='confirmed', confirmed_by_id=$2::uuid, confirmed_at=now(),
		  updated_at=now() WHERE id=$1::uuid`, id, actor); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	h.respondStatement(w, r, id, http.StatusOK)
}

// AuditStatement POST /finance/statements/{id}/audit —— 按同科目同方向历史均值检出异常高费用
func (h *Handler) AuditStatement(w http.ResponseWriter, r *http.Request) {
	if h.Svc.Guard(w, r, PermManage, denyManage) == nil {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Statement matches the given query.")
		return
	}
	var direction string
	if err := h.DB.QueryRow(ctx, "SELECT direction FROM fin_statement WHERE id=$1::uuid", id).Scan(&direction); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Statement matches the given query.")
		return
	}
	// 基线在 SQL 里一次算完：每行取同方向同科目、排除自身的历史均值与样本数
	rows, err := h.DB.Query(ctx, `
		SELECT l.id::text, l.expense_record_id::text, l.amount, s.avg_amount, s.n
		FROM fin_statement_line l
		LEFT JOIN LATERAL (
		  SELECT avg(e.amount) AS avg_amount, count(*) AS n
		  FROM fin_expense_record e
		  WHERE e.direction = $2 AND e.expense_item_code = l.expense_item_code
		    AND (l.expense_record_id IS NULL OR e.id <> l.expense_record_id)
		) s ON true
		WHERE l.statement_id = $1::uuid`, id, direction)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "审计失败："+err.Error())
		return
	}
	type auditRow struct {
		lineID    string
		expenseID *string
		amount    decimal.Decimal
		baseline  *decimal.Decimal
		n         int
	}
	all := []auditRow{}
	for rows.Next() {
		var a auditRow
		if rows.Scan(&a.lineID, &a.expenseID, &a.amount, &a.baseline, &a.n) != nil {
			break
		}
		all = append(all, a)
	}
	rows.Close()

	anomalies := 0
	for _, a := range all {
		if a.baseline == nil || a.n < anomalyMinSamples || a.baseline.IsZero() {
			_, _ = h.DB.Exec(ctx, `
				UPDATE fin_statement_line SET baseline_avg=NULL, deviation_pct=NULL,
				  is_anomaly=false, updated_at=now() WHERE id=$1::uuid`, a.lineID)
			continue
		}
		base := *a.baseline
		dev := a.amount.Sub(base).Div(base).Mul(decimal.NewFromInt(100)).RoundBank(1)
		isAnomaly := a.amount.GreaterThan(base.Mul(decimal.NewFromFloat(anomalyRatio))) &&
			a.amount.Sub(base).GreaterThanOrEqual(decimal.NewFromFloat(anomalyFloor))
		if isAnomaly {
			anomalies++
		}
		if _, err := h.DB.Exec(ctx, `
			UPDATE fin_statement_line SET baseline_avg=$2::numeric, deviation_pct=$3::numeric,
			  is_anomaly=$4, updated_at=now() WHERE id=$1::uuid`,
			a.lineID, base.String(), dev.String(), isAnomaly); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写回审计结果失败")
			return
		}
		// 风险信号同步到费用记录，供 AI 工作台等下游复用同一份判断
		if a.expenseID != nil {
			risk := "normal"
			if isAnomaly {
				risk = "high_deviation"
			}
			_, _ = h.DB.Exec(ctx,
				"UPDATE fin_expense_record SET risk_status=$2, updated_at=now() WHERE id=$1::uuid", *a.expenseID, risk)
		}
	}
	var auditedAt time.Time
	_ = h.DB.QueryRow(ctx,
		"UPDATE fin_statement SET audited_at=now(), updated_at=now() WHERE id=$1::uuid RETURNING audited_at", id).
		Scan(&auditedAt)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"total_lines": len(all), "anomaly_count": anomalies, "audited_at": auditedAt.Format(time.RFC3339Nano),
	})
}

type settleReq struct {
	Amount      string `json:"amount"`
	Method      string `json:"method"`
	PaidAt      string `json:"paid_at"`
	ReferenceNo string `json:"reference_no"`
	Remark      string `json:"remark"`
}

// SettleStatement POST /finance/statements/{id}/settle —— 登记一笔实际收/付款（支持分次核销）
func (h *Handler) SettleStatement(w http.ResponseWriter, r *http.Request) {
	if h.Svc.Guard(w, r, PermManage, denyManage) == nil {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Statement matches the given query.")
		return
	}
	var req settleReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	amt, err := decimal.NewFromString(strings.TrimSpace(req.Amount))
	if err != nil || !amt.IsPositive() {
		httpx.Err(w, http.StatusBadRequest, "INVALID_AMOUNT", "核销金额需大于 0。")
		return
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 行锁：分次核销必须串行，否则两笔并发核销会各自读到同一个未结余额而双双通过
	var status string
	var totalAmt, settled decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT status, total_amount, settled_amount FROM fin_statement
		WHERE id=$1::uuid FOR UPDATE`, id).Scan(&status, &totalAmt, &settled); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Statement matches the given query.")
		return
	}
	if status != "confirmed" && status != "partial" {
		httpx.Err(w, http.StatusConflict, "INVALID_STATEMENT_STATUS", "仅已确认或部分结算的对账单可登记收付款。")
		return
	}
	outstanding := totalAmt.Sub(settled)
	if amt.GreaterThan(outstanding.Add(decimal.NewFromFloat(0.01))) {
		httpx.Err(w, http.StatusBadRequest, "AMOUNT_EXCEEDS_OUTSTANDING",
			fmt.Sprintf("核销金额 %s 超过未结余额 %s。", amt.String(), outstanding.String()))
		return
	}

	method := req.Method
	if method == "" {
		method = "bank"
	}
	paidAt := req.PaidAt
	if paidAt == "" {
		paidAt = time.Now().In(shanghai()).Format("2006-01-02")
	}
	var actor any
	if v := auth.UserID(r); v != "" {
		actor = v
	}
	pid, _ := uuid.NewV7()
	if _, err := tx.Exec(ctx, `
		INSERT INTO fin_statement_payment (id, created_at, updated_at, statement_id, amount, method,
		  paid_at, reference_no, remark, created_by_id)
		VALUES ($1, now(), now(), $2::uuid, $3::numeric, $4, $5::date, $6, $7, $8::uuid)`,
		pid.String(), id, amt.String(), method, paidAt, req.ReferenceNo, req.Remark, actor); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "登记失败："+err.Error())
		return
	}
	newSettled := settled.Add(amt)
	newStatus := "partial"
	setSettledAt := ""
	if newSettled.GreaterThanOrEqual(totalAmt.Sub(decimal.NewFromFloat(0.01))) {
		newStatus = "settled"
		setSettledAt = ", settled_at = now()"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE fin_statement SET settled_amount=$2::numeric, status=$3, updated_at=now()`+setSettledAt+`
		WHERE id=$1::uuid`, id, newSettled.String(), newStatus); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	h.respondStatement(w, r, id, http.StatusOK)
}

// StatementPayments GET /finance/statements/{id}/payments —— 该单的收付款流水
func (h *Handler) StatementPayments(w http.ResponseWriter, r *http.Request) {
	if h.Svc.Guard(w, r, PermView, denyView) == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Statement matches the given query.")
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT p.id::text, p.statement_id::text, p.amount::text, p.method,
		       (CASE p.method WHEN 'bank' THEN '银行转账' WHEN 'cash' THEN '现金' WHEN 'wechat' THEN '微信'
		                      WHEN 'alipay' THEN '支付宝' WHEN 'offset' THEN '冲抵/对冲'
		                      WHEN 'acceptance' THEN '承兑汇票' ELSE '其他' END) AS method_label,
		       p.paid_at::text, p.reference_no, p.remark, COALESCE(u.username,''), p.created_at
		FROM fin_statement_payment p
		LEFT JOIN accounts_user u ON u.id = p.created_by_id
		WHERE p.statement_id = $1::uuid
		ORDER BY p.paid_at DESC, p.created_at DESC, p.id`, id)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var pid, sid, amount, method, label, paidAt, ref, remark, by string
		var createdAt time.Time
		if rows.Scan(&pid, &sid, &amount, &method, &label, &paidAt, &ref, &remark, &by, &createdAt) != nil {
			break
		}
		items = append(items, map[string]any{
			"id": pid, "statement": sid, "amount": amount, "method": method, "method_label": label,
			"paid_at": paidAt, "reference_no": ref, "remark": remark,
			"created_by_name": by, "created_at": createdAt.Format(time.RFC3339Nano),
		})
	}
	httpx.JSON(w, http.StatusOK, items)
}

// respondStatement 统一回读一张对账单（含 scope 与结算进度）
// inTx 把一段写操作裹进事务：任一步失败整体回滚
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

// statementJSON 对账单表头的 StatementSerializer 列面（$1 = 主键）
const statementJSON = `
		SELECT json_build_object(
		  'id', s.id::text, 'statement_no', s.statement_no, 'direction', s.direction,
		  'counterparty_type', s.counterparty_type, 'counterparty_id', s.counterparty_id,
		  'counterparty_name', s.counterparty_name,
		  'scope_type', s.scope_type, 'scope_id', s.scope_id::text, 'scope_name', s.scope_name,
		  'period_start', s.period_start::text, 'period_end', s.period_end::text,
		  'due_date', s.due_date::text, 'total_amount', s.total_amount::text,
		  'item_count', s.item_count, 'external_total', s.external_total::text,
		  'settled_amount', s.settled_amount::text,
		  'outstanding', (s.total_amount - s.settled_amount)::text,
		  'diff', (s.total_amount - s.external_total)::text,
		  'status', s.status, 'audited_at', s.audited_at, 'confirmed_at', s.confirmed_at,
		  'settled_at', s.settled_at, 'created_at', s.created_at)
		FROM fin_statement s WHERE s.id = $1::uuid`

func (h *Handler) respondStatement(w http.ResponseWriter, r *http.Request, id string, code int) {
	var out map[string]any
	row := h.DB.QueryRow(r.Context(), `
		SELECT json_build_object(
		  'id', s.id::text, 'statement_no', s.statement_no, 'direction', s.direction,
		  'counterparty_type', s.counterparty_type, 'counterparty_id', s.counterparty_id,
		  'counterparty_name', s.counterparty_name,
		  'scope_type', s.scope_type, 'scope_id', s.scope_id::text, 'scope_name', s.scope_name,
		  'period_start', s.period_start::text, 'period_end', s.period_end::text,
		  'due_date', s.due_date::text, 'total_amount', s.total_amount::text,
		  'item_count', s.item_count, 'external_total', s.external_total::text,
		  'settled_amount', s.settled_amount::text,
		  'outstanding', (s.total_amount - s.settled_amount)::text,
		  'diff', (s.total_amount - s.external_total)::text,
		  'status', s.status, 'audited_at', s.audited_at, 'confirmed_at', s.confirmed_at,
		  'settled_at', s.settled_at, 'created_at', s.created_at)
		FROM fin_statement s WHERE s.id = $1::uuid`, id)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	_ = json.Unmarshal(raw, &out)
	httpx.JSON(w, code, out)
}
