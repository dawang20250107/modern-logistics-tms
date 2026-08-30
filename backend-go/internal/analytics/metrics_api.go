package analytics

// 指标中台对外的三个端点 + 数据资产目录，对齐 apps/analytics/views.py：
//   GET  /analytics/metrics              指标目录（可按 domain 过滤）
//   POST /analytics/metrics/query        多指标一次算（codes + 时间范围 + 维度）
//   GET  /analytics/metrics/{code}/trend 从物化快照取近 N 天序列
//   GET  /analytics/catalog              数据资产目录（?counts=true 带记录数）
//
// 指标口径全部复用 metric.go 里的 metricSpecs，与 /analytics/dashboard 同一份 SQL。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

// metricOrder 目录与 dashboard 的输出次序 = definitions.py 里的注册次序
var metricOrder = []string{
	"ops.waybill_count", "ops.in_transit", "ops.on_time_rate", "ops.risk_rate",
	"fleet.online_rate", "fleet.utilization_rate", "fleet.alert_count",
	"order.count", "order.sla_on_time_rate", "order.conversion_rate",
	"finance.receivable_total", "finance.payable_total", "finance.statement_diff_total",
}

// dimLabels 各维度的取值→中文标签，对齐 definitions._dim_choices
var dimLabels = map[string]map[string]string{
	"ops.waybill_count/status":     wbstatus.Label,
	"ops.waybill_count/risk_level": {"high": "高", "medium": "中", "low": "低", "none": "无"},
	"fleet.alert_count/alert_type": alertTypeLabel,
	"fleet.alert_count/level":      {"info": "提示", "medium": "中", "high": "高"},
	"order.count/channel":          orderChannelLabel,
	"order.count/status": {
		"draft": "草稿", "pending_confirm": "待确认", "confirmed": "已确认", "pooled": "订单池",
		"dispatching": "调度中", "converted": "已派单", "completed": "已完成", "cancelled": "已取消",
	},
}

// breakdownSQL 维度构成：以 $1/$2 为账期。与 metricSpecs 的主 SQL 同一套过滤条件。
var breakdownSQL = map[string]string{
	"ops.waybill_count": `SELECT %s AS k, count(*)::int AS c FROM ops_waybill
	    WHERE (created_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date
	    GROUP BY 1 ORDER BY c DESC`,
	"fleet.alert_count": `SELECT %s AS k, count(*)::int AS c FROM tel_alert
	    WHERE (triggered_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date
	    GROUP BY 1 ORDER BY c DESC`,
	"order.count": `SELECT %s AS k, count(*)::int AS c FROM ops_order WHERE NOT is_deleted
	    AND (created_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $1::date AND $2::date
	    GROUP BY 1 ORDER BY c DESC`,
}

// guard 经营分析权限闸门（口径与 Django HasPermission 一致）
func (h *Handler) guard(w http.ResponseWriter, r *http.Request) bool {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return false
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil || !hasPerm(perms, "analytics.view") {
		httpx.Err(w, http.StatusForbidden, "permission_denied", "缺少所需权限。")
		return false
	}
	return true
}

