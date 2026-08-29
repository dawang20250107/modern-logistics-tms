package masterdata

// 主数据域的自定义动作：
//   GET  /customers/{id}/context        客服工作台的客户上下文
//   GET  /customers/{id}/lane-suggest   建单补全（常见货物 / 参考价区间 / 历史收货方）
//   GET  /carriers/{id}/performance     承运商经营表现（可按线路聚焦）+ 常跑线路
//   POST /carriers/{id}/blacklist       拉黑 / 解除拉黑
//   POST /drivers/{id}/refresh-stats    刷新司机累计运单与运费
//   GET  /drivers/lookup                姓名 + 身份证后 6 位检索司机档案与证件
//
// 对齐 apps/ops/customer_ctx.py、carrier_scoring.py、stats.py、credential_ocr.match_driver。

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

// pyRound 复刻 Python round(float, n)：十进制半偶入（Go 的 FormatFloat 同口径）
func pyRound(v float64, n int) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', n, 64), 64)
	return f
}

// resolveID 取 {id} 并确认对象存在（软删的按不存在处理）；失败时已写 404
func (h *Handler) resolveID(w http.ResponseWriter, r *http.Request, table, model string) (string, bool) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No "+model+" matches the given query.")
		return "", false
	}
	var exists bool
	_ = h.DB.QueryRow(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM "+table+" WHERE id=$1::uuid AND NOT is_deleted)", id).Scan(&exists)
	if !exists {
		httpx.Err(w, http.StatusNotFound, "error", "No "+model+" matches the given query.")
		return "", false
	}
	return id, true
}

// customerContextSQL 一条主 SQL 出齐客户上下文的全部区块。
// 地址/线路的并列名次按「最近一单更新」决胜，对齐 Python Counter.most_common
// 在等频时保持插入序（订单按 -created_at 遍历，故先出现的是最近下单的那个）。
const customerContextSQL = `
WITH o AS (
  SELECT * FROM ops_order
  WHERE customer_id = $1::uuid AND NOT is_deleted AND status <> 'cancelled'
),
outstanding AS (
  SELECT COALESCE(sum(e.amount), 0) AS amt
  FROM fin_expense_record e JOIN ops_waybill w ON w.id = e.waybill_id
  WHERE e.direction = 'receivable' AND w.customer_id = $1::uuid AND w.status <> 'settled'
)
SELECT
  (SELECT count(*) FROM o) AS total,
  (SELECT count(*) FROM o WHERE status IN ('pending_confirm','confirmed','pooled','dispatching')) AS open_cnt,
  (SELECT amt FROM outstanding) AS outstanding,
  (SELECT count(*) FROM ops_waybill w WHERE w.customer_id = $1::uuid
     AND EXISTS (SELECT 1 FROM ops_exception x WHERE x.waybill_id = w.id)
     AND NOT EXISTS (SELECT 1 FROM ops_exception x WHERE x.waybill_id = w.id AND x.status = 'resolved')) AS exc_cnt,
  (SELECT count(*) FROM ops_waybill w WHERE w.customer_id = $1::uuid
     AND w.status IN ('signed','delivered')
     AND w.receipt_status NOT IN ('returned','audited')) AS receipt_pending,
  COALESCE((SELECT json_agg(rt ORDER BY n DESC, last_at DESC) FROM (
     SELECT COALESCE(NULLIF(origin,''),'?')||'→'||COALESCE(NULLIF(destination,''),'?') AS rt,
            count(*) AS n, max(created_at) AS last_at
     FROM o WHERE origin <> '' OR destination <> ''
     GROUP BY 1 ORDER BY n DESC, last_at DESC LIMIT 5) t), '[]'::json) AS common_routes,
  COALESCE((SELECT json_agg(json_build_object(
       'address', address, 'contact_name', contact_name, 'contact_phone', contact_phone, 'count', n)
       ORDER BY n DESC, last_at DESC) FROM (
     SELECT pickup_address AS address,
            (array_agg(pickup_contact_name ORDER BY created_at))[1] AS contact_name,
            (array_agg(pickup_contact_phone ORDER BY created_at))[1] AS contact_phone,
            count(*) AS n, max(created_at) AS last_at
     FROM o WHERE pickup_address <> '' GROUP BY 1 ORDER BY n DESC, last_at DESC LIMIT 5) t), '[]'::json) AS pickups,
  COALESCE((SELECT json_agg(json_build_object(
       'address', address, 'contact_name', contact_name, 'contact_phone', contact_phone, 'count', n)
       ORDER BY n DESC, last_at DESC) FROM (
     SELECT delivery_address AS address,
            (array_agg(delivery_contact_name ORDER BY created_at))[1] AS contact_name,
            (array_agg(delivery_contact_phone ORDER BY created_at))[1] AS contact_phone,
            count(*) AS n, max(created_at) AS last_at
     FROM o WHERE delivery_address <> '' GROUP BY 1 ORDER BY n DESC, last_at DESC LIMIT 5) t), '[]'::json) AS deliveries,
  COALESCE((SELECT json_agg(b ORDER BY created_at DESC) FROM (
     SELECT ` + orderBriefJSON + ` AS b, created_at FROM o ORDER BY created_at DESC, id LIMIT 5) t), '[]'::json) AS recent,
  COALESCE((SELECT json_agg(b ORDER BY created_at DESC) FROM (
     SELECT ` + orderBriefJSON + ` AS b, created_at FROM o
     WHERE status IN ('pending_confirm','confirmed','pooled','dispatching')
     ORDER BY created_at DESC, id LIMIT 8) t), '[]'::json) AS open_list`

