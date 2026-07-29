// Package masterdata 主数据读路径：customers / vehicles / drivers 列表。
// 通用引擎：SELECT 列别名即 JSON 键（uuid/numeric/date 一律 ::text 保持 DRF 字符串语义），
// rows.Values() 泛化扫描 —— 新资源只需一份 resourceCfg 配置，无逐字段 scan 代码。
package masterdata

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
}

type resourceCfg struct {
	selectSQL    string // 列别名 = JSON 键
	fromClause   string
	searchCols   []string
	orderingCols map[string]string
	filterFields map[string]filters.FilterField
	directParams map[string]string // DRF filterset_fields：查询参数 → SQL 列
	defaultOrder string
}

var customersCfg = resourceCfg{
	selectSQL: `
SELECT c.id::text AS id, c.code, c.name, c.category, c.level,
       (CASE c.level WHEN 'S' THEN 'S · 战略' WHEN 'A' THEN 'A · 重点' WHEN 'B' THEN 'B · 常规'
                     WHEN 'C' THEN 'C · 一般' WHEN 'D' THEN 'D · 观察' ELSE '' END) AS level_label,
       c.contact_name, c.contact_phone, c.wechat_group,
       c.settlement_type, c.credit_limit::text AS credit_limit, c.credit_days, c.billing_day,
       c.is_active, NULL::text AS history`,
	fromClause: "FROM md_customer c",
	searchCols: []string{"c.code", "c.name", "c.contact_phone"},
	orderingCols: map[string]string{
		"code": "c.code", "name": "c.name", "created_at": "c.created_at", "level": "c.level",
		"category": "c.category", "credit_limit": "c.credit_limit", "credit_days": "c.credit_days",
		"contact_name": "c.contact_name", "contact_phone": "c.contact_phone", "is_active": "c.is_active",
	},
	filterFields: map[string]filters.FilterField{
		"name":     {Type: filters.Text, Cols: []string{"c.name"}},
		"code":     {Type: filters.Text, Cols: []string{"c.code"}},
		"contact":  {Type: filters.Text, Cols: []string{"c.contact_name"}},
		"level":    {Type: filters.Enum, Cols: []string{"c.level"}},
		"category": {Type: filters.Enum, Cols: []string{"c.category"}},
		"credit":   {Type: filters.Number, Cols: []string{"c.credit_limit"}},
		"days":     {Type: filters.Number, Cols: []string{"c.credit_days"}},
		"active":   {Type: filters.Bool, Cols: []string{"c.is_active"}},
	},
	directParams: map[string]string{"is_active": "c.is_active"},
	defaultOrder: "ORDER BY c.created_at DESC, c.id",
}

