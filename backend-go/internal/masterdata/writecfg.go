package masterdata

// 各主数据资源的写侧配置 + 新增资源（routes / carrier-lane-prices / driver-credentials）
// 的读侧配置。所有标准 CRUD 由 crud.go 的通用引擎驱动，此处只声明字段契约。

import "github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"

var CustomerWrite = WriteCfg{
	Table: "md_customer", Model: "Customer", Alias: "c", SoftDelete: true,
	Fields: map[string]Field{
		"code":            {Kind: FText, Required: true, Unique: true, Label: "编码"},
		"name":            {Kind: FText, Required: true},
		"category":        {Kind: FText, Default: "enterprise"},
		"level":           {Kind: FText, Default: "B"},
		"contact_name":    {Kind: FText},
		"contact_phone":   {Kind: FText},
		"wechat_group":    {Kind: FText},
		"settlement_type": {Kind: FText},
		"credit_limit":    {Kind: FDecimal, Default: "0"},
		"credit_days":     {Kind: FInt, Default: int64(30)},
		"billing_day":     {Kind: FInt, Default: int64(1)},
		"is_active":       {Kind: FBool, Default: true},
	},
}

var VehicleWrite = WriteCfg{
	Table: "md_vehicle", Model: "Vehicle", Alias: "v", SoftDelete: true,
	Fields: map[string]Field{
		"plate_no":               {Kind: FText, Required: true, Unique: true, Label: "车牌号"},
		"vehicle_type":           {Kind: FText},
		"vehicle_class":          {Kind: FText, Default: "rigid"},
		"body_type":              {Kind: FText},
		"dispatch_source":        {Kind: FText, Default: "own"},
		"ownership_type":         {Kind: FText},
		"carrier":                {Kind: FUUID, Ref: "md_carrier"},
		"load_capacity_ton":      {Kind: FDecimal, Default: "0"},
		"volume_capacity_cbm":    {Kind: FDecimal, Default: "0"},
		"vehicle_length_m":       {Kind: FDecimal, Default: "0"},
		"road_transport_cert_no": {Kind: FText},
		"inspection_expiry":      {Kind: FDate},
		"insurance_expiry":       {Kind: FDate},
		"maintenance_due_date":   {Kind: FDate},
		"is_active":              {Kind: FBool, Default: true},
	},
}

var DriverWrite = WriteCfg{
	Table: "md_driver", Model: "Driver", Alias: "d", SoftDelete: true,
	Fields: map[string]Field{
		"name":                  {Kind: FText, Required: true},
		"phone":                 {Kind: FText},
		"wechat":                {Kind: FText},
		"id_no":                 {Kind: FText},
		"license_no":            {Kind: FText},
		"license_type":          {Kind: FText},
		"license_expiry":        {Kind: FDate},
		"qualification_cert_no": {Kind: FText},
		"qualification_expiry":  {Kind: FDate},
		"employment_type":       {Kind: FText, Default: "employee"},
		"carrier":               {Kind: FUUID, Ref: "md_carrier"},
		"app_registered":        {Kind: FBool, Default: false},
		"is_active":             {Kind: FBool, Default: true},
	},
}

var CarrierWrite = WriteCfg{
	Table: "md_carrier", Model: "Carrier", Alias: "ca", SoftDelete: true,
	Fields: map[string]Field{
		"code":                 {Kind: FText, Required: true, Unique: true, Label: "编码"},
		"name":                 {Kind: FText, Required: true},
		"carrier_type":         {Kind: FText, Default: "owner_fleet"},
		"grade":                {Kind: FText, Default: "B"},
		"city":                 {Kind: FText},
		"service_area":         {Kind: FText},
		"contact_name":         {Kind: FText},
		"contact_phone":        {Kind: FText},
		"settlement_type":      {Kind: FText},
		"credit_limit":         {Kind: FDecimal, Default: "0"},
		"credit_days":          {Kind: FInt, Default: int64(30)},
		"billing_day":          {Kind: FInt, Default: int64(1)},
		"business_license_no":  {Kind: FText},
		"transport_license_no": {Kind: FText},
		"tax_no":               {Kind: FText},
		"qualification_expiry": {Kind: FDate},
		"contract_expiry":      {Kind: FDate},
		"insurance_expiry":     {Kind: FDate},
		"blacklisted":          {Kind: FBool, Default: false},
		"blacklist_reason":     {Kind: FText},
		"is_active":            {Kind: FBool, Default: true},
	},
}