// orderBriefJSON 对齐 customer_ctx._order_brief
const orderBriefJSON = `json_build_object(
  'order_no', order_no,
  'status', status,
  'status_label', (CASE status WHEN 'draft' THEN '草稿' WHEN 'pending_confirm' THEN '待确认'
       WHEN 'confirmed' THEN '已确认' WHEN 'pooled' THEN '订单池' WHEN 'dispatching' THEN '调度中'
       WHEN 'converted' THEN '已派单' WHEN 'completed' THEN '已完成' WHEN 'cancelled' THEN '已取消'
       ELSE status END),
  'route', COALESCE(NULLIF(origin,''),'?')||'→'||COALESCE(NULLIF(destination,''),'?'),
  'cargo', cargo_desc,
  'quoted_amount', COALESCE(quoted_amount, 0)::float8,
  'created_at', to_char(created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD"T"HH24:MI:SS.US+08:00'))`

// CustomerContext GET /api/v1/customers/{id}/context
func (h *Handler) CustomerContext(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveID(w, r, "md_customer", "Customer")
	if !ok {
		return
	}
	ctx := r.Context()
	var name, settlement string
	var creditLimit float64
	var creditDays, billingDay int
	if err := h.DB.QueryRow(ctx, `SELECT name, COALESCE(settlement_type,''),
		COALESCE(credit_limit,0)::float8, credit_days, billing_day
		FROM md_customer WHERE id=$1::uuid`, id).
		Scan(&name, &settlement, &creditLimit, &creditDays, &billingDay); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取客户失败")
		return
	}
	var total, openCnt, excCnt, receiptPending int
	var outstanding float64
	var routes, pickups, deliveries, recent, openList any
	if err := h.DB.QueryRow(ctx, customerContextSQL, id).Scan(
		&total, &openCnt, &outstanding, &excCnt, &receiptPending,
		&routes, &pickups, &deliveries, &recent, &openList); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取客户上下文失败："+err.Error())
		return
	}
	credit := map[string]any{
		"limit": creditLimit, "outstanding": outstanding,
		"available": nil, "used_pct": nil,
		"over_limit": creditLimit != 0 && outstanding > creditLimit,
	}
	if creditLimit != 0 {
		credit["available"] = creditLimit - outstanding
		credit["used_pct"] = pyRound(outstanding/creditLimit, 3)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"customer_id": id, "name": name,
		"profile": map[string]any{
			"settlement_type": settlement, "credit_limit": creditLimit,
			"credit_days": creditDays, "billing_day": billingDay,
		},
		"credit":            credit,
		"common_routes":     routes,
		"common_pickups":    pickups,
		"common_deliveries": deliveries,
		"recent_orders":     recent,
		"open_orders":       openList,
		"counts": map[string]any{
			"total": total, "open": openCnt,
			"exceptions": excCnt, "receipt_pending": receiptPending,
		},
	})
}