var vehiclesCfg = resourceCfg{
	selectSQL: `
SELECT v.id::text AS id, v.plate_no, v.vehicle_class,
       (CASE v.vehicle_class WHEN 'tractor' THEN '牵引车' WHEN 'trailer' THEN '挂车' WHEN 'rigid' THEN '单体车' ELSE '' END) AS vehicle_class_label,
       v.body_type,
       (CASE v.body_type WHEN 'stake' THEN '高栏' WHEN 'flatbed' THEN '平板' WHEN 'van' THEN '厢式' WHEN 'reefer' THEN '冷藏'
                          WHEN 'hazmat' THEN '危运' WHEN 'fence' THEN '仓栅' WHEN 'wing' THEN '飞翼' WHEN 'tank' THEN '罐式' ELSE '' END) AS body_type_label,
       v.vehicle_length_m::text AS vehicle_length_m, v.dispatch_source,
       (CASE v.dispatch_source WHEN 'own' THEN '自有' WHEN 'external' THEN '外调' WHEN 'platform' THEN '平台' ELSE '' END) AS dispatch_source_label,
       v.vehicle_type, v.ownership_type, v.carrier_id::text AS carrier, COALESCE(ca.name,'') AS carrier_name,
       v.load_capacity_ton::text AS load_capacity_ton, v.volume_capacity_cbm::text AS volume_capacity_cbm,
       v.road_transport_cert_no, v.inspection_expiry::text AS inspection_expiry,
       v.insurance_expiry::text AS insurance_expiry, v.maintenance_due_date::text AS maintenance_due_date,
       NULL::text AS freight_total, v.is_active`,
	fromClause: "FROM md_vehicle v LEFT JOIN md_carrier ca ON ca.id = v.carrier_id",
	searchCols: []string{"v.plate_no", "v.vehicle_type"},
	orderingCols: map[string]string{
		"plate_no": "v.plate_no", "created_at": "v.created_at", "load_capacity_ton": "v.load_capacity_ton",
		"volume_capacity_cbm": "v.volume_capacity_cbm", "vehicle_type": "v.vehicle_type",
		"owner_name": "COALESCE(ca.name,'自有')", "is_active": "v.is_active",
	},
	filterFields: map[string]filters.FilterField{
		"plate":  {Type: filters.Text, Cols: []string{"v.plate_no"}},
		"type":   {Type: filters.Text, Cols: []string{"v.vehicle_type"}},
		"owner":  {Type: filters.Text, Cols: []string{"COALESCE(ca.name,'自有')"}},
		"ton":    {Type: filters.Number, Cols: []string{"v.load_capacity_ton"}},
		"cbm":    {Type: filters.Number, Cols: []string{"v.volume_capacity_cbm"}},
		"active": {Type: filters.Bool, Cols: []string{"v.is_active"}},
	},
	directParams: map[string]string{
		"is_active": "v.is_active", "carrier": "v.carrier_id::text",
		"vehicle_class": "v.vehicle_class", "dispatch_source": "v.dispatch_source",
	},
	defaultOrder: "ORDER BY v.created_at DESC, v.id",
}

var driversCfg = resourceCfg{
	selectSQL: `
SELECT d.id::text AS id, d.name, d.phone, d.wechat, d.employment_type,
       (CASE d.employment_type WHEN 'employee' THEN '自有员工' WHEN 'outsourced' THEN '外协外调'
                                WHEN 'carrier_driver' THEN '承运商司机' WHEN 'temp' THEN '临时' ELSE '' END) AS employment_label,
       d.app_registered, d.app_registered_at, d.cumulative_waybills, d.cumulative_freight::text AS cumulative_freight,
       d.id_no, d.license_no, d.license_type, d.license_expiry::text AS license_expiry,
       d.qualification_cert_no, d.qualification_expiry::text AS qualification_expiry,
       d.carrier_id::text AS carrier, COALESCE(ca.name,'') AS carrier_name, d.is_active`,
	fromClause: "FROM md_driver d LEFT JOIN md_carrier ca ON ca.id = d.carrier_id",
	searchCols: []string{"d.name", "d.phone", "d.wechat"},
	orderingCols: map[string]string{
		"name": "d.name", "created_at": "d.created_at", "cumulative_waybills": "d.cumulative_waybills",
		"cumulative_freight": "d.cumulative_freight", "phone": "d.phone", "employment_type": "d.employment_type",
		"owner_name": "COALESCE(ca.name,'自有')", "license_type": "d.license_type",
		"license_expiry": "d.license_expiry", "is_active": "d.is_active",
	},
	filterFields: map[string]filters.FilterField{
		"name":     {Type: filters.Text, Cols: []string{"d.name"}},
		"phone":    {Type: filters.Text, Cols: []string{"d.phone"}},
		"license":  {Type: filters.Text, Cols: []string{"d.license_type"}},
		"emp":      {Type: filters.Enum, Cols: []string{"d.employment_type"}},
		"owner":    {Type: filters.Text, Cols: []string{"COALESCE(ca.name,'自有')"}},
		"waybills": {Type: filters.Number, Cols: []string{"d.cumulative_waybills"}},
		"active":   {Type: filters.Bool, Cols: []string{"d.is_active"}},
	},
	directParams: map[string]string{
		"is_active": "d.is_active", "carrier": "d.carrier_id::text",
		"employment_type": "d.employment_type", "app_registered": "d.app_registered",
	},
	defaultOrder: "ORDER BY d.created_at DESC, d.id",
}

