// Package resources 汇总各域「标准 ModelViewSet」资源的读写契约声明。
//
// 这些资源在 Django 侧几乎没有定制逻辑（ModelSerializer + filterset_fields +
// search_fields + ordering），因此不写 handler，只声明一份 ResourceCfg（读）与
// WriteCfg（写），由 masterdata 的通用引擎驱动全套 CRUD。
//
// 逐资源需要对齐的四件事：
//  1. 默认排序 = ViewSet.ordering，缺省时回落到模型 Meta.ordering；
//  2. 序列化字段名/顺序与类型（DecimalField→字符串、DateField→字符串、
//     get_X_display() 标签列内联在 SQL 里）；
//  3. filterset_fields / search_fields / ordering_fields 三组查询参数；
//  4. 数据范围（OrgScopedQuerysetMixin）—— 注意若 ViewSet 自己重写了
//     get_queryset，Mixin 的实现会被 MRO 覆盖，此时**不做**范围收窄。
package resources

import (
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
)

type (
	cfg = masterdata.ResourceCfg
	wcf = masterdata.WriteCfg
	fld = masterdata.Field
	ff  = filters.FilterField
)

const (
	fText     = masterdata.FText
	fEnum     = masterdata.FEnum
	fInt      = masterdata.FInt
	fDecimal  = masterdata.FDecimal
	fBool     = masterdata.FBool
	fDate     = masterdata.FDate
	fDateTime = masterdata.FDateTime
	fUUID     = masterdata.FUUID
	fJSON     = masterdata.FJSON
	fURL      = masterdata.FURL
)

// ─────────────────────────── iam：部门 / 员工组 / 权限点 ───────────────────────────

// DepartmentsCfg /api/v1/org/departments
var DepartmentsCfg = cfg{
	SelectSQL: `
SELECT dp.id::text AS id, dp.organization_id::text AS organization, COALESCE(og.name,'') AS organization_name,
       dp.parent_id::text AS parent, COALESCE(pp.name,'') AS parent_name,
       dp.code, dp.name, dp.manager_id::text AS manager, COALESCE(mg.name,'') AS manager_name,
       dp.sort_order, dp.is_active`,
	FromClause: `FROM iam_department dp
LEFT JOIN iam_organization og ON og.id = dp.organization_id
LEFT JOIN iam_department pp ON pp.id = dp.parent_id
LEFT JOIN iam_employee mg ON mg.id = dp.manager_id`,
	SearchCols:   []string{"dp.code", "dp.name"},
	OrderingCols: map[string]string{"sort_order": "dp.sort_order", "code": "dp.code"},
	DirectParams: map[string]string{
		"organization": "dp.organization_id::text", "is_active": "dp.is_active", "parent": "dp.parent_id::text",
	},
	DefaultOrder: "ORDER BY dp.sort_order, dp.code, dp.id",
	PartialOmit: map[string]string{
		"organization_name": "dp.organization_id", "parent_name": "dp.parent_id",
		"manager_name": "dp.manager_id",
	},
}

var DepartmentWrite = wcf{
	Table: "iam_department", Model: "Department", Verbose: "部门", Alias: "dp",
	ReadPerm: "org.view", WritePerm: "org.manage",
	Fields: map[string]fld{
		"organization": {Kind: fUUID, Ref: "iam_organization", Required: true},
		"parent":       {Kind: fUUID, Ref: "iam_department"},
		"code":         {Kind: fText, Required: true},
		"name":         {Kind: fText, Required: true},
		"manager":      {Kind: fUUID, Ref: "iam_employee"},
		"sort_order":   {Kind: fInt, Default: int64(0)},
		"is_active":    {Kind: fBool, Default: true},
	},
}

// EmployeeGroupsCfg /api/v1/org/employee-groups —— member_count 仅详情统计（列表恒 null）
var EmployeeGroupsCfg = cfg{
	SelectSQL: `
SELECT g.id::text AS id, g.code, g.name, g.description,
       COALESCE((SELECT json_agg(ro.id::text ORDER BY ro.code)
                 FROM iam_employee_group_roles gr JOIN iam_role ro ON ro.id = gr.role_id
                 WHERE gr.employeegroup_id = g.id), '[]'::json) AS roles,
       g.is_active, NULL::int AS member_count`,
	FromClause:   "FROM iam_employee_group g",
	SearchCols:   []string{"g.code", "g.name"},
	OrderingCols: map[string]string{"code": "g.code"},
	DirectParams: map[string]string{"is_active": "g.is_active"},
	DefaultOrder: "ORDER BY g.code, g.id",
	DetailExtras: map[string]string{
		"member_count": "SELECT count(*)::int FROM iam_employee_groups eg WHERE eg.employeegroup_id = g.id",
	},
}

var EmployeeGroupWrite = wcf{
	Table: "iam_employee_group", Model: "EmployeeGroup", Verbose: "用户组", Alias: "g",
	ReadPerm: "org.view", WritePerm: "org.manage",
	Fields: map[string]fld{
		"code":        {Kind: fText, Required: true, Unique: true, Label: "code"},
		"name":        {Kind: fText, Required: true},
		"description": {Kind: fText},
		"is_active":   {Kind: fBool, Default: true},
	},
	AfterWrite: setEmployeeGroupRoles,
	CascadeTables: map[string]string{
		"iam_employee_group_roles": "employeegroup_id",
		"iam_employee_groups":      "employeegroup_id",
	},
}