// CustomerLaneSuggest GET /api/v1/customers/{id}/lane-suggest?origin=&destination=
func (h *Handler) CustomerLaneSuggest(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveID(w, r, "md_customer", "Customer")
	if !ok {
		return
	}
	ctx := r.Context()
	origin := r.URL.Query().Get("origin")
	dest := r.URL.Query().Get("destination")

	// 订单侧：最近 100 单里的常见货物、报价区间、历史收货方
	var cargo, deliveries any
	var quoteMin, quoteMax *float64
	err := h.DB.QueryRow(ctx, `
		WITH o AS (
		  SELECT * FROM ops_order
		  WHERE customer_id=$1::uuid AND NOT is_deleted AND status <> 'cancelled'
		    AND ($2 = '' OR origin = $2) AND ($3 = '' OR destination = $3)
		  ORDER BY created_at DESC, id LIMIT 100
		)
		SELECT
		  COALESCE((SELECT json_agg(cd ORDER BY n DESC, last_at DESC) FROM (
		     SELECT cargo_desc AS cd, count(*) AS n, max(created_at) AS last_at
		     FROM o WHERE cargo_desc <> '' GROUP BY 1 ORDER BY n DESC, last_at DESC LIMIT 5) t), '[]'::json),
		  (SELECT min(quoted_amount)::float8 FROM o WHERE quoted_amount IS NOT NULL AND quoted_amount <> 0),
		  (SELECT max(quoted_amount)::float8 FROM o WHERE quoted_amount IS NOT NULL AND quoted_amount <> 0),
		  COALESCE((SELECT json_agg(json_build_object(
		       'address', address, 'contact_name', contact_name, 'contact_phone', contact_phone, 'count', n)
		       ORDER BY n DESC, last_at DESC) FROM (
		     SELECT delivery_address AS address,
		            (array_agg(delivery_contact_name ORDER BY created_at))[1] AS contact_name,
		            (array_agg(delivery_contact_phone ORDER BY created_at))[1] AS contact_phone,
		            count(*) AS n, max(created_at) AS last_at
		     FROM o WHERE delivery_address <> '' GROUP BY 1 ORDER BY n DESC, last_at DESC LIMIT 5) t), '[]'::json)
		`, id, origin, dest).Scan(&cargo, &quoteMin, &quoteMax, &deliveries)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取线路建议失败："+err.Error())
		return
	}

	// 线路价库参考价（承运侧成本）：起终点都给了才收窄，否则取全量在用价目
	var laneMin, laneMax *float64
	_ = h.DB.QueryRow(ctx, `
		SELECT min(standard_price)::float8, max(standard_price)::float8
		FROM md_carrier_lane_price
		WHERE is_active AND NOT is_deleted AND standard_price IS NOT NULL AND standard_price <> 0
		  AND ($1 = '' OR $2 = '' OR (origin_city = $1 AND dest_city = $2))`, origin, dest).
		Scan(&laneMin, &laneMax)

	band := bandOf(quoteMin, quoteMax)
	if band == nil {
		band = bandOf(laneMin, laneMax)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"common_cargo":      cargo,
		"price_band":        band,
		"cost_reference":    bandOf(laneMin, laneMax),
		"common_deliveries": deliveries,
	})
}

// bandOf 把最小/最大值折成 Python round() 后的整数区间；无样本返回 nil
func bandOf(lo, hi *float64) []float64 {
	if lo == nil || hi == nil {
		return nil
	}
	return []float64{pyRound(*lo, 0), pyRound(*hi, 0)}
}

