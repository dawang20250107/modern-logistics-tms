export type RiskLevel = "high" | "medium" | "low" | "none";

export interface CurrentUser {
  id: string;
  username: string;
  nickname: string;
  phone: string;
  is_staff: boolean;
  is_superuser: boolean;
  organization_id: string | null;
  roles: string[];
}

export interface Contract {
  id: string;
  contract_no: string;
  driver_name: string;
  content: string;
  sent_at: string | null;
  driver_reply: string;
  confirm_status: string;
  status_label: string;
  confirmed_at: string | null;
  pdf_url: string;
  created_at: string;
}

export interface WorkflowStage { key: string; name: string; done: boolean; detail: string; at: string | null }
export interface OrderWorkflow { order_no: string; current: string; stages: WorkflowStage[] }

export interface Reimbursement {
  id: string;
  reimb_no: string;
  waybill_no: string;
  order_no: string;
  category: string;
  category_label: string;
  amount: number;
  reason: string;
  status: string;
  status_label: string;
  submitted_by_name: string;
  created_at: string;
}

export const REIMB_CATEGORY_LABEL: Record<string, string> = {
  freight_advance: "运费垫付", toll: "过路费", fuel: "油费",
  loading: "装卸费", lodging: "食宿", other: "其他",
};

export interface ReminderTemplate {
  id: string;
  name: string;
  category: string;
  content: string;
  is_active: boolean;
}

export interface DriverReminder {
  id: string;
  waybill_no: string;
  driver_name: string;
  title: string;
  content: string;
  ack_required: boolean;
  status: string;
  sent_at: string;
  acknowledged_at: string | null;
}

export interface WaybillDriverRow {
  id: string;
  name: string;
  phone: string;
  wechat: string;
  app_registered: boolean;
  role: string;
  role_label: string;
  employment: string;
  note: string;
}

export interface Waybill {
  id: string;
  waybill_no: string;
  customer_name: string;
  carrier_name: string;
  vehicle_plate: string;
  trailer_plate: string;
  driver_name: string;
  driver_phone: string;
  driver_employment: string;
  drivers: WaybillDriverRow[];
  route_name: string;
  ai_conversation_id: string;
  origin: string;
  destination: string;
  status: string;
  dispatch_status: string;
  risk_level: RiskLevel;
  receipt_status: string;
  eta_drift_minutes: number;
  planned_arrival: string | null;
  estimated_arrival: string | null;
  loaded_at: string | null;
  departed_at: string | null;
  arrived_at: string | null;
  signed_at: string | null;
  freight_term: string;
  freight_term_label: string;
  freight_payer: string;
  freight_payer_label: string;
  cod_amount: string;
  cod_status: string;
  cod_status_label: string;
  cod_collected_at: string | null;
  cod_remitted_at: string | null;
  cargo: { quantity: number; weight_ton: number; volume_cbm: number };
  created_at: string;
}

export interface Paginated<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
  pages: number;
}

export interface QueryWaybillResult {
  answer: string;
  query: string;
  waybills: Waybill[];
}

export interface WaybillEvent {
  id: string;
  event_type: string;
  event_time: string;
  resource: string;
  source: string;
  payload: Record<string, unknown>;
}

export interface AgentSuggestion {
  id: string;
  waybill_no?: string;
  suggestion_type: string;
  title: string;
  body: string;
  status: string;
  evidence: Record<string, unknown>;
  created_at: string;
}

export interface WaybillStopRow {
  id: string;
  seq: number;
  stop_type: string;
  stop_type_label: string;
  city: string;
  address: string;
  contact_name: string;
  contact_phone: string;
  planned_eta: string | null;
  actual_arrival_at: string | null;
  actual_depart_at: string | null;
  arrival_source: string;
  status: string;
  status_label: string;
  note: string;
}

export interface WaybillDetail extends Waybill {
  timeline: WaybillEvent[];
  agent_suggestions: AgentSuggestion[];
  next_statuses: string[];
  stops: WaybillStopRow[];
}

export interface ExpenseLine {
  id: string;
  direction: string;
  expense_item_code: string;
  item_label: string;
  amount: number;
  risk_status: string;
  payee_type: string;
  payee_label: string;
  payee_ref: string;
  source_system: string;
  remark: string;
}

export interface PayeeGroup {
  payee_type: string;
  payee_label: string;
  amount: number;
}

export interface CostSummary {
  waybill_no: string;
  receivables: ExpenseLine[];
  payables: ExpenseLine[];
  external_expenses: ExpenseLine[];
  payables_by_payee: PayeeGroup[];
  receivable_total: number;
  payable_total: number;
  gross_profit: number;
  gross_margin: number;
}

