package waybills

// 运单详情页读侧全家桶：costs / cost-catalog / eta / collection / finance-card /
// reply-card / contract / reminders。单页打开触发 8+ 请求，一次性原生化。
// 费用写（add-expense / generate-costs）与代收货款写（collect-cod / remit-cod）
// 属财务写路径，按产品要求冻结，继续走代理。

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/expitem"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

// 词表收在 internal/expitem 里（原先这里和 finance/dashboard.go 各存一份，
// 而计价规则那条路径压根不校验）。这里只留别名，改科目改那一个文件。
var (
	costItems   = expitem.Cost
	incomeItems = expitem.Income
	payeeLabels = expitem.Payees
)

func itemLabel(code string) string { return expitem.Label(code) }

// resolve 鉴权 + 权限 + 数据范围，返回运单主键；失败时已写响应
func (h *Handler) resolve(w http.ResponseWriter, r *http.Request, perm string) (id, no string, ok bool) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return "", "", false
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil || !hasPerm(perms, perm) {
		httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "无运单查看权限")
		return "", "", false
	}
	no = chi.URLParam(r, "no")
	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return "", "", false
	}
	q := `SELECT id::text FROM ops_waybill WHERE waybill_no=$1`
	args := []any{no}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			httpx.Err(w, http.StatusNotFound, "error", "No Waybill matches the given query.")
			return "", "", false
		}
		q += ` AND organization_id::text = ANY($2)`
		args = append(args, scopeIDs)
	}
	if err := h.DB.QueryRow(ctx, q, args...).Scan(&id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Waybill matches the given query.")
		return "", "", false
	}
	return id, no, true
}

// CostCatalog GET /api/v1/waybills/cost-catalog
func (h *Handler) CostCatalog(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Svc.UserByID(r.Context(), auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"cost_items": costItems, "income_items": incomeItems, "payees": payeeLabels,
	})
}

type expenseRow struct {
	ID, Direction, Code, RiskStatus, PayeeType, PayeeRef, SourceSystem, Remark string
	Amount                                                                     float64
}