// PermissionsCfg /api/v1/org/permissions（ReadOnlyModelViewSet）
var PermissionsCfg = cfg{
	SelectSQL:    `SELECT pm.id::text AS id, pm.code, pm.name, pm.module`,
	FromClause:   "FROM iam_permission pm",
	SearchCols:   []string{"pm.code", "pm.name", "pm.module"},
	OrderingCols: map[string]string{"module": "pm.module", "code": "pm.code"},
	DirectParams: map[string]string{"module": "pm.module"},
	DefaultOrder: "ORDER BY pm.code, pm.id",
}

var PermissionWrite = wcf{
	Table: "iam_permission", Model: "Permission", Alias: "pm", ReadOnly: true,
	ReadPerm: "org.rbac",
}

// ─────────────────────────── ops：模板 / 提醒 / 回单 / 派车批次 ───────────────────────────

// OrderTemplatesCfg /api/v1/order-templates
var OrderTemplatesCfg = cfg{
	SelectSQL: `
SELECT ot.id::text AS id, ot.name, ot.payload, COALESCE(cu.username,'') AS created_by_name, ot.created_at`,
	FromClause:    "FROM ops_order_template ot LEFT JOIN accounts_user cu ON cu.id = ot.created_by_id",
	SearchCols:    []string{"ot.name"},
	OrderingCols:  map[string]string{"created_at": "ot.created_at", "name": "ot.name"},
	DefaultOrder:  "ORDER BY ot.created_at DESC, ot.id",
	SoftDeleteCol: "ot.is_deleted",
	PartialOmit:   map[string]string{"created_by_name": "ot.created_by_id"},
}

var OrderTemplateWrite = wcf{
	Table: "ops_order_template", Model: "OrderTemplate", Alias: "ot", SoftDelete: true,
	Fields: map[string]fld{
		"name":       {Kind: fText, Required: true},
		"payload":    {Kind: fJSON, Default: "{}"},
		"created_by": {Kind: fUUID, Ref: "accounts_user"},
	},
	BeforeCreate: stampCreatedBy, // 对齐 perform_create(created_by=request.user)
}

// ReminderTemplatesCfg /api/v1/reminder-templates
var ReminderTemplatesCfg = cfg{
	SelectSQL:  `SELECT rt.id::text AS id, rt.name, rt.category, rt.content, rt.is_active, rt.created_at`,
	FromClause: "FROM ops_reminder_template rt",
	SearchCols: []string{"rt.name", "rt.category"},
	OrderingCols: map[string]string{
		"category": "rt.category", "name": "rt.name", "created_at": "rt.created_at",
	},
	DirectParams:  map[string]string{"category": "rt.category", "is_active": "rt.is_active"},
	DefaultOrder:  "ORDER BY rt.category, rt.name, rt.id",
	SoftDeleteCol: "rt.is_deleted",
}

var ReminderTemplateWrite = wcf{
	Table: "ops_reminder_template", Model: "ReminderTemplate", Alias: "rt", SoftDelete: true,
	Fields: map[string]fld{
		"name":       {Kind: fText, Required: true},
		"category":   {Kind: fText},
		"content":    {Kind: fText, Required: true},
		"is_active":  {Kind: fBool, Default: true},
		"created_by": {Kind: fUUID, Ref: "accounts_user"},
	},
	BeforeCreate: stampCreatedBy,
}

// DriverRemindersCfg /api/v1/reminders（http_method_names 收窄为 get/post）
var DriverRemindersCfg = cfg{
	SelectSQL: `
SELECT dr.id::text AS id, dr.waybill_id::text AS waybill, COALESCE(wb.waybill_no,'') AS waybill_no,
       dr.driver_id::text AS driver, COALESCE(dv.name,'') AS driver_name,
       dr.template_id::text AS template, dr.title, dr.content, dr.ack_required, dr.status,
       dr.sent_at, dr.acknowledged_at`,
	FromClause: `FROM ops_driver_reminder dr
LEFT JOIN ops_waybill wb ON wb.id = dr.waybill_id
LEFT JOIN md_driver dv ON dv.id = dr.driver_id`,
	OrderingCols: map[string]string{"sent_at": "dr.sent_at"},
	DirectParams: map[string]string{
		"driver": "dr.driver_id::text", "waybill": "dr.waybill_id::text", "status": "dr.status",
	},
	DefaultOrder: "ORDER BY dr.sent_at DESC, dr.id",
	PartialOmit: map[string]string{
		"waybill_no": "dr.waybill_id", "driver_name": "dr.driver_id",
	},
}

var DriverReminderWrite = wcf{
	Table: "ops_driver_reminder", Model: "DriverReminder", Alias: "dr",
	NoUpdate: true, NoDelete: true,
	Fields: map[string]fld{
		"waybill":         {Kind: fUUID, Ref: "ops_waybill"},
		"driver":          {Kind: fUUID, Ref: "md_driver"},
		"template":        {Kind: fUUID, Ref: "ops_reminder_template"},
		"title":           {Kind: fText, Default: "作业提醒"},
		"content":         {Kind: fText, Required: true},
		"ack_required":    {Kind: fBool, Default: true},
		"status":          {Kind: fText, Default: "pending"},
		"sent_at":         {Kind: fDateTime},
		"acknowledged_at": {Kind: fDateTime},
	},
	// level 不在序列化器里，但模型 default='important'（通用零值补齐会写成 ''）
	ModelDefaults: map[string]any{"level": "important"},
	BeforeCreate:  stampSentAt,
}