func (h *Handler) Customers(w http.ResponseWriter, r *http.Request) { h.list(w, r, customersCfg) }
func (h *Handler) Vehicles(w http.ResponseWriter, r *http.Request)  { h.list(w, r, vehiclesCfg) }
func (h *Handler) Drivers(w http.ResponseWriter, r *http.Request)   { h.list(w, r, driversCfg) }

func clampInt(s string, def, lo, hi int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return max(lo, min(hi, n))
}

func parseBoolish(s string) (bool, bool) {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "t":
		return true, true
	case "false", "0", "no", "f":
		return false, true
	}
	return false, false
}

// list 通用列表：search/ordering/filter/直连参数/分页 → {items,total,page,page_size,pages}
func (h *Handler) list(w http.ResponseWriter, r *http.Request, cfg resourceCfg) {
	ctx := r.Context()
	q := r.URL.Query()
	page := clampInt(q.Get("page"), 1, 1, 1<<30)
	pageSize := clampInt(q.Get("page_size"), 20, 1, 1000) // 主数据下拉常用 page_size=500

	args := &filters.Args{}
	where := []string{"true"}
	if s := strings.TrimSpace(q.Get("search")); s != "" {
		ph := args.Add("%" + s + "%")
		parts := make([]string, len(cfg.searchCols))
		for i, c := range cfg.searchCols {
			parts[i] = fmt.Sprintf("%s ILIKE %s", c, ph)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	for param, col := range cfg.directParams {
		if v := q.Get(param); v != "" {
			if b, ok := parseBoolish(v); ok && (strings.HasSuffix(col, "is_active") || strings.HasSuffix(col, "app_registered")) {
				where = append(where, fmt.Sprintf("%s = %s", col, args.Add(b)))
			} else {
				where = append(where, fmt.Sprintf("%s = %s", col, args.Add(v)))
			}
		}
	}
	if frag := filters.Apply(q.Get("filter"), cfg.filterFields, args); frag != "" {
		where = append(where, frag)
	}
	whereSQL := "WHERE " + strings.Join(where, " AND ")

	orderSQL := cfg.defaultOrder
	if raw := q.Get("ordering"); raw != "" {
		var parts []string
		for _, f := range strings.Split(raw, ",") {
			f = strings.TrimSpace(f)
			desc := strings.HasPrefix(f, "-")
			col, ok := cfg.orderingCols[strings.TrimPrefix(f, "-")]
			if !ok {
				continue
			}
			dir := "ASC"
			if desc {
				dir = "DESC"
			}
			parts = append(parts, col+" "+dir)
		}
		if len(parts) > 0 {
			orderSQL = "ORDER BY " + strings.Join(parts, ", ") + ", id"
		}
	}

	var total int
	if err := h.DB.QueryRow(ctx, "SELECT count(*) "+cfg.fromClause+" "+whereSQL, args.Values...).Scan(&total); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	sql := cfg.selectSQL + " " + cfg.fromClause + " " + whereSQL + " " + orderSQL +
		fmt.Sprintf(" LIMIT %s OFFSET %s", args.Add(pageSize), args.Add((page-1)*pageSize))
	rows, err := h.DB.Query(ctx, sql, args.Values...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": page, "page_size": pageSize,
		"pages": int(math.Max(1, math.Ceil(float64(total)/float64(pageSize)))),
	})
}

// rowsToMaps 泛化扫描：列别名即 JSON 键；time.Time 走 RFC3339Nano 由 encoding/json 处理
func rowsToMaps(rows pgx.Rows) ([]map[string]any, error) {
	fds := rows.FieldDescriptions()
	items := []map[string]any{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(fds))
		for i, fd := range fds {
			v := vals[i]
			if t, ok := v.(time.Time); ok {
				m[fd.Name] = t.Format(time.RFC3339Nano)
			} else {
				m[fd.Name] = v
			}
		}
		items = append(items, m)
	}
	return items, rows.Err()
}