// MetricCatalog GET /api/v1/analytics/metrics?domain=
func (h *Handler) MetricCatalog(w http.ResponseWriter, r *http.Request) {
	if !h.guard(w, r) {
		return
	}
	domain := r.URL.Query().Get("domain")
	out := []map[string]any{}
	for _, code := range metricOrder {
		spec := metricSpecs[code]
		if domain != "" && spec.Domain != domain {
			continue
		}
		mtype := "range"
		if spec.Snapshot {
			mtype = "snapshot"
		}
		dims := spec.Dimensions
		if dims == nil {
			dims = []string{}
		}
		out = append(out, map[string]any{
			"code": code, "name": spec.Name, "domain": spec.Domain, "unit": spec.Unit,
			"type": mtype, "dimensions": dims, "description": spec.Description,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"metrics": out})
}

// MetricQuery POST /api/v1/analytics/metrics/query {codes, start, end, dimension}
func (h *Handler) MetricQuery(w http.ResponseWriter, r *http.Request) {
	if !h.guard(w, r) {
		return
	}
	var body struct {
		Codes     []string `json:"codes"`
		Start     string   `json:"start"`
		End       string   `json:"end"`
		Dimension string   `json:"dimension"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.Codes) == 0 {
		httpx.Err(w, http.StatusBadRequest, "CODES_REQUIRED", "codes 必须是非空数组。")
		return
	}
	s, e := dateRange(body.Start, body.End)
	results := make([]map[string]any, 0, len(body.Codes))
	for _, code := range body.Codes {
		// 与 compute_metric 一样：任一 code 不合法就整体报错，不做部分成功
		spec, ok := metricSpecs[code]
		if !ok {
			httpx.Err(w, http.StatusNotFound, "UNKNOWN_METRIC", "未知指标："+code)
			return
		}
		if body.Dimension != "" && !hasDim(spec.Dimensions, body.Dimension) {
			httpx.Err(w, http.StatusBadRequest, "INVALID_DIMENSION",
				fmt.Sprintf("指标 %s 不支持维度 %s", code, body.Dimension))
			return
		}
		m, err := h.computeAt(r, code, s, e, body.Dimension)
		if err != nil {
			httpx.Fail(w, r, "INTERNAL", "指标计算失败", err)
			return
		}
		results = append(results, m)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"results": results})
}

func hasDim(dims []string, want string) bool {
	for _, d := range dims {
		if d == want {
			return true
		}
	}
	return false
}

// computeAt 按显式账期算单指标（dashboard 走的是同一批 SQL）
func (h *Handler) computeAt(r *http.Request, code, s, e, dimension string) (map[string]any, error) {
	ctx := r.Context()
	spec := metricSpecs[code]
	out := map[string]any{"code": code, "name": spec.Name, "unit": spec.Unit, "domain": spec.Domain}
	args := []any{}
	if !spec.Snapshot {
		args = append(args, s, e)
	}
	if spec.Ratio {
		var num, den float64
		if err := h.DB.QueryRow(ctx, spec.SQL, args...).Scan(&num, &den); err != nil {
			return nil, err
		}
		out["value"] = rate(int(num), int(den))
		out["numerator"] = int(num)
		out["denominator"] = int(den)
		return out, nil
	}
	var v float64
	if err := h.DB.QueryRow(ctx, spec.SQL, args...).Scan(&v); err != nil {
		return nil, err
	}
	if spec.Float {
		out["value"] = v
	} else {
		out["value"] = int64(v)
	}
	if dimension != "" {
		if tpl, ok := breakdownSQL[code]; ok {
			col := "COALESCE(NULLIF(" + dimension + ",''), '')"
			out["breakdown"] = h.breakdown(ctx, fmt.Sprintf(tpl, col), dimLabels[code+"/"+dimension], args...)
		}
	}
	return out, nil
}

// MetricTrend GET /api/v1/analytics/metrics/{code}/trend?days=14
func (h *Handler) MetricTrend(w http.ResponseWriter, r *http.Request) {
	if !h.guard(w, r) {
		return
	}
	code := chi.URLParam(r, "code")
	days := 14
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		days = v
	}
	end := time.Now().In(cstZone)
	start := end.AddDate(0, 0, -days)
	rows, err := h.DB.Query(r.Context(), `
		SELECT stat_date::text, value::float8 FROM ana_metric_snapshot
		WHERE metric_code=$1 AND dimension_key='' AND stat_date BETWEEN $2::date AND $3::date
		ORDER BY stat_date`, code, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取趋势失败")
		return
	}
	defer rows.Close()
	series := []map[string]any{}
	for rows.Next() {
		var d string
		var v float64
		if rows.Scan(&d, &v) != nil {
			break
		}
		series = append(series, map[string]any{"date": d, "value": v})
	}
	// 未注册的 code 不报错，回落 code 本身当名字（对齐 metric_trend 的 spec 缺失分支）
	name, unit := code, ""
	if spec, ok := metricSpecs[code]; ok {
		name, unit = spec.Name, spec.Unit
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"code": code, "name": name, "unit": unit, "series": series,
	})
}

// domainLabel 表前缀 → 业务域中文名，对齐 catalog.DOMAIN_LABEL
var domainLabel = []struct{ Prefix, App, Label string }{
	{"accounts_", "accounts", "账号"},
	{"iam_", "iam", "权限/组织"},
	{"audit_", "audit", "审计"},
	{"md_", "masterdata", "主数据"},
	{"ops_", "ops", "运单/订单"},
	{"fin_", "finance", "财务"},
	{"ai_", "ai", "AI"},
	{"tel_", "telematics", "车联网"},
	{"ana_", "analytics", "数据中台"},
	{"ntf_", "notifications", "通知"},
}

// DataCatalog GET /api/v1/analytics/catalog?counts=true
//
// 与 Django 版的区别见 PORTING.md：Django 自省的是 ORM 模型（含反向关系伪字段、
// Django 内部类型名、help_text），Go 直接自省 PostgreSQL。目录的意义是"资产可见"，
// 而模型层正在被移除——照着已经不存在的 ORM 冻一份快照，只会让治理视图开始说谎。
func (h *Handler) DataCatalog(w http.ResponseWriter, r *http.Request) {
	if !h.guard(w, r) {
		return
	}
	ctx := r.Context()
	withCounts := false
	switch r.URL.Query().Get("counts") {
	case "1", "true", "yes", "True", "TRUE":
		withCounts = true
	}
	rows, err := h.DB.Query(ctx, `
		SELECT c.table_name, c.column_name, c.data_type,
		       COALESCE(col_description(($1 || c.table_name)::regclass::oid, c.ordinal_position), '') AS help
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_name = c.table_name AND t.table_schema = c.table_schema
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name, c.ordinal_position`, "public.")
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据资产失败")
		return
	}
	defer rows.Close()

	type asset struct {
		app, domain, table string
		fields             []map[string]any
	}
	byTable := map[string]*asset{}
	order := []string{}
	for rows.Next() {
		var tbl, col, typ, help string
		if rows.Scan(&tbl, &col, &typ, &help) != nil {
			break
		}
		app, dom := "", ""
		for _, d := range domainLabel {
			if len(tbl) > len(d.Prefix) && tbl[:len(d.Prefix)] == d.Prefix {
				app, dom = d.App, d.Label
				break
			}
		}
		if app == "" { // Django 内置与三方表不入目录，对齐 DOMAIN_APPS 白名单
			continue
		}
		a, ok := byTable[tbl]
		if !ok {
			a = &asset{app: app, domain: dom, table: tbl}
			byTable[tbl] = a
			order = append(order, tbl)
		}
		a.fields = append(a.fields, map[string]any{"name": col, "type": typ, "help": help})
	}

	assets := make([]map[string]any, 0, len(order))
	domains := map[string]bool{}
	for _, tbl := range order {
		a := byTable[tbl]
		m := map[string]any{
			"app": a.app, "domain": a.domain, "table": a.table,
			"field_count": len(a.fields), "fields": a.fields,
		}
		if withCounts {
			var n int64
			if h.DB.QueryRow(ctx, "SELECT count(*) FROM "+a.table).Scan(&n) == nil {
				m["row_count"] = n
			} else {
				m["row_count"] = nil
			}
		}
		assets = append(assets, m)
		domains[a.domain] = true
	}
	sort.Slice(assets, func(i, j int) bool {
		if assets[i]["app"] != assets[j]["app"] {
			return assets[i]["app"].(string) < assets[j]["app"].(string)
		}
		return assets[i]["table"].(string) < assets[j]["table"].(string)
	})
	dl := make([]string, 0, len(domains))
	for d := range domains {
		dl = append(dl, d)
	}
	sort.Strings(dl)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"assets": assets, "total_assets": len(assets), "domains": dl,
	})
}