// ReceiptsCfg /api/v1/receipts —— 按运单组织归属数据范围
var ReceiptsCfg = cfg{
	SelectSQL: `
SELECT rc.id::text AS id, rc.waybill_id::text AS waybill, COALESCE(wb.waybill_no,'') AS waybill_no,
       rc.receipt_type, rc.status, NULLIF(rc.file,'') AS file,
       COALESCE(NULLIF(rc.file,''), rc.file_url) AS file_display, rc.file_url,
       rc.ocr_status, rc.ocr_result, rc.signatory, rc.signed_at, rc.created_at,
       rc.outcome, rc.total_quantity::text AS total_quantity, rc.signed_quantity::text AS signed_quantity,
       rc.damaged_quantity::text AS damaged_quantity, rc.shortage_quantity::text AS shortage_quantity,
       rc.rejection_reason`,
	FromClause: "FROM ops_receipt rc LEFT JOIN ops_waybill wb ON wb.id = rc.waybill_id",
	DirectParams: map[string]string{
		"waybill": "rc.waybill_id::text", "status": "rc.status", "ocr_status": "rc.ocr_status",
	},
	DefaultOrder:     "ORDER BY rc.created_at DESC, rc.id",
	ScopeOrgCol:      "SELECT sw.organization_id FROM ops_waybill sw WHERE sw.id = rc.waybill_id",
	ScopeIncludeNull: false,
	PartialOmit:      map[string]string{"waybill_no": "rc.waybill_id"},
}

var ReceiptWrite = wcf{
	Table: "ops_receipt", Model: "Receipt", Alias: "rc",
	Fields: map[string]fld{
		"waybill":           {Kind: fUUID, Ref: "ops_waybill", Required: true},
		"receipt_type":      {Kind: fText, Default: "signed_pod"},
		"file":              {Kind: fText},
		"file_url":          {Kind: fURL},
		"signatory":         {Kind: fText},
		"signed_at":         {Kind: fDateTime},
		"outcome":           {Kind: fEnum, Choices: []string{"full", "partial", "rejected"}, Default: "full"},
		"total_quantity":    {Kind: fDecimal, Default: "0"},
		"signed_quantity":   {Kind: fDecimal, Default: "0"},
		"damaged_quantity":  {Kind: fDecimal, Default: "0"},
		"shortage_quantity": {Kind: fDecimal, Default: "0"},
		"rejection_reason":  {Kind: fText},
		"uploaded_by":       {Kind: fUUID, Ref: "accounts_user"},
	},
	// status / ocr_status / ocr_result 为 read_only：由模型 default 与 OCR 流程决定
	ModelDefaults: map[string]any{"status": "uploaded", "ocr_status": "pending", "ocr_result": "{}"},
	BeforeCreate:  stampUploadedBy,
	AfterWrite:    kickReceiptOCR, // 对齐 perform_create 里的 process_receipt_ocr.delay
}

// DispatchBatchesCfg /api/v1/dispatch-batches（ReadOnlyModelViewSet）
// 注：ViewSet 自带 get_queryset，MRO 上覆盖了 OrgScopedQuerysetMixin —— 不做范围收窄。
var DispatchBatchesCfg = cfg{
	SelectSQL: `
SELECT db.id::text AS id, db.batch_no, db.dispatch_type,
       (CASE db.dispatch_type WHEN 'third_party' THEN '外包承运商' WHEN 'platform' THEN '网货平台'
                              ELSE db.dispatch_type END) AS dispatch_type_label,
       db.carrier_id::text AS carrier, COALESCE(cr.name,'') AS carrier_name, db.platform_name,
       db.status,
       (CASE db.status WHEN 'draft' THEN '草稿' WHEN 'dispatched' THEN '已派车' WHEN 'partial' THEN '部分完成'
                       WHEN 'completed' THEN '已完成' WHEN 'cancelled' THEN '已取消' ELSE db.status END) AS status_label,
       db.allocation,
       (CASE db.allocation WHEN 'by_weight' THEN '按吨占比' WHEN 'even' THEN '均摊'
                           WHEN 'manual' THEN '逐单指定' ELSE db.allocation END) AS allocation_label,
       db.total_payable::text AS total_payable, db.order_count,
       db.total_weight_ton::text AS total_weight_ton, db.note, db.statement_no,
       COALESCE(NULLIF(cu.nickname,''), cu.username, '') AS created_by_name,
       COALESCE(cs.j, '[]'::json) AS customer_summary, db.created_at,
       NULL::json AS waybills`,
	FromClause: `FROM ops_dispatch_batch db
LEFT JOIN md_carrier cr ON cr.id = db.carrier_id
LEFT JOIN accounts_user cu ON cu.id = db.created_by_id
LEFT JOIN LATERAL (
  -- 批次涉及的客户去重，顺序对齐 Waybill.Meta.ordering = ['-created_at']
  SELECT json_agg(t.nm ORDER BY t.ord) AS j FROM (
    SELECT DISTINCT ON (nm) nm, ord FROM (
      SELECT COALESCE(NULLIF(wc.name,''), oc.name, '') AS nm,
             row_number() OVER (ORDER BY w.created_at DESC, w.id) AS ord
      FROM ops_waybill w
      LEFT JOIN md_customer wc ON wc.id = w.customer_id
      LEFT JOIN ops_order o ON o.id = w.order_id
      LEFT JOIN md_customer oc ON oc.id = o.customer_id
      WHERE w.batch_id = db.id
    ) z WHERE nm <> '' ORDER BY nm, ord
  ) t
) cs ON true`,
	SearchCols: []string{"db.batch_no", "cr.name", "db.platform_name"},
	OrderingCols: map[string]string{
		"created_at": "db.created_at", "batch_no": "db.batch_no", "status": "db.status",
		"dispatch_type": "db.dispatch_type", "total_payable": "db.total_payable",
		"order_count": "db.order_count", "total_weight_ton": "db.total_weight_ton",
		"carrier__name": "cr.name",
	},
	FilterFields: map[string]ff{
		"batch_no":   {Type: filters.Text, Cols: []string{"db.batch_no"}},
		"carrier":    {Type: filters.Text, Cols: []string{"cr.name"}},
		"channel":    {Type: filters.Enum, Cols: []string{"db.dispatch_type"}},
		"status":     {Type: filters.Enum, Cols: []string{"db.status"}},
		"allocation": {Type: filters.Enum, Cols: []string{"db.allocation"}},
		"payable":    {Type: filters.Number, Cols: []string{"db.total_payable"}},
		"count":      {Type: filters.Number, Cols: []string{"db.order_count"}},
		"weight":     {Type: filters.Number, Cols: []string{"db.total_weight_ton"}},
		"created_at": {Type: filters.Date, Cols: []string{"db.created_at"}},
	},
	DirectParams: map[string]string{"status": "db.status", "carrier": "db.carrier_id::text"},
	DefaultOrder: "ORDER BY db.created_at DESC, db.id",
	// 详情序列化器额外展开批次内运单（BatchWaybillSerializer）
	DetailExtras: map[string]string{
		"waybills": `SELECT COALESCE(json_agg(json_build_object(
            'id', w.id::text, 'waybill_no', w.waybill_no,
            'order_no', COALESCE(o.order_no,''), 'customer_name', COALESCE(wc.name,''),
            'origin', w.origin, 'destination', w.destination,
            'cargo_weight_ton', w.cargo_weight_ton::text, 'cargo_quantity', w.cargo_quantity,
            'status', w.status, 'status_label', ` + waybillStatusLabel + `,
            'payable', (SELECT e.amount::float8 FROM fin_expense_record e
                        WHERE e.waybill_id = w.id AND e.direction='payable'
                          AND e.expense_item_code='TRANSPORT_COST'
                        ORDER BY e.created_at DESC, e.id LIMIT 1))
            ORDER BY w.created_at DESC, w.id), '[]'::json)
          FROM ops_waybill w
          LEFT JOIN ops_order o ON o.id = w.order_id
          LEFT JOIN md_customer wc ON wc.id = w.customer_id
          WHERE w.batch_id = db.id`,
	},
}

