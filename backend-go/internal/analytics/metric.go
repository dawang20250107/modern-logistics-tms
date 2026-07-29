package analytics

// 单指标计算入口（对齐 apps/analytics/registry.compute_metric）：
// 供 AI 工具 analytics.query_metric 调用。口径与 Dashboard 逐条一致——
// 二者共用下方 metricSpecs 里的同一份 SQL，避免看板与 AI 回答对不上账。

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type metricSpec struct {
	Name, Unit, Domain string
	// Ratio 为 true 时按分子/分母计算比率并附 numerator/denominator
	Ratio bool
	// SQL 取值：Ratio 指标返回 (分子, 分母)；否则返回单值
	SQL string
	// Snapshot 指标忽略时间范围
	Snapshot bool
	// Float 为 true 时值是 Python float（字符串形态带 .0）；否则是 count() 的 int
	Float bool
}

// metricSpecs 与 apps/analytics/definitions.py 的 13 个指标一一对应
var metricSpecs = map[string]metricSpec{
	"ops.waybill_count": {Name: "运单量", Unit: "单", Domain: "ops",
		SQL: `SELECT count(*)::float8 FROM ops_waybill
		      WHERE (created_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date`},
	"ops.in_transit": {Name: "在途运单", Unit: "单", Domain: "ops", Snapshot: true,
		SQL: `SELECT count(*)::float8 FROM ops_waybill WHERE status='in_transit'`},
	"ops.on_time_rate": {Name: "准班率", Unit: "%", Domain: "ops", Ratio: true,
		SQL: `SELECT count(*) FILTER (WHERE arrived_at <= planned_arrival)::float8, count(*)::float8
		      FROM ops_waybill WHERE status IN ('arrived','signed','delivered','settled')
		        AND planned_arrival IS NOT NULL AND arrived_at IS NOT NULL
		        AND (created_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date`},
	"ops.risk_rate": {Name: "风险运单占比", Unit: "%", Domain: "ops", Ratio: true, Snapshot: true,
		SQL: `SELECT count(*) FILTER (WHERE risk_level IN ('high','medium'))::float8, count(*)::float8
		      FROM ops_waybill WHERE status NOT IN ('settled','cancelled','voided')`},
	"fleet.online_rate": {Name: "运力在线率", Unit: "%", Domain: "fleet", Ratio: true, Snapshot: true,
		SQL: `SELECT count(*) FILTER (WHERE online)::float8, count(*)::float8 FROM tel_vehicle_state`},
	"fleet.utilization_rate": {Name: "运力利用率", Unit: "%", Domain: "fleet", Ratio: true, Snapshot: true,
		SQL: `SELECT (SELECT count(DISTINCT vehicle_id) FROM ops_waybill
		              WHERE status IN ('dispatched','loaded','departed','in_transit') AND vehicle_id IS NOT NULL)::float8,
		             (SELECT count(*) FROM md_vehicle WHERE is_active AND NOT is_deleted)::float8`},
	"fleet.alert_count": {Name: "报警数", Unit: "条", Domain: "fleet",
		SQL: `SELECT count(*)::float8 FROM tel_alert
		      WHERE (triggered_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date`},
	"order.count": {Name: "订单量", Unit: "单", Domain: "order",
		SQL: `SELECT count(*)::float8 FROM ops_order WHERE NOT is_deleted
		        AND (created_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date`},
	"order.sla_on_time_rate": {Name: "SLA 准时率", Unit: "%", Domain: "order", Ratio: true,
		SQL: `SELECT count(*) FILTER (WHERE sla_status='on_time')::float8, count(*)::float8
		      FROM ops_order WHERE NOT is_deleted AND status='completed' AND delivered_at IS NOT NULL
		        AND (delivered_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date
		        AND sla_status IN ('on_time','breached')`},
	"order.conversion_rate": {Name: "订单转化率", Unit: "%", Domain: "order", Ratio: true,
		SQL: `SELECT count(*) FILTER (WHERE status='converted')::float8, count(*)::float8
		      FROM ops_order WHERE NOT is_deleted
		        AND (created_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date`},
	"finance.receivable_total": {Float: true, Name: "应收总额", Unit: "元", Domain: "finance",
		SQL: `SELECT COALESCE(sum(amount),0)::float8 FROM fin_expense_record WHERE direction='receivable'
		        AND (occurred_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date`},
	"finance.payable_total": {Float: true, Name: "应付总额", Unit: "元", Domain: "finance",
		SQL: `SELECT COALESCE(sum(amount),0)::float8 FROM fin_expense_record WHERE direction='payable'
		        AND (occurred_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date`},
	"finance.statement_diff_total": {Float: true, Name: "对账差异合计", Unit: "元", Domain: "finance",
		SQL: `SELECT (COALESCE(sum(total_amount),0) - COALESCE(sum(external_total),0))::float8
		      FROM fin_statement
		      WHERE (created_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date`},
}

// ComputeMetric 计算单个指标；days<=0 时取默认 30 天窗口
func ComputeMetric(ctx context.Context, db *pgxpool.Pool, code string, days int) (map[string]any, error) {
	spec, ok := metricSpecs[code]
	if !ok {
		return nil, fmt.Errorf("UNKNOWN_METRIC")
	}
	if days <= 0 {
		days = 30
	}
	end := time.Now().In(cstZone)
	start := end.AddDate(0, 0, -days)
	s, e := start.Format("2006-01-02"), end.Format("2006-01-02")

	out := map[string]any{"code": code, "name": spec.Name, "unit": spec.Unit, "domain": spec.Domain}
	args := []any{}
	if !spec.Snapshot {
		args = append(args, s, e)
	}
	if spec.Ratio {
		var num, den float64
		if err := db.QueryRow(ctx, spec.SQL, args...).Scan(&num, &den); err != nil {
			return nil, err
		}
		out["value"] = rate(int(num), int(den)) // 比率恒为 float
		out["numerator"] = int(num)
		out["denominator"] = int(den)
		return out, nil
	}
	var v float64
	if err := db.QueryRow(ctx, spec.SQL, args...).Scan(&v); err != nil {
		return nil, err
	}
	if spec.Float {
		out["value"] = v
	} else {
		out["value"] = int64(v) // 计数类指标 Django 走 count()，是整数
	}
	return out, nil
}
