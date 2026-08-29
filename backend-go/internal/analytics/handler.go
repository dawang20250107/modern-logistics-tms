package analytics

// 经营看板：GET /analytics/dashboard —— 对齐 apps/analytics 指标中台的 13 卡 + 5 趋势。
// 口径逐指标翻译自 definitions.py；日期语义 AT TIME ZONE 'Asia/Shanghai' 对齐 __date。

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
}

var cstZone = time.FixedZone("CST", 8*3600)

var wbStatusLabel = map[string]string{
	"draft": "草稿", "pending_dispatch": "待调度", "dispatched": "已派车", "loaded": "已装车",
	"departed": "已发车", "in_transit": "运输中", "arrived": "已到达", "partially_signed": "部分签收",
	"rejected": "已拒收", "signed": "已签收", "delivered": "已送达", "settled": "已结算",
	"cancelled": "已取消", "voided": "已作废",
}
var alertTypeLabel = map[string]string{
	"overspeed": "超速", "fatigue": "疲劳驾驶", "deviation": "偏航", "abnormal_stop": "异常停车",
	"geofence": "围栏进出", "temperature": "温度异常", "fuel": "油量异常", "offline": "设备离线",
}
var orderChannelLabel = map[string]string{
	"cs": "客服代下", "self": "客户自助", "miniprogram": "小程序", "wechat_group": "微信群", "api": "开放API",
}

func hasPerm(perms []string, want string) bool {
	for _, p := range perms {
		if p == "*" || p == want {
			return true
		}
	}
	return false
}

// dateRange 对齐 definitions._range：end 默认今天（项目时区），start 默认 end-30d
func dateRange(startQ, endQ string) (string, string) {
	end := time.Now().In(cstZone)
	e := end.Format("2006-01-02")
	if t, err := time.Parse("2006-01-02", endQ); err == nil {
		e = t.Format("2006-01-02")
	}
	et, _ := time.Parse("2006-01-02", e)
	s := et.AddDate(0, 0, -30).Format("2006-01-02")
	if t, err := time.Parse("2006-01-02", startQ); err == nil {
		s = t.Format("2006-01-02")
	}
	return s, e
}

const cstDate = "AT TIME ZONE 'Asia/Shanghai'"

func (h *Handler) breakdown(ctx context.Context, sql string, labels map[string]string, args ...any) []map[string]any {
	rows, err := h.DB.Query(ctx, sql, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var key string
		var c int
		if rows.Scan(&key, &c) != nil {
			return out
		}
		k, lbl := key, labels[key]
		if k == "" {
			k, lbl = "未知", "未知"
		} else if lbl == "" {
			lbl = k
		}
		out = append(out, map[string]any{"key": k, "label": lbl, "value": c})
	}
	return out
}