var B2BWrite = WriteCfg{
	Table: "md_b2b_partner", Model: "B2BPartner", Alias: "p", SoftDelete: true,
	Fields: map[string]Field{
		"partner_type":  {Kind: FEnum, Choices: []string{"shipper", "consignee", "supplier"}, Required: true},
		"code":          {Kind: FText, Required: true, Unique: true, Label: "编码"},
		"name":          {Kind: FText, Required: true},
		"contact_name":  {Kind: FText},
		"contact_phone": {Kind: FText},
		"address":       {Kind: FText},
		"city":          {Kind: FText},
		"is_active":     {Kind: FBool, Default: true},
	},
}

// ── 新增资源：线路 ──

var RoutesCfg = ResourceCfg{
	SelectSQL: `
SELECT r.id::text AS id, r.code, r.name, r.origin, r.destination, r.waypoints,
       r.corridor_m::text AS corridor_m, r.distance_km::text AS distance_km, r.is_active`,
	FromClause:   "FROM md_route r",
	SearchCols:   []string{"r.code", "r.name", "r.origin", "r.destination"},
	OrderingCols: map[string]string{"code": "r.code", "created_at": "r.created_at"},
	FilterFields: map[string]filters.FilterField{},
	DirectParams: map[string]string{"is_active": "r.is_active"},
	DefaultOrder: "ORDER BY r.code, r.id",
}

var RouteWrite = WriteCfg{
	Table: "md_route", Model: "Route", Alias: "r", SoftDelete: true,
	Fields: map[string]Field{
		"code":        {Kind: FText, Required: true},
		"name":        {Kind: FText, Required: true},
		"origin":      {Kind: FText},
		"destination": {Kind: FText},
		"waypoints":   {Kind: FJSON, Default: "[]"},
		"corridor_m":  {Kind: FDecimal, Default: "2000"},
		"distance_km": {Kind: FDecimal, Default: "0"},
		"is_active":   {Kind: FBool, Default: true},
	},
}

// ── 新增资源：承运商线路价 ──

var LanePricesCfg = ResourceCfg{
	SelectSQL: `
SELECT l.id::text AS id, l.carrier_id::text AS carrier, COALESCE(ca.name,'') AS carrier_name,
       l.origin_city, l.dest_city, l.vehicle_type, l.vehicle_length_m::text AS vehicle_length_m,
       l.standard_price::text AS standard_price, l.min_price::text AS min_price,
       l.max_price::text AS max_price, l.last_deal_price::text AS last_deal_price,
       l.effective_from::text AS effective_from, l.effective_to::text AS effective_to,
       l.is_preferred, l.is_recommended, l.note, l.is_active`,
	FromClause: "FROM md_carrier_lane_price l LEFT JOIN md_carrier ca ON ca.id = l.carrier_id",
	SearchCols: []string{"l.origin_city", "l.dest_city", "l.vehicle_type", "ca.name"},
	OrderingCols: map[string]string{
		"origin_city": "l.origin_city", "dest_city": "l.dest_city", "standard_price": "l.standard_price",
		"created_at": "l.created_at", "last_deal_price": "l.last_deal_price",
		"carrier__name": "ca.name", "vehicle_type": "l.vehicle_type",
	},
	FilterFields: map[string]filters.FilterField{},
	DirectParams: map[string]string{
		"carrier": "l.carrier_id::text", "origin_city": "l.origin_city",
		"dest_city": "l.dest_city", "vehicle_type": "l.vehicle_type", "is_active": "l.is_active",
	},
	DefaultOrder: "ORDER BY l.origin_city, l.dest_city, l.standard_price, l.id",
}