// CarrierPerformance GET /api/v1/carriers/{id}/performance?origin=&destination=
func (h *Handler) CarrierPerformance(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveID(w, r, "md_carrier", "Carrier")
	if !ok {
		return
	}
	ctx := r.Context()
	origin := r.URL.Query().Get("origin")
	dest := r.URL.Query().Get("destination")

	var total, timedTotal, onTimeHits, excTotal, doneTotal, receiptHits, routeHits int
	var lanePayable *float64
	err := h.DB.QueryRow(ctx, `
		WITH w AS (
		  SELECT * FROM ops_waybill
		  WHERE carrier_id=$1::uuid AND created_at >= now() - interval '90 days' AND status <> 'voided'
		),
		route AS (
		  -- 起终点缺一即视为「不聚焦线路」，对齐 Django 的 qs.none()
		  SELECT * FROM w WHERE $2 <> '' AND $3 <> '' AND origin = $2 AND destination = $3
		)
		SELECT
		  (SELECT count(*) FROM w),
		  -- 同 suggestion.go：准班率只从真的送达过的单里取样，
		  -- 否则「发车后取消」那条路径留下的假 arrived_at 会算成准点交付。
		  (SELECT count(*) FROM w WHERE status IN `+wbstatus.DeliveredSQL+`
		     AND planned_arrival IS NOT NULL AND arrived_at IS NOT NULL),
		  (SELECT count(*) FROM w WHERE status IN `+wbstatus.DeliveredSQL+`
		     AND planned_arrival IS NOT NULL AND arrived_at IS NOT NULL
		     AND arrived_at <= planned_arrival),
		  (SELECT count(*) FROM w WHERE EXISTS (SELECT 1 FROM ops_exception x WHERE x.waybill_id = w.id)),
		  (SELECT count(*) FROM w WHERE status IN `+wbstatus.DeliveredSQL+`),
		  (SELECT count(*) FROM w WHERE status IN `+wbstatus.DeliveredSQL+`
		     AND receipt_status IN ('returned','audited')),
		  (SELECT count(*) FROM route),
		  -- 本线路应付均价：先按运单汇总再取均值（对齐 annotate(Sum) + aggregate(Avg)），
		  -- 没有应付费用的运单在 Django 侧汇总为 NULL，不进入均值
		  (SELECT avg(pay)::float8 FROM (
		     SELECT (SELECT sum(e.amount) FROM fin_expense_record e
		             WHERE e.waybill_id = route.id AND e.direction='payable') AS pay
		     FROM route) s WHERE pay IS NOT NULL)
		`, id, origin, dest).Scan(&total, &timedTotal, &onTimeHits, &excTotal, &doneTotal, &receiptHits,
		&routeHits, &lanePayable)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取承运商表现失败："+err.Error())
		return
	}

	out := map[string]any{
		"deals":        total,
		"route_hits":   routeHits,
		"on_time_rate": ratioOr(onTimeHits, timedTotal, 0.85),
		// 基线取 Django 的 1 - _BASELINE["low_exception"]：Go 的无类型常量会精确折成 0.1，
		// 而 Python 在 IEEE754 下算出的是 0.09999999999999998，故显式走 float64 运算
		"exception_rate":      ratioOr(excTotal, total, excBaseline),
		"receipt_timely_rate": ratioOr(receiptHits, doneTotal, 0.88),
		"recent_deal_price":   nil,
		"has_history":         total > 0,
	}
	if lanePayable != nil {
		out["recent_deal_price"] = pyRound(*lanePayable, 2)
	}
	rows, err := h.DB.Query(ctx, `
		SELECT origin, destination, count(*)::int AS deals FROM ops_waybill
		WHERE carrier_id=$1::uuid AND created_at >= now() - interval '90 days'
		  AND status <> 'voided' AND origin <> '' AND destination <> ''
		GROUP BY origin, destination ORDER BY deals DESC, origin, destination LIMIT 5`, id)
	freq := []map[string]any{}
	if err == nil {
		for rows.Next() {
			var o, d string
			var n int
			if rows.Scan(&o, &d, &n) != nil {
				break
			}
			freq = append(freq, map[string]any{"origin": o, "destination": d, "deals": n})
		}
		rows.Close()
	}
	out["frequent_routes"] = freq
	httpx.JSON(w, http.StatusOK, out)
}

var lowExceptionBaseline = 0.90
var excBaseline = float64(1) - lowExceptionBaseline

// ratioOr 对齐 carrier_scoring._rate：无样本时回落基线
func ratioOr(num, den int, def float64) float64 {
	if den == 0 {
		return def
	}
	return pyRound(float64(num)/float64(den), 4)
}