func card(code, name, unit, domain string, value any, extra map[string]any) map[string]any {
	m := map[string]any{"code": code, "name": name, "unit": unit, "domain": domain, "value": value}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func rate(num, den int) float64 {
	if den == 0 {
		return 0.0
	}
	// 对齐 Python round(x, 4)（银行家舍入在 4 位小数上的实际数据几乎不可分辨；SQL 侧同口径）
	v := float64(num) / float64(den)
	return float64(int64(v*10000+0.5)) / 10000
}

// Dashboard GET /api/v1/analytics/dashboard?trends=true&start=&end=
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil || !hasPerm(perms, "analytics.view") {
		httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "无经营分析查看权限")
		return
	}
	q := r.URL.Query()
	s, e := dateRange(q.Get("start"), q.Get("end"))
	withTrends := false
	switch q.Get("trends") {
	case "1", "true", "yes", "True", "TRUE":
		withTrends = true
	}

	metrics := []map[string]any{}

	// ops.waybill_count（range，默认维度 status）
	var wbCount int
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_waybill
		WHERE (created_at `+cstDate+`)::date BETWEEN $1::date AND $2::date`, s, e).Scan(&wbCount)
	metrics = append(metrics, card("ops.waybill_count", "运单量", "单", "ops", wbCount, map[string]any{
		"breakdown": h.breakdown(ctx, `SELECT status, count(*) FROM ops_waybill
			WHERE (created_at `+cstDate+`)::date BETWEEN $1::date AND $2::date
			GROUP BY status ORDER BY count(*) DESC`, wbStatusLabel, s, e),
	}))

	// ops.in_transit（snapshot）
	var inTransit int
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_waybill WHERE status='in_transit'`).Scan(&inTransit)
	metrics = append(metrics, card("ops.in_transit", "在途运单", "单", "ops", inTransit, nil))

	// ops.on_time_rate（range）
	var otNum, otDen int
	_ = h.DB.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE arrived_at <= planned_arrival), count(*)
		FROM ops_waybill
		WHERE status IN `+wbstatus.DeliveredSQL+`
		  AND planned_arrival IS NOT NULL AND arrived_at IS NOT NULL
		  AND (created_at `+cstDate+`)::date BETWEEN $1::date AND $2::date`, s, e).Scan(&otNum, &otDen)
	metrics = append(metrics, card("ops.on_time_rate", "准班率", "%", "ops", rate(otNum, otDen),
		map[string]any{"numerator": otNum, "denominator": otDen}))

	// ops.risk_rate（snapshot）
	var riskNum, riskDen int
	_ = h.DB.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE risk_level IN ('high','medium')), count(*)
		FROM ops_waybill WHERE status NOT IN ('settled','cancelled','voided')`).Scan(&riskNum, &riskDen)
	metrics = append(metrics, card("ops.risk_rate", "风险运单占比", "%", "ops", rate(riskNum, riskDen),
		map[string]any{"numerator": riskNum, "denominator": riskDen}))

	// fleet.online_rate（snapshot）
	var onlNum, onlDen int
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE online), count(*) FROM tel_vehicle_state`).Scan(&onlNum, &onlDen)
	metrics = append(metrics, card("fleet.online_rate", "运力在线率", "%", "fleet", rate(onlNum, onlDen),
		map[string]any{"numerator": onlNum, "denominator": onlDen}))

	// fleet.utilization_rate（snapshot）
	var busy, vehTotal int
	_ = h.DB.QueryRow(ctx, `SELECT count(DISTINCT vehicle_id) FROM ops_waybill
		WHERE status IN ('dispatched','loaded','departed','in_transit') AND vehicle_id IS NOT NULL`).Scan(&busy)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM md_vehicle WHERE is_active AND NOT is_deleted`).Scan(&vehTotal)
	metrics = append(metrics, card("fleet.utilization_rate", "运力利用率", "%", "fleet", rate(busy, vehTotal),
		map[string]any{"numerator": busy, "denominator": vehTotal}))

	// fleet.alert_count（range，默认维度 alert_type）
	var alertCount int
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM tel_alert
		WHERE (triggered_at `+cstDate+`)::date BETWEEN $1::date AND $2::date`, s, e).Scan(&alertCount)
	metrics = append(metrics, card("fleet.alert_count", "报警数", "条", "fleet", alertCount, map[string]any{
		"breakdown": h.breakdown(ctx, `SELECT alert_type, count(*) FROM tel_alert
			WHERE (triggered_at `+cstDate+`)::date BETWEEN $1::date AND $2::date
			GROUP BY alert_type ORDER BY count(*) DESC`, alertTypeLabel, s, e),
	}))

	// order.count（range，默认维度 channel；Order 软删管理器默认排除 is_deleted）
	var orderCount int
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order
		WHERE NOT is_deleted AND (created_at `+cstDate+`)::date BETWEEN $1::date AND $2::date`, s, e).Scan(&orderCount)
	metrics = append(metrics, card("order.count", "订单量", "单", "order", orderCount, map[string]any{
		"breakdown": h.breakdown(ctx, `SELECT channel, count(*) FROM ops_order
			WHERE NOT is_deleted AND (created_at `+cstDate+`)::date BETWEEN $1::date AND $2::date
			GROUP BY channel ORDER BY count(*) DESC`, orderChannelLabel, s, e),
	}))

	// order.conversion_rate（range）
	var convNum, convDen int
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='converted'), count(*)
		FROM ops_order WHERE NOT is_deleted
		  AND (created_at `+cstDate+`)::date BETWEEN $1::date AND $2::date`, s, e).Scan(&convNum, &convDen)
	metrics = append(metrics, card("order.conversion_rate", "订单转化率", "%", "order", rate(convNum, convDen),
		map[string]any{"numerator": convNum, "denominator": convDen}))

	// order.sla_on_time_rate（range，按 delivered_at）
	var slaNum, slaDen int
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE sla_status='on_time'), count(*)
		FROM ops_order WHERE NOT is_deleted AND status='completed'
		  AND delivered_at IS NOT NULL
		  AND (delivered_at `+cstDate+`)::date BETWEEN $1::date AND $2::date
		  AND sla_status IN ('on_time','breached')`, s, e).Scan(&slaNum, &slaDen)
	metrics = append(metrics, card("order.sla_on_time_rate", "SLA 准时率", "%", "order", rate(slaNum, slaDen),
		map[string]any{"numerator": slaNum, "denominator": slaDen}))

	// finance.*（range，读侧聚合——财务写路径冻结不受影响）
	var recvTotal, payTotal, stmtDiff float64
	_ = h.DB.QueryRow(ctx, `SELECT COALESCE(sum(amount),0)::float8 FROM fin_expense_record
		WHERE direction='receivable' AND (occurred_at `+cstDate+`)::date BETWEEN $1::date AND $2::date`, s, e).Scan(&recvTotal)
	_ = h.DB.QueryRow(ctx, `SELECT COALESCE(sum(amount),0)::float8 FROM fin_expense_record
		WHERE direction='payable' AND (occurred_at `+cstDate+`)::date BETWEEN $1::date AND $2::date`, s, e).Scan(&payTotal)
	_ = h.DB.QueryRow(ctx, `SELECT (COALESCE(sum(total_amount),0) - COALESCE(sum(external_total),0))::float8
		FROM fin_statement WHERE (created_at `+cstDate+`)::date BETWEEN $1::date AND $2::date`, s, e).Scan(&stmtDiff)
	metrics = append(metrics,
		card("finance.receivable_total", "应收总额", "元", "finance", recvTotal, nil),
		card("finance.payable_total", "应付总额", "元", "finance", payTotal, nil),
		card("finance.statement_diff_total", "对账差异合计", "元", "finance", stmtDiff, nil),
	)

	// 与 DASHBOARD_METRICS 顺序一致重排（上面按查询组织，输出按 Django 列表序）
	orderIdx := map[string]int{
		"ops.waybill_count": 0, "ops.in_transit": 1, "ops.on_time_rate": 2, "ops.risk_rate": 3,
		"fleet.online_rate": 4, "fleet.utilization_rate": 5, "fleet.alert_count": 6,
		"order.count": 7, "order.conversion_rate": 8, "order.sla_on_time_rate": 9,
		"finance.receivable_total": 10, "finance.payable_total": 11, "finance.statement_diff_total": 12,
	}
	sorted := make([]map[string]any, len(metrics))
	for _, m := range metrics {
		sorted[orderIdx[m["code"].(string)]] = m
	}

	result := map[string]any{"metrics": sorted}
	if withTrends {
		trends := map[string]any{}
		for _, code := range []string{"ops.waybill_count", "fleet.alert_count", "order.count", "finance.receivable_total", "finance.payable_total"} {
			end := time.Now().In(cstZone)
			start := end.AddDate(0, 0, -14)
			rows, err := h.DB.Query(ctx, `SELECT stat_date::text, value::float8 FROM ana_metric_snapshot
				WHERE metric_code=$1 AND dimension_key='' AND stat_date BETWEEN $2::date AND $3::date
				ORDER BY stat_date`, code, start.Format("2006-01-02"), end.Format("2006-01-02"))
			if err != nil {
				continue
			}
			series := []map[string]any{}
			for rows.Next() {
				var d string
				var v float64
				if rows.Scan(&d, &v) != nil {
					break
				}
				series = append(series, map[string]any{"date": d, "value": v})
			}
			rows.Close()
			trends[code] = series
		}
		result["trends"] = trends
	}
	httpx.JSON(w, http.StatusOK, result)
}