var LanePriceWrite = WriteCfg{
	Table: "md_carrier_lane_price", Model: "CarrierLanePrice", Alias: "l", SoftDelete: true,
	Fields: map[string]Field{
		"carrier":          {Kind: FUUID, Ref: "md_carrier", Required: true},
		"origin_city":      {Kind: FText, Required: true},
		"dest_city":        {Kind: FText, Required: true},
		"vehicle_type":     {Kind: FText},
		"vehicle_length_m": {Kind: FDecimal, Default: "0"},
		"standard_price":   {Kind: FDecimal, Default: "0"},
		"min_price":        {Kind: FDecimal, Default: "0"},
		"max_price":        {Kind: FDecimal, Default: "0"},
		"last_deal_price":  {Kind: FDecimal, Default: "0"},
		"effective_from":   {Kind: FDate},
		"effective_to":     {Kind: FDate},
		"is_preferred":     {Kind: FBool, Default: false},
		"is_recommended":   {Kind: FBool, Default: false},
		"note":             {Kind: FText},
		"is_active":        {Kind: FBool, Default: true},
	},
}

// ── 新增资源：司机证件 ──

var DriverCredsCfg = ResourceCfg{
	SelectSQL: `
SELECT dc.id::text AS id, dc.driver_id::text AS driver, COALESCE(d.name,'') AS driver_name,
       dc.cred_type,
       (CASE dc.cred_type WHEN 'vehicle_license' THEN '车头行驶证' WHEN 'trailer_license' THEN '车挂行驶证'
                          WHEN 'driving_license' THEN '驾驶证' WHEN 'transport_cert' THEN '道路运输证'
                          WHEN 'id_card' THEN '身份证' ELSE dc.cred_type END) AS cred_type_label,
       dc.side,
       (CASE dc.side WHEN 'main' THEN '主页/正面' WHEN 'back' THEN '副页/反面' ELSE dc.side END) AS side_label,
       NULLIF(dc.file,'') AS file, COALESCE(NULLIF(dc.file,''), dc.file_url) AS file_display, dc.file_url,
       dc.ocr_status, dc.ocr_result, dc.holder_name, dc.cert_no,
       dc.expiry_date::text AS expiry_date, dc.self_uploaded, dc.created_at`,
	FromClause:   "FROM md_driver_credential dc LEFT JOIN md_driver d ON d.id = dc.driver_id",
	SearchCols:   []string{"dc.holder_name", "dc.cert_no", "d.name"},
	OrderingCols: map[string]string{"created_at": "dc.created_at", "expiry_date": "dc.expiry_date"},
	FilterFields: map[string]filters.FilterField{},
	DirectParams: map[string]string{
		"driver": "dc.driver_id::text", "cred_type": "dc.cred_type", "ocr_status": "dc.ocr_status",
	},
	// Django ordering=["driver",…]：按关联模型 Driver 的 Meta.ordering（name）排序
	DefaultOrder: "ORDER BY d.name, dc.cred_type, dc.side, dc.id",
}

var DriverCredWrite = WriteCfg{
	Table: "md_driver_credential", Model: "DriverCredential", Alias: "dc",
	Fields: map[string]Field{
		"driver":        {Kind: FUUID, Ref: "md_driver", Required: true},
		"cred_type":     {Kind: FEnum, Choices: []string{"vehicle_license", "trailer_license", "driving_license", "transport_cert", "id_card"}, Required: true},
		"side":          {Kind: FEnum, Choices: []string{"main", "back"}, Default: "main"},
		"file_url":      {Kind: FText},
		"holder_name":   {Kind: FText},
		"cert_no":       {Kind: FText},
		"expiry_date":   {Kind: FDate},
		"self_uploaded": {Kind: FBool, Default: false},
		"ocr_status":    {Kind: FText, Default: "pending"},
		"ocr_result":    {Kind: FJSON, Default: "{}"},
		"file":          {Kind: FText, Default: ""},
	},
	AfterWrite: CredentialAfterWrite, // 上传即触发 OCR 建档
}