var DispatchBatchWrite = wcf{
	Table: "ops_dispatch_batch", Model: "DispatchBatch", Alias: "db", ReadOnly: true,
}

// ─────────────────────────── telematics：设备 / 围栏 / 报警 ───────────────────────────

// DevicesCfg /api/v1/devices
var DevicesCfg = cfg{
	SelectSQL: `
SELECT dv.id::text AS id, dv.device_no, dv.device_type, dv.vehicle_id::text AS vehicle,
       COALESCE(vh.plate_no,'') AS vehicle_plate, dv.sim_no, dv.status, dv.last_seen_at,
       dv.meta, dv.created_at`,
	FromClause: "FROM tel_device dv LEFT JOIN md_vehicle vh ON vh.id = dv.vehicle_id",
	SearchCols: []string{"dv.device_no", "dv.sim_no"},
	DirectParams: map[string]string{
		"device_type": "dv.device_type", "status": "dv.status", "vehicle": "dv.vehicle_id::text",
	},
	DefaultOrder: "ORDER BY dv.device_no, dv.id",
	PartialOmit:  map[string]string{"vehicle_plate": "dv.vehicle_id"},
}

var DeviceWrite = wcf{
	Table: "tel_device", Model: "Device", Verbose: "车载终端", Alias: "dv",
	ReadPerm: "telematics.view", WritePerm: "telematics.manage",
	Fields: map[string]fld{
		"device_no": {Kind: fText, Required: true, Unique: true, Label: "device no"},
		"device_type": {Kind: fEnum, Default: "gps",
			Choices: []string{"gps", "beidou", "temperature", "fuel", "etc", "adas", "dsm"}},
		"vehicle": {Kind: fUUID, Ref: "md_vehicle"},
		"sim_no":  {Kind: fText},
		"meta":    {Kind: fJSON, Default: "{}"},
	},
	// status / last_seen_at 为 read_only：由上报流程维护
	ModelDefaults: map[string]any{"status": "offline"},
}

// GeofencesCfg /api/v1/geofences
var GeofencesCfg = cfg{
	SelectSQL: `
SELECT gf.id::text AS id, gf.name, gf.shape, gf.purpose,
       gf.center_lng::text AS center_lng, gf.center_lat::text AS center_lat,
       gf.radius_m::text AS radius_m, gf.polygon, gf.is_active, gf.created_at`,
	FromClause: "FROM tel_geofence gf",
	SearchCols: []string{"gf.name"},
	DirectParams: map[string]string{
		"shape": "gf.shape", "purpose": "gf.purpose", "is_active": "gf.is_active",
	},
	DefaultOrder: "ORDER BY gf.name, gf.id",
}