// CarrierBlacklist POST /api/v1/carriers/{id}/blacklist {blacklisted, reason}
func (h *Handler) CarrierBlacklist(w http.ResponseWriter, r *http.Request) {
	if !h.Allow(w, r, "carrier.manage") {
		return
	}
	id, ok := h.resolveID(w, r, "md_carrier", "Carrier")
	if !ok {
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	blacklisted := true
	if v, has := body["blacklisted"]; has {
		blacklisted = truthy(v)
	}
	reason := ""
	if v, _ := body["reason"].(string); v != "" {
		reason = strings.TrimSpace(v)
	}
	if blacklisted && reason == "" {
		httpx.Err(w, http.StatusBadRequest, "REASON_REQUIRED", "拉黑承运商需填写原因。")
		return
	}
	if !blacklisted {
		reason = ""
	}
	if _, err := h.DB.Exec(r.Context(), `
		UPDATE md_carrier SET blacklisted=$2, blacklist_reason=$3, updated_at=now()
		WHERE id=$1::uuid`, id, blacklisted, reason); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败："+err.Error())
		return
	}
	it, err := h.OneDetail(r.Context(), CarriersCfg, "ca.id = $1::uuid", id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// truthy 对齐 Python bool()：非空字符串、非零数字、true 均为真
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case nil:
		return false
	}
	return true
}

// DriverRefreshStats POST /api/v1/drivers/{id}/refresh-stats
func (h *Handler) DriverRefreshStats(w http.ResponseWriter, r *http.Request) {
	if !h.Allow(w, r, "masterdata.manage") {
		return
	}
	id, ok := h.resolveID(w, r, "md_driver", "Driver")
	if !ok {
		return
	}
	if _, err := h.DB.Exec(r.Context(), `
		UPDATE md_driver d SET
		  cumulative_waybills = (SELECT count(*) FROM ops_waybill w
		     WHERE w.driver_id = d.id AND w.status IN ('signed','delivered','settled')),
		  cumulative_freight = (SELECT COALESCE(sum(e.amount), 0) FROM fin_expense_record e
		     JOIN ops_waybill w ON w.id = e.waybill_id
		     WHERE w.driver_id = d.id AND e.direction = 'payable'),
		  updated_at = now()
		WHERE d.id = $1::uuid`, id); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "刷新失败："+err.Error())
		return
	}
	it, err := h.OneDetail(r.Context(), DriversCfg, "d.id = $1::uuid", id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// DriverLookup GET /api/v1/drivers/lookup?name=&id_tail=
//
// 要求姓名 + 恰好 6 位数字尾号且唯一命中才认；模糊即不认 —— 把证件或运单
// 错绑到同名他人，比"查不到"要贵得多。
func (h *Handler) DriverLookup(w http.ResponseWriter, r *http.Request) {
	if !h.Allow(w, r, "masterdata.view") {
		return
	}
	ctx := r.Context()
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	idTail := strings.TrimSpace(r.URL.Query().Get("id_tail"))
	miss := map[string]any{"matched": false, "driver": nil, "credentials": []any{}}
	if name == "" || len(idTail) != 6 || !allDigits(idTail) {
		httpx.JSON(w, http.StatusOK, miss)
		return
	}
	var id string
	var n int
	if h.DB.QueryRow(ctx, `SELECT count(*) FROM md_driver
		WHERE NOT is_deleted AND name=$1 AND id_no LIKE '%'||$2`, name, idTail).Scan(&n) != nil || n != 1 {
		httpx.JSON(w, http.StatusOK, miss)
		return
	}
	if h.DB.QueryRow(ctx, `SELECT id::text FROM md_driver
		WHERE NOT is_deleted AND name=$1 AND id_no LIKE '%'||$2 LIMIT 1`, name, idTail).Scan(&id) != nil {
		httpx.JSON(w, http.StatusOK, miss)
		return
	}
	driver, err := h.OneDetail(ctx, DriversCfg, "d.id = $1::uuid", id)
	if err != nil || driver == nil {
		httpx.JSON(w, http.StatusOK, miss)
		return
	}
	creds, _ := h.listBy(ctx, DriverCredsCfg, "dc.driver_id = $1::uuid", id)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"matched": true, "driver": driver, "credentials": creds,
	})
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// listBy 按条件取一组行（不分页），沿用资源的列面与默认排序
func (h *Handler) listBy(ctx context.Context, cfg ResourceCfg, where string, args ...any) ([]map[string]any, error) {
	sql := cfg.SelectSQL + " " + cfg.FromClause + " WHERE " + where + " " + cfg.DefaultOrder
	rows, err := h.DB.Query(ctx, sql, args...)
	if err != nil {
		return []map[string]any{}, err
	}
	defer rows.Close()
	return rowsToMaps(rows)
}