// Costs GET /api/v1/waybills/{no}/costs
func (h *Handler) Costs(w http.ResponseWriter, r *http.Request) {
	id, no, ok := h.resolve(w, r, "waybill.view")
	if !ok {
		return
	}
	ctx := r.Context()
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, direction, expense_item_code, amount::float8, risk_status,
		       payee_type, payee_ref, source_system, remark
		FROM fin_expense_record WHERE waybill_id=$1::uuid ORDER BY created_at, id`, id)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	receivables, payables, external := []map[string]any{}, []map[string]any{}, []map[string]any{}
	var rt, pt, et float64
	byPayee := map[string]*struct {
		PayeeType, PayeeLabel string
		Amount                float64
	}{}
	payeeOrder := []string{}
	for rows.Next() {
		var e expenseRow
		if rows.Scan(&e.ID, &e.Direction, &e.Code, &e.Amount, &e.RiskStatus,
			&e.PayeeType, &e.PayeeRef, &e.SourceSystem, &e.Remark) != nil {
			break
		}
		payload := map[string]any{
			"id": e.ID, "direction": e.Direction, "expense_item_code": e.Code,
			"item_label": itemLabel(e.Code), "amount": e.Amount, "risk_status": e.RiskStatus,
			"payee_type": e.PayeeType, "payee_label": payeeLabels[e.PayeeType],
			"payee_ref": e.PayeeRef, "source_system": e.SourceSystem, "remark": e.Remark,
		}
		switch e.Direction {
		case "receivable":
			receivables = append(receivables, payload)
			rt += e.Amount
		case "payable":
			payables = append(payables, payload)
			pt += e.Amount
			key := e.PayeeType
			if key == "" {
				key = "other"
			}
			if _, seen := byPayee[key]; !seen {
				byPayee[key] = &struct {
					PayeeType, PayeeLabel string
					Amount                float64
				}{key, payeeLabels[key], 0}
				payeeOrder = append(payeeOrder, key)
			}
			byPayee[key].Amount += e.Amount
		case "external":
			external = append(external, payload)
			et += e.Amount
		}
	}
	byPayeeList := make([]map[string]any, 0, len(payeeOrder))
	for _, k := range payeeOrder {
		v := byPayee[k]
		byPayeeList = append(byPayeeList, map[string]any{
			"payee_type": v.PayeeType, "payee_label": v.PayeeLabel, "amount": v.Amount,
		})
	}
	gross := rt - pt - et
	margin := 0.0
	if rt != 0 {
		margin = gross / rt
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waybill_no": no, "receivables": receivables, "payables": payables,
		"external_expenses": external, "payables_by_payee": byPayeeList,
		"receivable_total": rt, "payable_total": pt,
		"gross_profit": gross, "gross_margin": margin,
	})
}

// ETA GET /api/v1/waybills/{no}/eta —— 按最新轨迹点动态预测并回写（对齐 predict_eta persist=True）
func (h *Handler) ETA(w http.ResponseWriter, r *http.Request) {
	id, no, ok := h.resolve(w, r, "waybill.view")
	if !ok {
		return
	}
	ctx := r.Context()
	var plannedArrival, estimatedArrival *time.Time
	var drift int
	var riskLevel string
	if err := h.DB.QueryRow(ctx, `SELECT planned_arrival, estimated_arrival, eta_drift_minutes, risk_level
		FROM ops_waybill WHERE id=$1::uuid`, id).Scan(&plannedArrival, &estimatedArrival, &drift, &riskLevel); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}

	var remainingKM, avgSpeed *float64
	var lat, lng *float64
	_ = h.DB.QueryRow(ctx, `SELECT lat::float8, lng::float8 FROM ops_tracking_point
		WHERE waybill_id=$1::uuid ORDER BY reported_at DESC LIMIT 1`, id).Scan(&lat, &lng)
	var dLat, dLng *float64
	_ = h.DB.QueryRow(ctx, `SELECT lat::float8, lng::float8 FROM ops_waybill_stop
		WHERE waybill_id=$1::uuid AND stop_type='delivery' AND lat IS NOT NULL AND lng IS NOT NULL
		ORDER BY seq DESC LIMIT 1`, id).Scan(&dLat, &dLng)

	if lat != nil && lng != nil && dLat != nil && dLng != nil {
		const roadFactor, defaultSpeed, minMoving = 1.3, 55.0, 5.0
		km := haversineM(*lat, *lng, *dLat, *dLng) / 1000.0 * roadFactor
		// 近 5 个轨迹点中有效行驶速度均值（不足则默认巡航速度）
		speed := defaultSpeed
		srows, err := h.DB.Query(ctx, `SELECT speed_kmh::float8 FROM ops_tracking_point
			WHERE waybill_id=$1::uuid ORDER BY reported_at DESC LIMIT 5`, id)
		if err == nil {
			sum, n := 0.0, 0
			for srows.Next() {
				var s *float64
				if srows.Scan(&s) != nil {
					break
				}
				if s != nil && *s >= minMoving {
					sum += *s
					n++
				}
			}
			srows.Close()
			if n > 0 {
				speed = sum / float64(n)
			}
		}
		etaMinutes := 0.0
		if speed != 0 {
			etaMinutes = km / speed * 60
		}
		estimated := httpx.Micros(time.Now().Add(time.Duration(etaMinutes * float64(time.Minute))))
		newDrift := 0
		if plannedArrival != nil {
			newDrift = int(estimated.Sub(*plannedArrival).Minutes())
		}
		if _, err := h.DB.Exec(ctx, `UPDATE ops_waybill SET estimated_arrival=$2, eta_drift_minutes=$3, updated_at=now()
			WHERE id=$1::uuid`, id, estimated, newDrift); err != nil {
			slog.Warn("运单卡片写库失败", "err", err)
		}
		estimatedArrival, drift = &estimated, newDrift
		rkm := math.Round(km*10) / 10
		rsp := math.Round(speed*10) / 10
		remainingKM, avgSpeed = &rkm, &rsp
	}

	reason := "traffic_or_capacity_risk"
	if riskLevel == "high" {
		reason = "route_deviation_detected"
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waybill_no": no, "planned_arrival": plannedArrival, "estimated_arrival": estimatedArrival,
		"eta_drift_minutes": drift, "risk_level": riskLevel,
		"predicted": remainingKM != nil, "remaining_km": remainingKM, "avg_speed_kmh": avgSpeed,
		"reason": reason,
	})
}

// haversineM 球面距离（米），对齐 apps/ops/geofence.haversine_m
func haversineM(lat1, lng1, lat2, lng2 float64) float64 {
	const earthR = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthR * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// Collection GET /api/v1/waybills/{no}/collection —— 司机送达应收（到付运费 + 代收货款）
func (h *Handler) Collection(w http.ResponseWriter, r *http.Request) {
	id, no, ok := h.resolve(w, r, "waybill.view")
	if !ok {
		return
	}
	var freightTerm, codStatus string
	var cod float64
	var orderQuoted *float64
	var arSum *float64
	if err := h.DB.QueryRow(r.Context(), `
		SELECT w.freight_term, w.cod_status, w.cod_amount::float8,
		       (SELECT sum(amount)::float8 FROM fin_expense_record e
		        WHERE e.waybill_id=w.id AND e.direction='receivable'),
		       (SELECT o.quoted_amount::float8 FROM ops_order o WHERE o.id=w.order_id)
		FROM ops_waybill w WHERE w.id=$1::uuid`, id).
		Scan(&freightTerm, &codStatus, &cod, &arSum, &orderQuoted); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	collectFreight := 0.0
	if freightTerm == "collect" {
		switch {
		case arSum != nil:
			collectFreight = *arSum
		case orderQuoted != nil:
			collectFreight = *orderQuoted
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waybill_no": no, "freight_term": freightTerm,
		"collect_freight": collectFreight, "cod_amount": cod, "cod_status": codStatus,
		"total_to_collect": collectFreight + cod,
	})
}

// FinanceCard GET /api/v1/waybills/{no}/finance-card
func (h *Handler) FinanceCard(w http.ResponseWriter, r *http.Request) {
	id, no, ok := h.resolve(w, r, "waybill.view")
	if !ok {
		return
	}
	var customerName, carrierName, receiptStatus string
	var receivable, payable, other, excDeduction float64
	var openExc int
	if err := h.DB.QueryRow(r.Context(), `
		SELECT COALESCE(c.name,'散客'), COALESCE(ca.name,''), w.receipt_status,
		       COALESCE((SELECT sum(amount) FROM fin_expense_record e WHERE e.waybill_id=w.id AND e.direction='receivable'),0)::float8,
		       COALESCE((SELECT sum(amount) FROM fin_expense_record e WHERE e.waybill_id=w.id AND e.direction='payable'),0)::float8,
		       COALESCE((SELECT sum(amount) FROM fin_expense_record e WHERE e.waybill_id=w.id AND e.direction='external'),0)::float8,
		       COALESCE((SELECT sum(amount) FROM ops_exception x WHERE x.waybill_id=w.id AND x.status <> 'resolved'),0)::float8,
		       (SELECT count(*) FROM ops_exception x WHERE x.waybill_id=w.id AND x.status <> 'resolved')
		FROM ops_waybill w
		LEFT JOIN md_customer c ON c.id=w.customer_id
		LEFT JOIN md_carrier ca ON ca.id=w.carrier_id
		WHERE w.id=$1::uuid`, id).
		Scan(&customerName, &carrierName, &receiptStatus, &receivable, &payable, &other, &excDeduction, &openExc); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	gross := math.Round((receivable-payable-other)*100) / 100
	receiptOK := receiptStatus == "returned" || receiptStatus == "audited"
	hasOpenExc := openExc > 0
	blockers := []string{}
	if !receiptOK {
		blockers = append(blockers, "回单未回收")
	}
	if hasOpenExc {
		blockers = append(blockers, "存在未决异常")
	}
	var marginPct any
	if receivable != 0 {
		marginPct = math.Round(gross/receivable*1000) / 1000
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waybill_no": no, "customer_name": customerName, "carrier_name": carrierName,
		"receivable": receivable, "payable": payable, "other_fee": other,
		"gross_margin": gross, "margin_pct": marginPct,
		"exception_deduction": excDeduction, "receipt_ok": receiptOK,
		"reconcilable": receiptOK && !hasOpenExc, "blockers": blockers,
	})
}

// ReplyCard GET /api/v1/waybills/{no}/reply-card —— 客服可复制话术卡
func (h *Handler) ReplyCard(w http.ResponseWriter, r *http.Request) {
	id, no, ok := h.resolve(w, r, "waybill.view")
	if !ok {
		return
	}
	ctx := r.Context()
	var status, origin, destination, driverName, driverPhone, plate, receiptStatus string
	var estimated, planned *time.Time
	if err := h.DB.QueryRow(ctx, `
		SELECT w.status, w.origin, w.destination, COALESCE(d.name,''), COALESCE(d.phone,''),
		       COALESCE(v.plate_no,''), w.receipt_status, w.estimated_arrival, w.planned_arrival
		FROM ops_waybill w
		LEFT JOIN md_driver d ON d.id=w.driver_id
		LEFT JOIN md_vehicle v ON v.id=w.vehicle_id
		WHERE w.id=$1::uuid`, id).
		Scan(&status, &origin, &destination, &driverName, &driverPhone, &plate, &receiptStatus, &estimated, &planned); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	// 最近节点：优先已实际到达的停靠点，回退最近运单事件
	var latestNode map[string]any
	var stopType, city string
	var arrivedAt *time.Time
	if err := h.DB.QueryRow(ctx, `SELECT stop_type, city, actual_arrival_at FROM ops_waybill_stop
		WHERE waybill_id=$1::uuid AND actual_arrival_at IS NOT NULL
		ORDER BY actual_arrival_at DESC LIMIT 1`, id).Scan(&stopType, &city, &arrivedAt); err == nil {
		node := stopTypeLabel[stopType]
		if city != "" {
			node += " · " + city
		}
		var at any
		if arrivedAt != nil {
			at = arrivedAt.Format(time.RFC3339Nano)
		}
		latestNode = map[string]any{"node": node, "at": at}
	} else {
		var evType string
		var evTime *time.Time
		if err := h.DB.QueryRow(ctx, `SELECT event_type, event_time FROM ops_waybill_event
			WHERE waybill_id=$1::uuid ORDER BY event_time DESC LIMIT 1`, id).Scan(&evType, &evTime); err == nil {
			var at any
			if evTime != nil {
				at = evTime.Format(time.RFC3339Nano)
			}
			latestNode = map[string]any{"node": evType, "at": at}
		}
	}
	var excType *string
	_ = h.DB.QueryRow(ctx, `SELECT exception_type FROM ops_exception
		WHERE waybill_id=$1::uuid AND status <> 'resolved' ORDER BY created_at DESC LIMIT 1`, id).Scan(&excType)

	statusLbl := wbstatus.Label[status]
	receiptLbl := "待回收"
	switch receiptStatus {
	case "returned":
		receiptLbl = "已回收"
	case "audited":
		receiptLbl = "已核销"
	}
	eta := estimated
	if eta == nil {
		eta = planned
	}
	route := orDash(origin) + "→" + orDash(destination)
	lines := []string{"【" + no + "】" + route, "当前状态：" + statusLbl}
	if plate != "" || driverName != "" {
		lines = append(lines, trimSpace("承运："+trimSpace(plate+" "+driverName+" "+driverPhone)))
	}
	if latestNode != nil {
		lines = append(lines, "最近节点："+fmt.Sprint(latestNode["node"]))
	}
	if eta != nil {
		lines = append(lines, "预计到达："+eta.In(cardCST).Format("01-02 15:04"))
	}
	lines = append(lines, "回单："+receiptLbl)
	var excLabel any
	if excType != nil {
		lbl := exceptionTypeLabel[*excType]
		if lbl == "" {
			lbl = *excType
		}
		excLabel = lbl
		lines = append(lines, "异常："+lbl)
	}
	var etaOut any
	if eta != nil {
		etaOut = eta.Format(time.RFC3339Nano)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waybill_no": no, "route": route, "status": status, "status_label": statusLbl,
		"driver_name": driverName, "driver_phone": driverPhone, "plate_no": plate,
		"latest_node": latestNode, "eta": etaOut, "receipt_status": receiptLbl,
		"exception": excLabel, "copy_text": joinLines(lines),
	})
}

// Contract GET /api/v1/waybills/{no}/contract —— 最新合同（无则 null）
func (h *Handler) Contract(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.resolve(w, r, "waybill.view")
	if !ok {
		return
	}
	var out json.RawMessage
	err := h.DB.QueryRow(r.Context(), `
		SELECT COALESCE((
		  SELECT json_build_object(
		    'id', c.id::text, 'contract_no', c.contract_no, 'waybill', c.waybill_id::text,
		    'driver', c.driver_id::text, 'driver_name', COALESCE(d.name,''),
		    'template_code', c.template_code, 'content', c.content, 'sent_at', c.sent_at,
		    'driver_reply', c.driver_reply, 'confirm_status', c.confirm_status,
		    'status_label', (CASE c.confirm_status WHEN 'pending' THEN '待发送' WHEN 'sent' THEN '已发送'
		                     WHEN 'confirmed' THEN '已确认' WHEN 'rejected' THEN '已拒签' ELSE c.confirm_status END),
		    'confirmed_at', c.confirmed_at, 'pdf_url', (CASE WHEN c.pdf <> '' THEN '/media/' || c.pdf ELSE '' END), 'created_at', c.created_at)
		  FROM ops_contract c LEFT JOIN md_driver d ON d.id=c.driver_id
		  WHERE c.waybill_id=$1::uuid ORDER BY c.created_at DESC LIMIT 1
		), 'null'::json)`, id).Scan(&out)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// Reminders GET /api/v1/waybills/{no}/reminders
func (h *Handler) Reminders(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.resolve(w, r, "waybill.view")
	if !ok {
		return
	}
	var out json.RawMessage
	err := h.DB.QueryRow(r.Context(), `
		SELECT COALESCE(json_agg(json_build_object(
		    'id', m.id::text, 'waybill', m.waybill_id::text, 'waybill_no', COALESCE(w.waybill_no,''),
		    'driver', m.driver_id::text, 'driver_name', COALESCE(d.name,''),
		    'template', m.template_id::text, 'title', m.title, 'content', m.content,
		    'ack_required', m.ack_required, 'status', m.status,
		    'sent_at', m.sent_at, 'acknowledged_at', m.acknowledged_at
		  ) ORDER BY m.sent_at DESC), '[]'::json)
		FROM ops_driver_reminder m
		LEFT JOIN ops_waybill w ON w.id = m.waybill_id
		LEFT JOIN md_driver d ON d.id = m.driver_id
		WHERE m.waybill_id=$1::uuid`, id).Scan(&out)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// SendReminder POST /api/v1/waybills/{no}/reminders —— 给这张运单的司机发作业提醒。
//
// 运单详情页上那颗「发送提醒」按钮打的就是这里，而此前只注册了 GET，恒定 405。
// 司机端那一侧一直是全的：/driver/tasks 会把待确认的提醒当强制弹窗推给司机，
// /driver/reminders/{id}/ack 收确认。收的一头做好了，发的一头没有——
// 「装车前先拍照」「这批货不能倒放」发不出去，调度员只能打电话，
// 而电话说过的话，出事时谁都不认。
func (h *Handler) SendReminder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _, ok := h.resolve(w, r, "waybill.manage")
	if !ok {
		return
	}
	var body struct {
		Template    string `json:"template"`
		Title       string `json:"title"`
		Content     string `json:"content"`
		AckRequired *bool  `json:"ack_required"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// 模板只是把正文填进来的快捷方式；正文才是发给司机的东西。
	content := strings.TrimSpace(body.Content)
	var tplID any
	if body.Template != "" {
		if _, err := uuid.Parse(body.Template); err == nil {
			var tplContent string
			if h.DB.QueryRow(ctx, `SELECT content FROM ops_reminder_template
				WHERE id=$1::uuid AND NOT is_deleted`, body.Template).Scan(&tplContent) == nil {
				tplID = body.Template
				if content == "" {
					content = strings.TrimSpace(tplContent)
				}
			}
		}
	}
	if content == "" {
		httpx.Err(w, http.StatusBadRequest, "REMINDER_CONTENT", "提醒内容不能为空。")
		return
	}

	// 提醒是发给**人**的：司机端按 driver_id 拉待办，绑不上就永远收不到。
	// 与其落一条没人会看到的记录，不如直接说清楚。
	var driverID *string
	_ = h.DB.QueryRow(ctx, `SELECT driver_id::text FROM ops_waybill WHERE id=$1::uuid`, id).Scan(&driverID)
	if driverID == nil || *driverID == "" {
		httpx.Err(w, http.StatusBadRequest, "NO_DRIVER",
			"该运单还没有指派司机，提醒发不出去。请先指派司机。")
		return
	}

	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = "作业提醒"
	}
	ack := true
	if body.AckRequired != nil {
		ack = *body.AckRequired
	}
	rid, _ := uuid.NewV7()
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO ops_driver_reminder (id, created_at, updated_at, waybill_id, driver_id,
		  template_id, title, content, ack_required, status, level, sent_at)
		VALUES ($1, now(), now(), $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, '`+wbstatus.ReminderPending+`', 'important', now())`,
		rid.String(), id, *driverID, tplID, title, content, ack); err != nil {
		httpx.Fail(w, r, "INTERNAL", "写入失败", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id": rid.String(), "title": title, "content": content,
		"ack_required": ack, "status": wbstatus.ReminderPending,
	})
}

var cardCST = time.FixedZone("CST", 8*3600)

var exceptionTypeLabel = map[string]string{
	"transit_delay": "在途超时", "route_deviation": "偏航/路线异常", "cargo_damage": "货损货差",
	"vehicle_breakdown": "车辆故障", "detained": "扣车扣货", "customer_complaint": "客户投诉",
	"temperature": "冷链温度异常", "fuel": "油耗/漏油异常", "overspeed": "超速驾驶",
	"fatigue": "疲劳驾驶", "deviation": "偏航（车联网）", "abnormal_stop": "异常停车",
	"geofence": "围栏进出", "offline": "设备离线", "receipt_pending": "回单待确认", "other": "其他",
}

func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func trimSpace(s string) string {
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	return s
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