var GeofenceWrite = wcf{
	Table: "tel_geofence", Model: "Geofence", Alias: "gf",
	ReadPerm: "telematics.view", WritePerm: "telematics.manage",
	Fields: map[string]fld{
		"name":       {Kind: fText, Required: true},
		"shape":      {Kind: fEnum, Choices: []string{"circle", "polygon"}, Default: "circle"},
		"purpose":    {Kind: fEnum, Choices: []string{"warehouse", "route", "restricted"}, Default: "warehouse"},
		"center_lng": {Kind: fDecimal},
		"center_lat": {Kind: fDecimal},
		"radius_m":   {Kind: fDecimal, Default: "0"},
		"polygon":    {Kind: fJSON, Default: "[]"},
		"is_active":  {Kind: fBool, Default: true},
	},
}

// AlertsCfg /api/v1/alerts（list + retrieve + ack/close 自定义动作）
var AlertsCfg = cfg{
	SelectSQL: `
SELECT al.id::text AS id, al.alert_type, al.level, al.status,
       al.vehicle_id::text AS vehicle, COALESCE(vh.plate_no,'') AS vehicle_plate,
       al.device_id::text AS device, COALESCE(dv.device_no,'') AS device_no,
       al.waybill_id::text AS waybill, COALESCE(wb.waybill_no,'') AS waybill_no,
       al.message, al.value::text AS value, al.threshold::text AS threshold,
       al.detail, al.triggered_at, al.handled_at, al.created_at`,
	FromClause: `FROM tel_alert al
LEFT JOIN md_vehicle vh ON vh.id = al.vehicle_id
LEFT JOIN tel_device dv ON dv.id = al.device_id
LEFT JOIN ops_waybill wb ON wb.id = al.waybill_id`,
	SearchCols: []string{"al.message"},
	DirectParams: map[string]string{
		"alert_type": "al.alert_type", "level": "al.level", "status": "al.status",
		"vehicle": "al.vehicle_id::text", "waybill": "al.waybill_id::text",
	},
	DefaultOrder: "ORDER BY al.triggered_at DESC, al.id",
}

var AlertWrite = wcf{
	Table: "tel_alert", Model: "Alert", Alias: "al", ReadOnly: true,
	ReadPerm: "telematics.view",
}

// ─────────────────────────── finance：台账类标准资源 ───────────────────────────

// ExpenseItemsCfg /api/v1/expense-items
var ExpenseItemsCfg = cfg{
	SelectSQL: `
SELECT ei.id::text AS id, ei.code, ei.name, ei.direction,
       ei.debit_account_code, ei.credit_account_code, ei.is_active`,
	FromClause:   "FROM fin_expense_item ei",
	SearchCols:   []string{"ei.code", "ei.name"},
	DirectParams: map[string]string{"direction": "ei.direction", "is_active": "ei.is_active"},
	DefaultOrder: "ORDER BY ei.code, ei.id",
}

var ExpenseItemWrite = wcf{
	Table: "fin_expense_item", Model: "ExpenseItem", Verbose: "费用项", Alias: "ei",
	Fields: map[string]fld{
		"code": {Kind: fText, Required: true, Unique: true, Label: "code"},
		"name": {Kind: fText, Required: true},
		"direction": {Kind: fEnum, Required: true,
			Choices: []string{"receivable", "payable", "external"}},
		"debit_account_code":  {Kind: fText},
		"credit_account_code": {Kind: fText},
		"is_active":           {Kind: fBool, Default: true},
	},
}

// ExpenseRecordsCfg /api/v1/expense-records —— 按运单组织归属数据范围（含无归属可见）
var ExpenseRecordsCfg = cfg{
	SelectSQL: `
SELECT er.id::text AS id, er.waybill_id::text AS waybill, er.direction, er.expense_item_code,
       er.amount::text AS amount, er.currency, er.occurred_at, er.risk_status,
       er.source_system, er.external_id, er.payee_type, er.payee_ref, er.remark, er.created_at`,
	FromClause: "FROM fin_expense_record er",
	SearchCols: []string{"er.expense_item_code", "er.external_id"},
	DirectParams: map[string]string{
		"direction": "er.direction", "risk_status": "er.risk_status", "waybill": "er.waybill_id::text",
	},
	DefaultOrder:     "ORDER BY er.created_at DESC, er.id",
	ScopeOrgCol:      "SELECT sw.organization_id FROM ops_waybill sw WHERE sw.id = er.waybill_id",
	ScopeIncludeNull: true,
}

var ExpenseRecordWrite = wcf{
	Table: "fin_expense_record", Model: "ExpenseRecord", Alias: "er",
	Fields: map[string]fld{
		"waybill":           {Kind: fUUID, Ref: "ops_waybill"},
		"direction":         {Kind: fText, Required: true},
		"expense_item_code": {Kind: fText, Required: true},
		"amount":            {Kind: fDecimal, Required: true},
		"currency":          {Kind: fText, Default: "CNY"},
		"occurred_at":       {Kind: fDateTime},
		"risk_status":       {Kind: fText, Default: "normal"},
		"source_system":     {Kind: fText},
		"external_id":       {Kind: fText},
		"payee_type":        {Kind: fText},
		"payee_ref":         {Kind: fText},
		"remark":            {Kind: fText},
	},
	BeforeCreate: resolveWaybillNo, // 外部系统按业务单号推送：waybill_no → waybill
}

// PaymentRequestsCfg /api/v1/payment-requests
var PaymentRequestsCfg = cfg{
	SelectSQL: `
SELECT pr.id::text AS id, pr.request_no, pr.waybill_id::text AS waybill, pr.counterparty_type,
       pr.counterparty_ref, pr.amount::text AS amount, pr.reason, pr.status,
       pr.external_approval_no, pr.created_at`,
	FromClause:       "FROM fin_payment_request pr",
	SearchCols:       []string{"pr.request_no"},
	DirectParams:     map[string]string{"status": "pr.status"},
	DefaultOrder:     "ORDER BY pr.created_at DESC, pr.id",
	ScopeOrgCol:      "SELECT sw.organization_id FROM ops_waybill sw WHERE sw.id = pr.waybill_id",
	ScopeIncludeNull: true,
}