export interface CostCatalog {
  cost_items: Record<string, string>;
  income_items: Record<string, string>;
  payees: Record<string, string>;
}

export interface ExceptionRecord {
  id: string;
  waybill: string | null;
  waybill_no: string;
  exception_type: string;
  level: string;
  source: string;
  description: string;
  status: string;
  assignee: string | null;
  assignee_name: string;
  responsibility_party: string;
  amount: string | number;
  resolution: string;
  created_at: string;
}

export interface ExceptionEvent {
  id: string;
  event_type: string;
  from_status: string;
  to_status: string;
  actor_name: string;
  note: string;
  payload: Record<string, unknown>;
  event_time: string;
}
export const EXC_EVENT_LABEL: Record<string, string> = {
  create: "立案", assign: "指派", handle: "处理", ai_resolve: "AI 诊断", close: "闭环",
};

export interface Receipt {
  id: string;
  waybill: string;
  waybill_no: string;
  receipt_type: string;
  status: string;
  file_display: string;
  file_url: string;
  ocr_status: string;
  ocr_result: Record<string, unknown>;
  signatory: string;
  created_at: string;
}

export const STATUS_LABEL: Record<string, string> = {
  draft: "草稿",
  pending_dispatch: "待调度",
  dispatched: "已派车",
  loaded: "已装车",
  departed: "已发车",
  in_transit: "运输中",
  arrived: "已到达",
  signed: "已签收",
  delivered: "已送达",
  settled: "已结算",
  cancelled: "已取消",
  voided: "已作废",
};

// ── 车联网监控 ──────────────────────────────────────────
export interface VehicleState {
  id: string;
  vehicle: string;
  vehicle_plate: string;
  vehicle_type: string;
  waybill: string | null;
  waybill_no: string;
  lng: string;
  lat: string;
  speed_kmh: string;
  heading: number;
  mileage_km: string;
  temperature_c: string | null;
  fuel_pct: string | null;
  online: boolean;
  reported_at: string | null;
}

export type AlertType =
  | "overspeed" | "fatigue" | "deviation" | "abnormal_stop"
  | "geofence" | "temperature" | "fuel" | "offline";
export type AlertLevel = "info" | "medium" | "high";
export type AlertStatus = "open" | "acknowledged" | "closed";

export interface Alert {
  id: string;
  alert_type: AlertType;
  level: AlertLevel;
  status: AlertStatus;
  vehicle: string | null;
  vehicle_plate: string;
  device_no: string;
  waybill: string | null;
  waybill_no: string;
  message: string;
  value: string | null;
  threshold: string | null;
  detail: Record<string, unknown>;
  triggered_at: string;
  handled_at: string | null;
  created_at: string;
}

export const ALERT_TYPE_LABEL: Record<AlertType, string> = {
  overspeed: "超速",
  fatigue: "疲劳驾驶",
  deviation: "偏航",
  abnormal_stop: "异常停车",
  geofence: "围栏进出",
  temperature: "温度异常",
  fuel: "油量异常",
  offline: "设备离线",
};

// ── 多渠道订单 ──────────────────────────────────────────
export type OrderChannel = "cs" | "self" | "miniprogram" | "wechat_group" | "api";
export const FREIGHT_TERM_LABEL: Record<string, string> = {
  prepaid: "现付", collect: "到付", receipt: "回单付", monthly: "月结",
};
export const FREIGHT_PAYER_LABEL: Record<string, string> = {
  shipper: "发货方", consignee: "收货方", third_party: "第三方",
};
export const COD_STATUS_LABEL: Record<string, string> = {
  none: "无代收", pending: "待代收", collected: "已代收", remitted: "已回款",
};
export interface DriverCollection {
  waybill_no: string;
  freight_term: string;
  collect_freight: number;
  cod_amount: number;
  cod_status: string;
  total_to_collect: number;
}