// 导出既有列表配置供路由绑定；详情态补上 DRF 里「仅 retrieve 计算」的重聚合字段
var (
	CustomersCfg = withDetail(customersCfg, map[string]string{
		// customer_history：非取消订单数 + 出现频次 Top3 线路
		"history": `SELECT json_build_object(
			'order_count', (SELECT count(*) FROM ops_order o
			                WHERE o.customer_id=c.id AND NOT o.is_deleted AND o.status <> 'cancelled'),
			'common_routes', COALESCE((SELECT json_agg(rt) FROM (
			   SELECT COALESCE(NULLIF(o.origin,''),'?')||'→'||COALESCE(NULLIF(o.destination,''),'?') AS rt,
			          count(*) AS n
			   FROM ops_order o
			   WHERE o.customer_id=c.id AND NOT o.is_deleted AND o.status <> 'cancelled'
			     AND (o.origin <> '' OR o.destination <> '')
			   GROUP BY 1 ORDER BY n DESC, rt LIMIT 3) t), '[]'::json))`,
	})
	VehiclesCfg = withDetail(vehiclesCfg, map[string]string{
		// vehicle_freight_total：该车全部运单的应付合计
		"freight_total": `SELECT COALESCE(sum(e.amount),0)::float8
			FROM fin_expense_record e JOIN ops_waybill w ON w.id=e.waybill_id
			WHERE w.vehicle_id=v.id AND e.direction='payable'`,
	})
	DriversCfg  = driversCfg
	CarriersCfg = withDetail(carriersCfg, map[string]string{
		// carrier_performance(近 90 天) + frequent_routes(Top5)
		"performance": carrierPerformanceSQL,
	})
	B2BCfg = b2bCfg
)

func withDetail(cfg ResourceCfg, extras map[string]string) ResourceCfg {
	cfg.DetailExtras = extras
	return cfg
}

// carrierPerformanceSQL 对齐 apps/ops/carrier_scoring.{carrier_performance, frequent_routes}
// 基线：无样本时 on_time=0.85 / exception=0.10 / receipt_timely=0.88（_BASELINE）
const carrierPerformanceSQL = `
SELECT json_build_object(
  'deals', s.total,
  'route_hits', 0,
  'on_time_rate', CASE WHEN s.timed_total > 0
      THEN round(s.on_time_hits::numeric / s.timed_total, 4)::float8 ELSE 0.85 END,
  'exception_rate', CASE WHEN s.total > 0
      THEN round(s.exc_total::numeric / s.total, 4)::float8
      ELSE (1::float8 - 0.9::float8) END,  -- Django: 1 - _BASELINE['low_exception']，IEEE754 下为 0.09999999999999998
  'receipt_timely_rate', CASE WHEN s.done_total > 0
      THEN round(s.receipt_hits::numeric / s.done_total, 4)::float8 ELSE 0.88 END,
  'recent_deal_price', NULL,
  'has_history', s.total > 0,
  'frequent_routes', COALESCE((SELECT json_agg(json_build_object(
       'origin', f.origin, 'destination', f.destination, 'deals', f.deals)) FROM (
     SELECT w.origin, w.destination, count(*) AS deals FROM ops_waybill w
     WHERE w.carrier_id=ca.id AND w.created_at >= now() - interval '90 days'
       AND w.status <> 'voided' AND w.origin <> '' AND w.destination <> ''
     GROUP BY w.origin, w.destination ORDER BY deals DESC LIMIT 5) f), '[]'::json))
FROM (SELECT
    count(*) AS total,
    count(*) FILTER (WHERE w.planned_arrival IS NOT NULL AND w.arrived_at IS NOT NULL) AS timed_total,
    count(*) FILTER (WHERE w.planned_arrival IS NOT NULL AND w.arrived_at IS NOT NULL
                       AND w.arrived_at <= w.planned_arrival) AS on_time_hits,
    count(*) FILTER (WHERE EXISTS (SELECT 1 FROM ops_exception x WHERE x.waybill_id=w.id)) AS exc_total,
    count(*) FILTER (WHERE w.status IN ('arrived','signed','delivered','settled')) AS done_total,
    count(*) FILTER (WHERE w.status IN ('arrived','signed','delivered','settled')
                       AND w.receipt_status IN ('returned','audited')) AS receipt_hits
  FROM ops_waybill w
  WHERE w.carrier_id = ca.id AND w.created_at >= now() - interval '90 days' AND w.status <> 'voided') s`