var PaymentRequestWrite = wcf{
	Table: "fin_payment_request", Model: "PaymentRequest", Verbose: "付款申请", Alias: "pr",
	Fields: map[string]fld{
		"request_no":           {Kind: fText, Required: true, Unique: true, Label: "request no"},
		"waybill":              {Kind: fUUID, Ref: "ops_waybill"},
		"counterparty_type":    {Kind: fText},
		"counterparty_ref":     {Kind: fText},
		"amount":               {Kind: fDecimal, Default: "0"},
		"reason":               {Kind: fText},
		"status":               {Kind: fText, Default: "created"},
		"external_approval_no": {Kind: fText},
	},
}

// PricingRulesCfg /api/v1/pricing-rules
var PricingRulesCfg = cfg{
	SelectSQL: `
SELECT pg.id::text AS id, pg.name, pg.price_type, pg.charge_method,
       (CASE pg.charge_method WHEN 'tiered_weight' THEN '按重量阶梯' WHEN 'flat' THEN '整车一口价'
                              WHEN 'per_volume' THEN '按方计费' WHEN 'per_piece' THEN '按件计费'
                              WHEN 'per_km' THEN '按公里计费' WHEN 'per_ton_km' THEN '吨公里计费'
                              ELSE pg.charge_method END) AS charge_method_label,
       pg.expense_item_code, pg.customer_id::text AS customer, COALESCE(cm.name,'') AS customer_name,
       pg.carrier_id::text AS carrier, COALESCE(cr.name,'') AS carrier_name,
       pg.route_name, pg.vehicle_type, pg.base_price::text AS base_price, pg.min_price::text AS min_price,
       pg.unit_price::text AS unit_price, pg.min_charge_qty::text AS min_charge_qty, pg.tier_prices,
       pg.volumetric_factor::text AS volumetric_factor, pg.fuel_surcharge_pct::text AS fuel_surcharge_pct,
       pg.priority, pg.is_active, pg.created_at`,
	FromClause: `FROM fin_pricing_rule pg
LEFT JOIN md_customer cm ON cm.id = pg.customer_id
LEFT JOIN md_carrier cr ON cr.id = pg.carrier_id`,
	SearchCols: []string{"pg.name", "pg.route_name", "pg.expense_item_code"},
	DirectParams: map[string]string{
		"price_type": "pg.price_type", "is_active": "pg.is_active",
		"customer": "pg.customer_id::text", "carrier": "pg.carrier_id::text",
	},
	DefaultOrder: "ORDER BY pg.priority DESC, pg.name, pg.id",
	PartialOmit: map[string]string{
		"customer_name": "pg.customer_id", "carrier_name": "pg.carrier_id",
	},
}

var PricingRuleWrite = wcf{
	Table: "fin_pricing_rule", Model: "PricingRule", Alias: "pg",
	Fields: map[string]fld{
		"name":       {Kind: fText, Required: true},
		"price_type": {Kind: fEnum, Required: true, Choices: []string{"income", "cost"}},
		"charge_method": {Kind: fEnum, Default: "tiered_weight",
			Choices: []string{"tiered_weight", "flat", "per_volume", "per_piece", "per_km", "per_ton_km"}},
		"expense_item_code":  {Kind: fText, Required: true},
		"customer":           {Kind: fUUID, Ref: "md_customer"},
		"carrier":            {Kind: fUUID, Ref: "md_carrier"},
		"route_name":         {Kind: fText},
		"vehicle_type":       {Kind: fText},
		"base_price":         {Kind: fDecimal, Default: "0"},
		"min_price":          {Kind: fDecimal, Default: "0"},
		"unit_price":         {Kind: fDecimal, Default: "0"},
		"min_charge_qty":     {Kind: fDecimal, Default: "0"},
		"tier_prices":        {Kind: fJSON, Default: "[]"},
		"volumetric_factor":  {Kind: fDecimal, Default: "0.3333"},
		"fuel_surcharge_pct": {Kind: fDecimal, Default: "0"},
		"priority":           {Kind: fInt, Default: int64(0)},
		"is_active":          {Kind: fBool, Default: true},
	},
}

// WebhooksCfg /api/v1/webhooks —— secret 为 write_only，不出现在响应里
var WebhooksCfg = cfg{
	SelectSQL: `
SELECT wh.id::text AS id, wh.name, wh.target_url, wh.events, wh.is_active, wh.created_at`,
	FromClause:   "FROM fin_webhook wh",
	SearchCols:   []string{"wh.name", "wh.target_url"},
	DirectParams: map[string]string{"is_active": "wh.is_active"},
	DefaultOrder: "ORDER BY wh.created_at DESC, wh.id",
}

var WebhookWrite = wcf{
	Table: "fin_webhook", Model: "Webhook", Alias: "wh",
	Fields: map[string]fld{
		"name":       {Kind: fText, Required: true},
		"target_url": {Kind: fURL, Required: true},
		"secret":     {Kind: fText},
		"events":     {Kind: fText, Default: "*"},
		"is_active":  {Kind: fBool, Default: true},
	},
}