export interface Order {
  id: string;
  order_no: string;
  customer: string | null;
  customer_name: string;
  channel: OrderChannel;
  source: string;
  source_type: string;
  business_type: string;
  priority: string;
  settlement_type: string;
  freight_term: string;
  freight_term_label: string;
  freight_payer: string;
  freight_payer_label: string;
  cod_amount: string;
  cod_status: string;
  status: string;
  contact_name: string;
  contact_phone: string;
  origin: string;
  destination: string;
  cargo_desc: string;
  cargo_quantity: number;
  cargo_weight_ton: string;
  cargo_volume_cbm: string;
  cargo_value: string;
  is_hazardous: boolean;
  temperature_range: string;
  claimed_by_name: string;
  created_by_name: string;
  sla_status: string;
  pooled_at: string | null;
  delivered_at: string | null;
  raw_text: string;
  ai_conversation_id: string;
  parse_meta: Record<string, unknown>;
  waybill_nos: string[];
  cargo_items: OrderCargoItem[];
  stops: OrderStop[];
  attachments: OrderAttachment[];
  approval_status: "none" | "pending" | "approved" | "rejected";
  approval_remark: string;
  approved_at: string | null;
  quoted_amount: string;
  package_type: string;
  expected_pickup_at: string | null;
  expected_delivery_at: string | null;
  pickup_address: string;
  delivery_address: string;
  remark: string;
  created_at: string;
}

export interface OrderCargoItem {
  id?: string;
  seq?: number;
  name: string;
  quantity: number | string;
  weight_ton: number | string;
  volume_cbm: number | string;
  package_type: string;
  temperature_range: string;
  remark: string;
}
export interface OrderStop {
  id?: string;
  seq?: number;
  stop_type: "pickup" | "delivery";
  city: string;
  address: string;
  contact_name: string;
  contact_phone: string;
  expected_start: string | null;
  expected_end: string | null;
  cargo_note: string;
}
export interface OrderTemplate {
  id: string;
  name: string;
  payload: Record<string, unknown>;
  created_by_name: string;
  created_at: string;
}
export interface OrderAttachment {
  id: string;
  kind: string;
  name: string;
  file_display: string;
  file_url: string;
  uploaded_by_name: string;
  created_at: string;
}
export const ATTACHMENT_KIND_LABEL: Record<string, string> = {
  contract: "合同", authorization: "委托书", photo: "货物照片", other: "其他",
};
export const SETTLEMENT_LABEL: Record<string, string> = { monthly: "月结", cash: "现结", prepaid: "预付" };
export const SOURCE_TYPE_LABEL: Record<string, string> = { individual: "个人", enterprise: "企业", government: "政府" };

export const SLA_STATUS_LABEL: Record<string, string> = {
  pending: "进行中", at_risk: "临期", on_time: "准时", breached: "超时",
};

export const BODY_TYPE_LABEL: Record<string, string> = {
  stake: "高栏", flatbed: "平板", van: "厢式", reefer: "冷藏",
  hazmat: "危运", fence: "仓栅", wing: "飞翼", tank: "罐式",
};

export interface DispatchSuggestion {
  order_no: string;
  vehicle_candidates: Array<{ vehicle_id?: string; plate_no: string; utilization: number; compliance?: string[]; compliance_ok?: boolean; body_type?: string; vehicle_length_m?: number }>;
  carrier_quotes: Array<{ carrier_id?: string; carrier: string; quote: number }>;
  ymm_quote?: YmmQuote;
  external_signals: Array<{ type: string; level: string; note: string }>;
  suggested_dispatch_type: string;
  best_vehicle: { vehicle_id?: string; plate_no: string; compliance?: string[]; compliance_ok?: boolean } | null;
  best_carrier: { carrier_id?: string; carrier: string; quote: number } | null;
}

export interface YmmQuote {
  source: string;
  provider: string;
  route: string;
  low: number | null;
  avg: number | null;
  high: number | null;
  currency: string;
  note: string;
}

export const BUSINESS_TYPE_LABEL: Record<string, string> = {
  ftl: "整车", ltl: "零担", express: "快递", coldchain: "冷链",
};
export const PRIORITY_LABEL: Record<string, string> = {
  normal: "普通", urgent: "加急", vip: "VIP",
};
export const DISPATCH_TYPE_LABEL: Record<string, string> = {
  own_vehicle: "自有单车", fleet: "自有车队", third_party: "三方承运商",
};
export interface DuplicateOrder {
  id: string;
  order_no: string;
  status: string;
  origin: string;
  destination: string;
  contact_phone: string;
  created_at: string;
}
export interface ParsedOrder {
  fields: Record<string, string | number>;
  meta: { source?: string };
  missing?: Array<{ field: string; label: string }>;
  duplicates?: DuplicateOrder[];
}
export const ORDER_CHANNEL_LABEL: Record<OrderChannel, string> = {
  cs: "客服代下",
  self: "客户自助",
  miniprogram: "小程序",
  wechat_group: "微信群",
  api: "开放API",
};
export const ORDER_STATUS_LABEL: Record<string, string> = {
  draft: "草稿",
  pending_confirm: "待确认",
  confirmed: "已确认",
  pooled: "订单池",
  dispatching: "调度中",
  converted: "已派单",
  completed: "已完成",
  cancelled: "已取消",
};

