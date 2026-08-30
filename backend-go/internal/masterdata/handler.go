// Package masterdata 主数据读路径：customers / vehicles / drivers 列表。
// 通用引擎：SELECT 列别名即 JSON 键（uuid/numeric/date 一律 ::text 保持 DRF 字符串语义），
// rows.Values() 泛化扫描 —— 新资源只需一份 ResourceCfg 配置，无逐字段 scan 代码。
package masterdata

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/blob"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
	// Fallback 子路由未命中时的兜底（绞杀期回代 Django）。
	// CRUD 子路由是挂载式的，未声明的自定义动作（如 /carriers/{id}/blacklist）
	// 不会回到父路由，必须在子路由上显式兜底，否则会被误判成 404。
	Fallback http.Handler
	// Blob 上传文件的存放。为 nil 时退回 MediaRoot 落盘。
	Blob      blob.Store
	MediaRoot string
}

type ResourceCfg struct {
	SelectSQL    string // 列别名 = JSON 键
	FromClause   string
	SearchCols   []string
	OrderingCols map[string]string
	FilterFields map[string]filters.FilterField
	DirectParams map[string]string // DRF filterset_fields：查询参数 → SQL 列
	DefaultOrder string
	// DetailExtras 仅详情（retrieve）计算的重聚合列：别名 → SQL 表达式。
	// 对齐 DRF 里「_is_list(self) 则返回 None」的字段（列表恒为 null，避免 N+1）。
	DetailExtras map[string]string

	// ScopeOrgCol 数据范围：该资源「归属组织 ID」的 SQL 标量表达式（可为子查询）。
	// 置空表示不做数据范围收窄。对齐 iam.scoping.scope_queryset：
	// 超管/all 档全量；否则限组织（子树）；表达式取值为 NULL 相当于 Django 里
	// LEFT JOIN 后的 organization__isnull=True。
	ScopeOrgCol string
	// ScopeIncludeNull 对齐 org_scope_include_null：无组织归属的记录对所有人可见
	ScopeIncludeNull bool

	// PartialOmit 复刻 DRF 的一个反直觉行为：partial=True（即 PATCH）时，
	// Field.get_default() 直接 raise SkipField，于是「带 default 的只读关联字段」
	// 一旦 source 链上出现 None，该键会从 PATCH 响应里**整个消失**（而非返回 ""）。
	// 键 = JSON 字段名；值 = 判空的 SQL 表达式，为 NULL 时该键在 PATCH 响应中省略。
	PartialOmit map[string]string
	// SoftDeleteCol 软删列（如 "c.is_deleted"）。对齐 core.SoftDeleteManager：
	// 这些模型的默认管理器带 WHERE NOT is_deleted，软删行在读路径上必须不可见。
	SoftDeleteCol string

	// scopeWhere 由 applyScope 计算后注入，不在配置里手写
	scopeWhere string
}

var customersCfg = ResourceCfg{
	SelectSQL: `
SELECT c.id::text AS id, c.code, c.name, c.category, c.level,
       (CASE c.level WHEN 'S' THEN 'S · 战略' WHEN 'A' THEN 'A · 重点' WHEN 'B' THEN 'B · 常规'
                     WHEN 'C' THEN 'C · 一般' WHEN 'D' THEN 'D · 观察' ELSE '' END) AS level_label,
       c.contact_name, c.contact_phone, c.wechat_group,
       c.settlement_type, c.credit_limit::text AS credit_limit, c.credit_days, c.billing_day,
       c.is_active, NULL::text AS history`,
	FromClause: "FROM md_customer c",
	SearchCols: []string{"c.code", "c.name", "c.contact_phone"},
	OrderingCols: map[string]string{
		"code": "c.code", "name": "c.name", "created_at": "c.created_at", "level": "c.level",
		"category": "c.category", "credit_limit": "c.credit_limit", "credit_days": "c.credit_days",
		"contact_name": "c.contact_name", "contact_phone": "c.contact_phone", "is_active": "c.is_active",
	},
	FilterFields: map[string]filters.FilterField{
		"name":     {Type: filters.Text, Cols: []string{"c.name"}},
		"code":     {Type: filters.Text, Cols: []string{"c.code"}},
		"contact":  {Type: filters.Text, Cols: []string{"c.contact_name"}},
		"level":    {Type: filters.Enum, Cols: []string{"c.level"}},
		"category": {Type: filters.Enum, Cols: []string{"c.category"}},
		"credit":   {Type: filters.Number, Cols: []string{"c.credit_limit"}},
		"days":     {Type: filters.Number, Cols: []string{"c.credit_days"}},
		"active":   {Type: filters.Bool, Cols: []string{"c.is_active"}},
	},
	DirectParams:  map[string]string{"is_active": "c.is_active"},
	DefaultOrder:  "ORDER BY c.code, c.id",
	SoftDeleteCol: "c.is_deleted",
}