// WebhookDeliveriesCfg /api/v1/webhook-deliveries（list + retrieve）
var WebhookDeliveriesCfg = cfg{
	SelectSQL: `
SELECT wd.id::text AS id, wd.webhook_id::text AS webhook, wd.event_type, wd.payload,
       wd.status, wd.response_code, wd.attempts, wd.created_at`,
	FromClause: "FROM fin_webhook_delivery wd",
	DirectParams: map[string]string{
		"status": "wd.status", "event_type": "wd.event_type", "webhook": "wd.webhook_id::text",
	},
	DefaultOrder: "ORDER BY wd.created_at DESC, wd.id",
}

var WebhookDeliveryWrite = wcf{
	Table: "fin_webhook_delivery", Model: "WebhookDelivery", Alias: "wd", ReadOnly: true,
}

// ReimbursementsCfg /api/v1/reimbursements
// 注：ViewSet 自带 get_queryset，MRO 上覆盖了 OrgScopedQuerysetMixin —— 不做范围收窄。
var ReimbursementsCfg = cfg{
	SelectSQL: `
SELECT rb.id::text AS id, rb.reimb_no, rb.waybill_id::text AS waybill,
       COALESCE(wb.waybill_no,'') AS waybill_no, rb.order_no, rb.category,
       (CASE rb.category WHEN 'freight_advance' THEN '运费垫付' WHEN 'toll' THEN '过路费'
                         WHEN 'fuel' THEN '油费' WHEN 'loading' THEN '装卸费'
                         WHEN 'lodging' THEN '食宿' WHEN 'other' THEN '其他'
                         ELSE rb.category END) AS category_label,
       rb.amount::text AS amount, rb.reason, rb.status,
       (CASE rb.status WHEN 'submitted' THEN '已提交' WHEN 'approved' THEN '已审批'
                       WHEN 'rejected' THEN '已驳回' WHEN 'paid' THEN '已付款'
                       ELSE rb.status END) AS status_label,
       COALESCE(su.username,'') AS submitted_by_name,
       rb.approved_at, rb.paid_at, rb.remark, rb.created_at`,
	FromClause: `FROM fin_reimbursement rb
LEFT JOIN ops_waybill wb ON wb.id = rb.waybill_id
LEFT JOIN accounts_user su ON su.id = rb.submitted_by_id`,
	SearchCols:   []string{"rb.reimb_no", "rb.order_no", "rb.reason"},
	OrderingCols: map[string]string{"created_at": "rb.created_at", "amount": "rb.amount"},
	DirectParams: map[string]string{
		"status": "rb.status", "category": "rb.category", "waybill": "rb.waybill_id::text",
	},
	DefaultOrder: "ORDER BY rb.created_at DESC, rb.id",
	PartialOmit: map[string]string{
		"waybill_no": "rb.waybill_id", "submitted_by_name": "rb.submitted_by_id",
	},
}

var ReimbursementWrite = wcf{
	Table: "fin_reimbursement", Model: "Reimbursement", Alias: "rb",
	NoCreate: true, // create 由 ReimbursementCreate 接管（走 submit_reimbursement 语义）
	Fields: map[string]fld{
		"waybill":  {Kind: fUUID, Ref: "ops_waybill"},
		"order_no": {Kind: fText},
		"category": {Kind: fEnum, Default: "other",
			Choices: []string{"freight_advance", "toll", "fuel", "loading", "lodging", "other"}},
		"amount": {Kind: fDecimal, Default: "0"},
		"reason": {Kind: fText},
		"remark": {Kind: fText},
	},
	ModelDefaults: map[string]any{"status": "submitted"},
}

// waybillStatusLabel 运单状态中文标签（与 waybills 域保持同一份口径）
const waybillStatusLabel = `(CASE w.status
    WHEN 'draft' THEN '草稿' WHEN 'pending_dispatch' THEN '待调度' WHEN 'dispatched' THEN '已派车'
    WHEN 'loaded' THEN '已装车' WHEN 'departed' THEN '已发车' WHEN 'in_transit' THEN '运输中'
    WHEN 'arrived' THEN '已到达' WHEN 'partially_signed' THEN '部分签收' WHEN 'rejected' THEN '已拒收'
    WHEN 'signed' THEN '已签收' WHEN 'delivered' THEN '已送达' WHEN 'settled' THEN '已结算'
    WHEN 'cancelled' THEN '已取消' WHEN 'voided' THEN '已作废' ELSE w.status END)`

// ─────────────────────────── finance：运输合同（商务/价格合同）───────────────────────────