// ── 对账单 ──────────────────────────────────────────────
export interface StatementLine {
  id: string;
  waybill_no: string;
  expense_item_code: string;
  amount: string;
  occurred_at: string | null;
  is_anomaly: boolean;
  baseline_avg: string | null;
  deviation_pct: string | null;
}
export interface Statement {
  id: string;
  statement_no: string;
  direction: "receivable" | "payable";
  counterparty_type: "customer" | "carrier";
  counterparty_id: string;
  counterparty_name: string;
  period_start: string;
  period_end: string;
  total_amount: string;
  item_count: number;
  external_total: string;
  diff: string;
  status: string;
  audited_at: string | null;
  created_at: string;
  lines?: StatementLine[];
}
export interface StatementAuditResult {
  total_lines: number;
  anomaly_count: number;
  audited_at: string;
  statement: Statement;
}
export const STATEMENT_STATUS_LABEL: Record<string, string> = {
  draft: "草稿",
  confirmed: "已确认",
  settled: "已结算",
};

// ── 车队合规预警 ────────────────────────────────────────
export type CredSeverity = "expired" | "critical" | "warning";
export interface CredentialRow {
  subject: string;
  plate_no?: string;
  name?: string;
  credential: string;
  expiry: string;
  days_left: number;
  severity: CredSeverity;
}
export interface ExpiringCredentials {
  days: number;
  summary: { total: number; expired: number; critical: number; warning: number };
  vehicles: CredentialRow[];
  drivers: CredentialRow[];
}
export const CRED_SEVERITY_LABEL: Record<CredSeverity, string> = {
  expired: "已过期", critical: "紧急", warning: "临期",
};

// ── 合同价 / 计价规则 ───────────────────────────────────
export interface PricingRule {
  id: string;
  name: string;
  price_type: "income" | "cost";
  charge_method: string;
  charge_method_label: string;
  expense_item_code: string;
  customer?: string;
  customer_name?: string;
  carrier?: string;
  carrier_name?: string;
  route_name: string;
  vehicle_type: string;
  base_price: string;
  min_price: string;
  unit_price: string;
  min_charge_qty: string;
  tier_prices: Array<{ min_ton: number; max_ton: number; price: number }>;
  volumetric_factor: string;
  fuel_surcharge_pct: string;
  priority: number;
  is_active: boolean;
  created_at: string;
}
export const PRICE_TYPE_LABEL: Record<string, string> = { income: "收入价（报给客户）", cost: "支出价（付给承运商）" };

// ── 主数据(精简) ───────────────────────────────────────
export interface Customer { id: string; code: string; name: string; }
export interface Carrier { id: string; code: string; name: string; }

// ── 指标中台 ────────────────────────────────────────────
export interface MetricCard {
  code: string;
  name: string;
  unit: string;
  domain: string;
  value: number;
  breakdown?: Array<{ key: string; value: number }>;
}
export const METRIC_DOMAIN_LABEL: Record<string, string> = {
  ops: "运单 / 履约",
  fleet: "运力 / 车辆",
  order: "订单 / 渠道",
  finance: "财务 / 对账",
};
export interface Vehicle { id: string; plate_no: string; vehicle_type: string; vehicle_class?: string; vehicle_class_label?: string; }
export interface Driver { id: string; name: string; phone: string; employment_type?: string; employment_label?: string; }

export interface DriverCredential {
  id: string;
  driver: string;
  driver_name: string;
  cred_type: string;
  cred_type_label: string;
  side: string;
  side_label: string;
  file_display: string;
  ocr_status: string;
  holder_name: string;
  cert_no: string;
  expiry_date: string | null;
  self_uploaded: boolean;
  created_at: string;
}

export interface DriverLookup {
  matched: boolean;
  driver: Driver | null;
  credentials: DriverCredential[];
}

export const CRED_TYPE_LABEL: Record<string, string> = {
  vehicle_license: "车头行驶证", trailer_license: "车挂行驶证",
  driving_license: "驾驶证", transport_cert: "道路运输证", id_card: "身份证",
};