var vehiclesCfg = ResourceCfg{
	SelectSQL: `
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
	FromClause: "FROM md_vehicle v LEFT JOIN md_carrier ca ON ca.id = v.carrier_id",
	SearchCols: []string{"v.plate_no", "v.vehicle_type"},
	OrderingCols: map[string]string{
		"plate_no": "v.plate_no", "created_at": "v.created_at", "load_capacity_ton": "v.load_capacity_ton",
		"volume_capacity_cbm": "v.volume_capacity_cbm", "vehicle_type": "v.vehicle_type",
		"owner_name": "COALESCE(ca.name,'自有')", "is_active": "v.is_active",
	},
	FilterFields: map[string]filters.FilterField{
		"plate":  {Type: filters.Text, Cols: []string{"v.plate_no"}},
		"type":   {Type: filters.Text, Cols: []string{"v.vehicle_type"}},
		"owner":  {Type: filters.Text, Cols: []string{"COALESCE(ca.name,'自有')"}},
		"ton":    {Type: filters.Number, Cols: []string{"v.load_capacity_ton"}},
		"cbm":    {Type: filters.Number, Cols: []string{"v.volume_capacity_cbm"}},
		"active": {Type: filters.Bool, Cols: []string{"v.is_active"}},
	},
	DirectParams: map[string]string{
		"is_active": "v.is_active", "carrier": "v.carrier_id::text",
		"vehicle_class": "v.vehicle_class", "dispatch_source": "v.dispatch_source",
	},
	DefaultOrder:  "ORDER BY v.plate_no, v.id",
	SoftDeleteCol: "v.is_deleted",
}

var driversCfg = ResourceCfg{
	SelectSQL: `
SELECT d.id::text AS id, d.name, d.phone, d.wechat, d.employment_type,
       (CASE d.employment_type WHEN 'employee' THEN '自有员工' WHEN 'outsourced' THEN '外协外调'
                                WHEN 'carrier_driver' THEN '承运商司机' WHEN 'temp' THEN '临时' ELSE '' END) AS employment_label,
       d.app_registered, d.app_registered_at, d.cumulative_waybills, d.cumulative_freight::text AS cumulative_freight,
       d.id_no, d.license_no, d.license_type, d.license_expiry::text AS license_expiry,
       d.qualification_cert_no, d.qualification_expiry::text AS qualification_expiry,
       d.carrier_id::text AS carrier, COALESCE(ca.name,'') AS carrier_name, d.is_active`,
	FromClause: "FROM md_driver d LEFT JOIN md_carrier ca ON ca.id = d.carrier_id",
	SearchCols: []string{"d.name", "d.phone", "d.wechat"},
	OrderingCols: map[string]string{
		"name": "d.name", "created_at": "d.created_at", "cumulative_waybills": "d.cumulative_waybills",
		"cumulative_freight": "d.cumulative_freight", "phone": "d.phone", "employment_type": "d.employment_type",
		"owner_name": "COALESCE(ca.name,'自有')", "license_type": "d.license_type",
		"license_expiry": "d.license_expiry", "is_active": "d.is_active",
	},
	FilterFields: map[string]filters.FilterField{
		"name":     {Type: filters.Text, Cols: []string{"d.name"}},
		"phone":    {Type: filters.Text, Cols: []string{"d.phone"}},
		"license":  {Type: filters.Text, Cols: []string{"d.license_type"}},
		"emp":      {Type: filters.Enum, Cols: []string{"d.employment_type"}},
		"owner":    {Type: filters.Text, Cols: []string{"COALESCE(ca.name,'自有')"}},
		"waybills": {Type: filters.Number, Cols: []string{"d.cumulative_waybills"}},
		"active":   {Type: filters.Bool, Cols: []string{"d.is_active"}},
	},
	DirectParams: map[string]string{
		"is_active": "d.is_active", "carrier": "d.carrier_id::text",
		"employment_type": "d.employment_type", "app_registered": "d.app_registered",
	},
	DefaultOrder:  "ORDER BY d.name, d.id",
	SoftDeleteCol: "d.is_deleted",
}

func (h *Handler) Customers(w http.ResponseWriter, r *http.Request) { h.List(w, r, customersCfg) }
func (h *Handler) Vehicles(w http.ResponseWriter, r *http.Request)  { h.List(w, r, vehiclesCfg) }
func (h *Handler) Drivers(w http.ResponseWriter, r *http.Request)   { h.List(w, r, driversCfg) }

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

// applyScope 按当前用户的数据范围收窄配置；返回 false 表示已写出错误响应
func (h *Handler) applyScope(r *http.Request, cfg ResourceCfg) (ResourceCfg, string) {
	if cfg.ScopeOrgCol == "" {
		return cfg, ""
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		return cfg, "TOKEN_INVALID"
	}
	ids, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		return cfg, "INTERNAL"
	}
	if ids == nil { // all 档：不收窄
		return cfg, ""
	}
	cond := "false"
	if len(ids) > 0 {
		quoted := make([]string, len(ids))
		for i, id := range ids {
			quoted[i] = "'" + strings.ReplaceAll(id, "'", "") + "'"
		}
		cond = fmt.Sprintf("(%s)::text IN (%s)", cfg.ScopeOrgCol, strings.Join(quoted, ","))
	}
	if cfg.ScopeIncludeNull {
		cond = "(" + cond + " OR (" + cfg.ScopeOrgCol + ") IS NULL)"
	}
	cfg.scopeWhere = cond
	return cfg, ""
}

// list 通用列表：search/ordering/filter/直连参数/分页 → {items,total,page,page_size,pages}
func (h *Handler) List(w http.ResponseWriter, r *http.Request, cfg ResourceCfg) {
	ctx := r.Context()
	cfg, scopeErr := h.applyScope(r, cfg)
	if scopeErr == "TOKEN_INVALID" {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	} else if scopeErr != "" {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	q := r.URL.Query()
	page := clampInt(q.Get("page"), 1, 1, 1<<30)
	pageSize := clampInt(q.Get("page_size"), 20, 1, 1000) // 主数据下拉常用 page_size=500

	args := &filters.Args{}
	where := []string{"true"}
	if cfg.SoftDeleteCol != "" {
		where = append(where, "NOT "+cfg.SoftDeleteCol)
	}
	if cfg.scopeWhere != "" {
		where = append(where, cfg.scopeWhere)
	}
	if s := strings.TrimSpace(q.Get("search")); s != "" {
		ph := args.Add("%" + s + "%")
		parts := make([]string, len(cfg.SearchCols))
		for i, c := range cfg.SearchCols {
			parts[i] = fmt.Sprintf("%s ILIKE %s", c, ph)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	for param, col := range cfg.DirectParams {
		if v := q.Get(param); v != "" {
			if b, ok := parseBoolish(v); ok && (strings.HasSuffix(col, "is_active") || strings.HasSuffix(col, "app_registered")) {
				where = append(where, fmt.Sprintf("%s = %s", col, args.Add(b)))
			} else {
				where = append(where, fmt.Sprintf("%s = %s", col, args.Add(v)))
			}
		}
	}
	// 已知字段上的非法值（比如日期框里打了「今天」）要当场说清是哪个字段，
	// 而不是让 Postgres 报错变成 500，也不是默默把这个条件丢掉。
	frag, ferr := filters.Apply(q.Get("filter"), cfg.FilterFields, args)
	if ferr != nil {
		httpx.Err(w, http.StatusBadRequest, "INVALID_FILTER", ferr.Error())
		return
	}
	if frag != "" {
		where = append(where, frag)
	}
	whereSQL := "WHERE " + strings.Join(where, " AND ")

	orderSQL := cfg.DefaultOrder
	if raw := q.Get("ordering"); raw != "" {
		var parts []string
		for _, f := range strings.Split(raw, ",") {
			f = strings.TrimSpace(f)
			desc := strings.HasPrefix(f, "-")
			col, ok := cfg.OrderingCols[strings.TrimPrefix(f, "-")]
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
	if err := h.DB.QueryRow(ctx, "SELECT count(*) "+cfg.FromClause+" "+whereSQL, args.Values...).Scan(&total); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	sql := cfg.SelectSQL + " " + cfg.FromClause + " " + whereSQL + " " + orderSQL +
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

// One 用资源配置回读单行（写路径回显复用列表列面）；未命中返回 nil
// OneScoped 带数据范围的单对象读取：越权与不存在同样返回 (nil, nil)，
// 让调用方一律回 404 —— 不告诉越权者"这条记录其实存在"。
func (h *Handler) OneScoped(r *http.Request, cfg ResourceCfg, where string, args ...any) (map[string]any, error) {
	scoped, errCode := h.applyScope(r, cfg)
	if errCode != "" {
		return nil, fmt.Errorf("%s", errCode)
	}
	return h.one(r.Context(), scoped, false, where, args...)
}

func (h *Handler) One(ctx context.Context, cfg ResourceCfg, where string, args ...any) (map[string]any, error) {
	return h.one(ctx, cfg, false, where, args...)
}

// OneDetail 详情回读：额外算上 DetailExtras 的重聚合列
func (h *Handler) OneDetail(ctx context.Context, cfg ResourceCfg, where string, args ...any) (map[string]any, error) {
	return h.one(ctx, cfg, true, where, args...)
}

// OnePartial 回读 PATCH 响应：按 PartialOmit 摘掉 source 链为 None 的只读关联字段
func (h *Handler) OnePartial(ctx context.Context, cfg ResourceCfg, where string, args ...any) (map[string]any, error) {
	if len(cfg.PartialOmit) == 0 {
		return h.OneDetail(ctx, cfg, where, args...)
	}
	keys := make([]string, 0, len(cfg.PartialOmit))
	for k := range cfg.PartialOmit {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	probe := cfg
	for _, k := range keys {
		probe.SelectSQL += ", ((" + cfg.PartialOmit[k] + ") IS NULL) AS __po_" + k
	}
	it, err := h.one(ctx, probe, true, where, args...)
	if err != nil || it == nil {
		return it, err
	}
	for _, k := range keys {
		if drop, _ := it["__po_"+k].(bool); drop {
			delete(it, k)
		}
		delete(it, "__po_"+k)
	}
	return it, nil
}

func (h *Handler) one(ctx context.Context, cfg ResourceCfg, detail bool, where string, args ...any) (map[string]any, error) {
	sel := cfg.SelectSQL
	if detail && len(cfg.DetailExtras) > 0 {
		keys := make([]string, 0, len(cfg.DetailExtras))
		for k := range cfg.DetailExtras {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// 详情列覆盖同名的列表占位列（列表里恒为 NULL）
		for _, k := range keys {
			sel = stripColumn(sel, k)
		}
		for _, k := range keys {
			sel += ", (" + cfg.DetailExtras[k] + ") AS " + k
		}
	}
	if cfg.SoftDeleteCol != "" {
		where = "(" + where + ") AND NOT " + cfg.SoftDeleteCol
	}
	if cfg.scopeWhere != "" {
		where = "(" + where + ") AND " + cfg.scopeWhere
	}
	rows, err := h.DB.Query(ctx, sel+" "+cfg.FromClause+" WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return items[0], nil
}

// stripColumn 从 SELECT 列面里摘掉某个别名列（详情态用真实聚合替换列表占位）
func stripColumn(sel, alias string) string {
	marker := " AS " + alias
	i := strings.Index(sel, marker)
	if i < 0 {
		return sel
	}
	end := i + len(marker)
	start := strings.LastIndex(sel[:i], ",")
	if start < 0 {
		return sel
	}
	return sel[:start] + sel[end:]
}

// pyISO 复刻 Python datetime.isoformat() + DRF 的 +00:00→Z 替换：
// 微秒为 0 时不带小数部分，否则固定 6 位（Go 的 RFC3339Nano 会裁掉末尾零，与 Python 不符）。
func pyISO(t time.Time) string {
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05Z07:00")
	}
	return t.Format("2006-01-02T15:04:05.000000Z07:00")
}

// rowsToMaps 泛化扫描：列别名即 JSON 键；time.Time 走 DRF 同款 ISO-8601
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
				m[fd.Name] = pyISO(t)
			} else {
				m[fd.Name] = v
			}
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

var b2bCfg = ResourceCfg{
	SelectSQL: `
SELECT p.id::text AS id, p.partner_type,
       (CASE p.partner_type WHEN 'shipper' THEN '发货方' WHEN 'consignee' THEN '收货方' WHEN 'supplier' THEN '供应商/承运商' ELSE '' END) AS partner_type_label,
       p.code, p.name, p.contact_name, p.contact_phone, p.address, p.city, p.is_active`,
	FromClause:    "FROM md_b2b_partner p",
	SearchCols:    []string{"p.code", "p.name", "p.contact_phone", "p.city"},
	OrderingCols:  map[string]string{"code": "p.code", "name": "p.name", "created_at": "p.created_at"},
	FilterFields:  map[string]filters.FilterField{},
	DirectParams:  map[string]string{"partner_type": "p.partner_type", "is_active": "p.is_active"},
	DefaultOrder:  "ORDER BY p.code, p.id",
	SoftDeleteCol: "p.is_deleted",
}

func (h *Handler) B2BPartners(w http.ResponseWriter, r *http.Request) { h.List(w, r, b2bCfg) }

// carriers：dispatch_blocked 风控原因与 expiry_alerts 30 天到期预警均在 SQL 内联计算，
// performance 聚合仅详情页有（列表恒 null，对齐 DRF _is_list 行为）。
var carriersCfg = ResourceCfg{
	SelectSQL: `
SELECT ca.id::text AS id, ca.code, ca.name, ca.carrier_type,
       (CASE ca.carrier_type WHEN 'owner_fleet' THEN '个体车队' WHEN 'company_fleet' THEN '公司车队'
                             WHEN 'platform' THEN '网货平台' WHEN 'temporary' THEN '临时承运商' ELSE '' END) AS carrier_type_label,
       ca.contact_name, ca.contact_phone, ca.city, ca.service_area, ca.settlement_type, ca.is_active,
       ca.grade,
       (CASE ca.grade WHEN 'A' THEN 'A · 优质' WHEN 'B' THEN 'B · 良好' WHEN 'C' THEN 'C · 关注' WHEN 'D' THEN 'D · 高风险' ELSE '' END) AS grade_label,
       ca.blacklisted, ca.blacklist_reason,
       ca.business_license_no, ca.transport_license_no, ca.qualification_expiry::text AS qualification_expiry,
       ca.contract_expiry::text AS contract_expiry, ca.insurance_expiry::text AS insurance_expiry, ca.tax_no,
       ca.credit_limit::text AS credit_limit, ca.credit_days, ca.billing_day,
       (CASE WHEN ca.blacklisted THEN '承运商 '||ca.name||' 已列入黑名单'||(CASE WHEN COALESCE(ca.blacklist_reason,'')<>'' THEN '（'||ca.blacklist_reason||'）' ELSE '' END)
             WHEN NOT ca.is_active THEN '承运商 '||ca.name||' 已停用'
             WHEN ca.qualification_expiry IS NOT NULL AND ca.qualification_expiry < td.today
               THEN '承运商 '||ca.name||' 承运资质已于 '||to_char(ca.qualification_expiry,'YYYY-MM-DD')||' 到期'
             ELSE '' END) AS dispatch_blocked,
       alerts.j AS expiry_alerts, NULL::text AS performance`,
	FromClause: `FROM md_carrier ca
CROSS JOIN LATERAL (SELECT (now() AT TIME ZONE 'Asia/Shanghai')::date AS today) td
LEFT JOIN LATERAL (
  SELECT COALESCE(json_agg(x.j ORDER BY x.ord), '[]'::json) AS j FROM (
    SELECT 1 AS ord, json_build_object('field','qualification_expiry','label','承运资质','date',ca.qualification_expiry::text,'expired',ca.qualification_expiry<td.today) AS j
      WHERE ca.qualification_expiry IS NOT NULL AND ca.qualification_expiry <= td.today + 30
    UNION ALL
    SELECT 2, json_build_object('field','contract_expiry','label','合作合同','date',ca.contract_expiry::text,'expired',ca.contract_expiry<td.today)
      WHERE ca.contract_expiry IS NOT NULL AND ca.contract_expiry <= td.today + 30
    UNION ALL
    SELECT 3, json_build_object('field','insurance_expiry','label','承运人责任险','date',ca.insurance_expiry::text,'expired',ca.insurance_expiry<td.today)
      WHERE ca.insurance_expiry IS NOT NULL AND ca.insurance_expiry <= td.today + 30
  ) x
) alerts ON true`,
	SearchCols: []string{"ca.code", "ca.name", "ca.contact_phone", "ca.city"},
	OrderingCols: map[string]string{
		"code": "ca.code", "name": "ca.name", "grade": "ca.grade", "created_at": "ca.created_at",
		"city": "ca.city", "credit_days": "ca.credit_days", "carrier_type": "ca.carrier_type", "is_active": "ca.is_active",
	},
	FilterFields: map[string]filters.FilterField{
		"name":        {Type: filters.Text, Cols: []string{"ca.name"}},
		"code":        {Type: filters.Text, Cols: []string{"ca.code"}},
		"city":        {Type: filters.Text, Cols: []string{"ca.city"}},
		"grade":       {Type: filters.Enum, Cols: []string{"ca.grade"}},
		"type":        {Type: filters.Enum, Cols: []string{"ca.carrier_type"}},
		"credit_days": {Type: filters.Number, Cols: []string{"ca.credit_days"}},
		"blocked":     {Type: filters.Bool, Cols: []string{"(ca.blacklisted OR NOT ca.is_active OR (ca.qualification_expiry IS NOT NULL AND ca.qualification_expiry < (now() AT TIME ZONE 'Asia/Shanghai')::date))"}},
		"active":      {Type: filters.Bool, Cols: []string{"ca.is_active"}},
	},
	DirectParams: map[string]string{
		"is_active": "ca.is_active", "grade": "ca.grade", "carrier_type": "ca.carrier_type",
	},
	DefaultOrder:  "ORDER BY ca.code, ca.id",
	SoftDeleteCol: "ca.is_deleted",
}

func (h *Handler) Carriers(w http.ResponseWriter, r *http.Request) { h.List(w, r, carriersCfg) }