// ContractsCfg /api/v1/finance/contracts —— 一客一价/一商一价的载体
var ContractsCfg = cfg{
	SelectSQL: `
SELECT ct.id::text AS id, ct.contract_no, ct.name, ct.contract_type,
       (CASE ct.contract_type WHEN 'long_term' THEN '长期合同' WHEN 'short_term' THEN '短期合同'
                              WHEN 'temporary' THEN '临时合同' WHEN 'agreement' THEN '仅协议'
                              ELSE ct.contract_type END) AS contract_type_label,
       ct.party_type,
       (CASE ct.party_type WHEN 'customer' THEN '客户' WHEN 'carrier' THEN '承运商'
                           ELSE ct.party_type END) AS party_type_label,
       ct.party_id::text AS party, ct.party_name,
       ct.effective_from::text AS effective_from, ct.effective_to::text AS effective_to,
       ct.signed_at::text AS signed_at, ct.status,
       (CASE ct.status WHEN 'draft' THEN '草稿' WHEN 'active' THEN '生效中' WHEN 'suspended' THEN '已暂停'
                       WHEN 'expired' THEN '已到期' WHEN 'terminated' THEN '已终止'
                       ELSE ct.status END) AS status_label,
       ct.settlement_type, ct.credit_days, ct.billing_day, ct.file_url, ct.remark, ct.created_at,
       (SELECT count(*)::int FROM fin_pricing_rule pr
        WHERE pr.contract_id = ct.id AND pr.is_active) AS rule_count,
       -- 到期预警：30 天内到期的生效合同，续签窗口靠它提醒
       (ct.status='active' AND ct.effective_to IS NOT NULL
        AND ct.effective_to <= (now() AT TIME ZONE 'Asia/Shanghai')::date + 30) AS expiring_soon`,
	FromClause: "FROM fin_contract ct",
	SearchCols: []string{"ct.contract_no", "ct.name", "ct.party_name"},
	OrderingCols: map[string]string{
		"contract_no": "ct.contract_no", "effective_from": "ct.effective_from",
		"effective_to": "ct.effective_to", "created_at": "ct.created_at",
	},
	DirectParams: map[string]string{
		"party_type": "ct.party_type", "party": "ct.party_id::text",
		"contract_type": "ct.contract_type", "status": "ct.status",
	},
	DefaultOrder:  "ORDER BY ct.effective_from DESC, ct.contract_no, ct.id",
	SoftDeleteCol: "ct.is_deleted",
}

var ContractWrite = wcf{
	Table: "fin_contract", Model: "Contract", Verbose: "合同", Alias: "ct", SoftDelete: true,
	Fields: map[string]fld{
		"contract_no": {Kind: fText, Required: true, Unique: true, Label: "contract no"},
		"name":        {Kind: fText},
		"contract_type": {Kind: fEnum, Default: "long_term",
			Choices: []string{"long_term", "short_term", "temporary", "agreement"}},
		"party_type":      {Kind: fEnum, Required: true, Choices: []string{"customer", "carrier"}},
		"party":           {Kind: fUUID, Required: true, Column: "party_id"},
		"party_name":      {Kind: fText},
		"effective_from":  {Kind: fDate, Required: true},
		"effective_to":    {Kind: fDate},
		"signed_at":       {Kind: fDate},
		"status":          {Kind: fEnum, Default: "active", Choices: []string{"draft", "active", "suspended", "expired", "terminated"}},
		"settlement_type": {Kind: fText, Default: "monthly"},
		"credit_days":     {Kind: fInt, Default: int64(30)},
		"billing_day":     {Kind: fInt, Default: int64(1)},
		"file_url":        {Kind: fURL},
		"remark":          {Kind: fText},
	},
	AfterWrite: snapshotPartyName, // 落对手方名称快照，列表与对账单免 JOIN
}

// ProjectsCfg /api/v1/finance/projects —— 对账的主归集维度
var ProjectsCfg = cfg{
	SelectSQL: `
SELECT pj.id::text AS id, pj.project_no, pj.name,
       pj.customer_id::text AS customer, COALESCE(cm.name,'') AS customer_name,
       pj.contract_id::text AS contract, COALESCE(ct.contract_no,'') AS contract_no,
       pj.start_date::text AS start_date, pj.end_date::text AS end_date, pj.status,
       (CASE pj.status WHEN 'active' THEN '进行中' WHEN 'paused' THEN '已暂停'
                       WHEN 'closed' THEN '已结项' ELSE pj.status END) AS status_label,
       pj.manager_id::text AS manager, COALESCE(u.username,'') AS manager_name,
       pj.remark, pj.created_at,
       (SELECT count(*)::int FROM ops_waybill w WHERE w.project_id = pj.id) AS waybill_count`,
	FromClause: `FROM fin_project pj
LEFT JOIN md_customer cm ON cm.id = pj.customer_id
LEFT JOIN fin_contract ct ON ct.id = pj.contract_id
LEFT JOIN accounts_user u ON u.id = pj.manager_id`,
	SearchCols: []string{"pj.project_no", "pj.name", "cm.name"},
	OrderingCols: map[string]string{
		"project_no": "pj.project_no", "start_date": "pj.start_date", "created_at": "pj.created_at",
	},
	DirectParams: map[string]string{
		"customer": "pj.customer_id::text", "status": "pj.status", "contract": "pj.contract_id::text",
	},
	DefaultOrder:  "ORDER BY pj.start_date DESC NULLS LAST, pj.project_no, pj.id",
	SoftDeleteCol: "pj.is_deleted",
	PartialOmit: map[string]string{
		"customer_name": "pj.customer_id", "contract_no": "pj.contract_id", "manager_name": "pj.manager_id",
	},
}

var ProjectWrite = wcf{
	Table: "fin_project", Model: "Project", Verbose: "项目", Alias: "pj", SoftDelete: true,
	Fields: map[string]fld{
		"project_no": {Kind: fText, Required: true, Unique: true, Label: "project no"},
		"name":       {Kind: fText, Required: true},
		"customer":   {Kind: fUUID, Ref: "md_customer", Column: "customer_id"},
		"contract":   {Kind: fUUID, Ref: "fin_contract", Column: "contract_id"},
		"start_date": {Kind: fDate},
		"end_date":   {Kind: fDate},
		"status":     {Kind: fEnum, Default: "active", Choices: []string{"active", "paused", "closed"}},
		"manager":    {Kind: fUUID, Ref: "accounts_user", Column: "manager_id"},
		"remark":     {Kind: fText},
	},
}