// ── 通知 / 订单事件 ─────────────────────────────────────
export interface Notification {
  id: string;
  category: string;
  title: string;
  body: string;
  level: "info" | "warning" | "critical";
  link_type: string;
  link_id: string;
  is_read: boolean;
  created_at: string;
}
export interface OrderEvent {
  id: string;
  event_type: string;
  from_status: string;
  to_status: string;
  actor_name: string;
  source: string;
  payload: Record<string, unknown>;
  event_time: string;
}
export const ORDER_EVENT_LABEL: Record<string, string> = {
  created: "建单", confirmed: "确认", pooled: "进池", claimed: "调度认领",
  dispatched: "派单", completed: "完成", cancelled: "取消", updated: "编辑",
  approval_required: "提交审批", approved: "审批通过", rejected: "审批驳回",
  split: "拆单", merged: "合单",
  contract_generated: "生成合同", contract_sent: "发送合同",
  contract_confirmed: "合同确认", contract_rejected: "合同拒签",
};
export const APPROVAL_STATUS_LABEL: Record<string, string> = {
  none: "无需审批", pending: "待审批", approved: "已通过", rejected: "已驳回",
};

// ── 数据资产目录 ───────────────────────────────────────
export interface DataAsset {
  app: string;
  domain: string;
  model: string;
  table: string;
  verbose_name: string;
  field_count: number;
  row_count?: number | null;
  fields: Array<{ name: string; type: string; help: string }>;
}

// ── 组织中台 ────────────────────────────────────────────
export interface OrgTreeNode {
  id: string;
  code: string;
  name: string;
  short_name: string;
  type: string;
  type_label: string;
  org_property: string;
  org_property_label: string;
  manager_name: string;
  is_active: boolean;
  parent_id: string | null;
  direct_headcount: number;
  total_headcount: number;
  children: OrgTreeNode[];
}

export interface Employee {
  id: string;
  employee_no: string;
  name: string;
  phone: string;
  email: string;
  organization: string | null;
  organization_name: string;
  department: string | null;
  department_name: string;
  supervisor: string | null;
  supervisor_name: string;
  groups: string[];
  group_names: string[] | null;
  position: string;
  status: "active" | "disabled" | "left";
  status_label: string;
  hire_date: string | null;
  user: string | null;
  username: string;
  account_active: boolean;
}

export interface ServiceArea {
  id: string;
  organization: string | null;
  organization_name: string;
  area_type: string;
  area_type_label: string;
  province: string;
  city: string;
  district: string;
  region_code: string;
  region_name: string;
  priority: number;
  note: string;
  is_active: boolean;
}

export interface OrgOverview {
  organizations: { total: number; by_property: Record<string, number>; by_type: Record<string, number> };
  employees: { total: number; active: number; by_status: Record<string, number>; active_without_account: number };
  departments: number;
  service_areas: { total: number; by_type: Record<string, number> };
}

export interface CoverageResolved {
  organization_id: string;
  organization_name: string;
  org_short: string;
  manager_name: string;
  area_type: string;
  area_type_label: string;
  region_name: string;
  priority: number;
  matched_on: string;
}
export interface CoverageResult {
  destination: string;
  resolved: CoverageResolved[];
  excluded: Array<{ organization_id: string; organization_name: string; reason: string }>;
}

export interface RbacMatrix {
  modules: Array<{ module: string; permissions: Array<{ id: string; code: string; name: string }> }>;
  roles: Array<{ id: string; code: string; name: string; data_scope: string; is_active: boolean; permission_codes: string[] }>;
  permission_total: number;
}

export interface OrgOption {
  id: string;
  name: string;
  code: string;
  type: string;
  type_label: string;
}

export interface AccountHandover {
  id: string;
  from_employee: string;
  from_name: string;
  to_employee: string;
  to_name: string;
  operator_name: string;
  reason: string;
  moved_reports: number;
  moved_departments: number;
  disabled_account: boolean;
  created_at: string;
}

export const EMP_STATUS_LABEL: Record<string, string> = {
  active: "在职", disabled: "停用", left: "离职",
};
export const AREA_TYPE_LABEL: Record<string, string> = {
  deliver: "派送区域", transfer: "中转区域", special: "特殊区域",
  no_deliver: "不派送区域", no_transfer: "不中转区域",
};
export const ORG_PROPERTY_LABEL: Record<string, string> = {
  self: "自营", franchise: "加盟", outsource: "外包", partner: "合作", jv: "合资",
};

// ── 审计日志 ────────────────────────────────────────────
export interface AuditLog {
  id: string;
  actor_name: string;
  action: string;
  resource_type: string;
  resource_id: string;
  request_id: string;
  method: string;
  path: string;
  status_code: number | null;
  ip: string | null;
  payload: Record<string, unknown>;
  created_at: string;
}
