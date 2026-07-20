export type RiskLevel = "high" | "medium" | "low" | "none";

export interface CurrentUser {
  id: string;
  username: string;
  nickname: string;
  phone: string;
  email: string;
  avatar_url: string | null;
  preferences: UserPreferences;
  is_staff: boolean;
  is_superuser: boolean;
  organization_id: string | null;
  organization_name: string | null;
  date_joined: string | null;
  last_login: string | null;
  roles: string[];
  role_names: string[];
  permissions: string[];
}

export interface UserPreferences {
  default_route?: string;
  table_density?: "standard" | "compact";
  page_size?: number;
  notify_desktop?: boolean;
  notify_email?: boolean;
}

export interface AuthMethods {
  password: boolean;
  wechat: { enabled: boolean; note: string };
}

export interface LoginAttemptRow {
  id: string;
  username: string;
  success: boolean;
  result: string;
  ip: string | null;
  user_agent: string;
  created_at: string;
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
  dispatch_type: string;
  dispatch_type_label: string;
  channel: string;
  platform_name: string;
  platform_order_no: string;
  receivable_amount: number;
  payable_amount: number;
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
  create: "立案", assign: "指派", handle: "处理", close: "闭环",
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
  customer_level?: string;
  assigned_to?: string | null;
  assigned_to_name?: string;
  assigned_by_name?: string;
  dispatchable?: boolean;
  lock_state?: "free" | "mine" | "locked" | "assigned_mine" | "assigned_other";
  created_by_name: string;
  sla_status: string;
  pooled_at: string | null;
  claimed_at?: string | null;
  assigned_at?: string | null;
  dispatched_at?: string | null;
  exception_count?: number;
  exception_level?: string;
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
export const SOURCE_TYPE_LABEL: Record<string, string> = { individual: "个体", enterprise: "企业", government: "政府" };
export const CUSTOMER_LEVEL_LABEL: Record<string, string> = { S: "S · 战略", A: "A · 重点", B: "B · 常规", C: "C · 一般", D: "D · 观察" };

export const SLA_STATUS_LABEL: Record<string, string> = {
  pending: "进行中", at_risk: "临期", on_time: "准时", breached: "超时",
};

export const BODY_TYPE_LABEL: Record<string, string> = {
  stake: "高栏", flatbed: "平板", van: "厢式", reefer: "冷藏",
  hazmat: "危运", fence: "仓栅", wing: "飞翼", tank: "罐式",
};

export interface CarrierScoreRow {
  carrier_id: string;
  carrier: string;
  carrier_grade: string;
  quote: number | null;
  recent_deal_price: number | null;
  suggested_price_band: [number, number] | null;
  deals: number;
  route_hits: number;
  on_time_rate: number;
  exception_rate: number;
  receipt_timely_rate: number;
  score: number;
  risk_level: string;
  label: string;
  risk_notes: string[];
}

export interface CarrierRecommendation {
  carrier_id: string;
  carrier: string;
  suggested_price_band: [number, number] | null;
  risk_level: string;
  label: string;
  reasons: string[];
  risk_notes: string[];
  needs_approval: boolean;
}

export interface DispatchSuggestion {
  order_no: string;
  carrier_recommendations: CarrierScoreRow[];
  recommendation: CarrierRecommendation | null;
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
  ftl: "整车", ltl: "零担", express: "快递", coldchain: "冷链", hazmat: "危化",
};
export const PRIORITY_LABEL: Record<string, string> = {
  normal: "普通", urgent: "加急", vip: "VIP",
};
export const DISPATCH_TYPE_LABEL: Record<string, string> = {
  own_vehicle: "自营单车", fleet: "自营车队", third_party: "外包承运商", platform: "网货平台",
};
// 承运通道大类配色（列表通道标签）
export const CHANNEL_TAG: Record<string, string> = {
  自营: "tag-low", 外包: "tag-info", 网货: "tag-medium",
};

// ── 派车批次（批量派承运商）─────────────────────────────
export const ALLOCATION_LABEL: Record<string, string> = {
  by_weight: "按吨占比", even: "均摊", manual: "逐单指定",
};
export const BATCH_STATUS_LABEL: Record<string, string> = {
  draft: "草稿", dispatched: "已派车", partial: "部分完成", completed: "已完成", cancelled: "已取消",
};
export interface DispatchBatch {
  id: string;
  batch_no: string;
  dispatch_type: string;
  dispatch_type_label: string;
  carrier: string | null;
  carrier_name: string;
  platform_name: string;
  status: string;
  status_label: string;
  allocation: string;
  allocation_label: string;
  total_payable: string;
  order_count: number;
  total_weight_ton: string;
  note: string;
  statement_no: string;
  created_by_name: string;
  customer_summary: string[];
  created_at: string;
}
export interface BatchWaybill {
  id: string;
  waybill_no: string;
  order_no: string;
  customer_name: string;
  origin: string;
  destination: string;
  cargo_weight_ton: string;
  cargo_quantity: number;
  status: string;
  status_label: string;
  payable: number | null;
}
export interface DispatchBatchDetail extends DispatchBatch {
  waybills: BatchWaybill[];
}
export interface BatchDispatchResult {
  batch_no: string;
  batch_id: string;
  carrier: string;
  order_count: number;
  total_payable: number;
  ok: Array<{ order_no: string; waybill_no: string; payable: number; customer: string }>;
  failed: Array<{ order_no: string; reason: string }>;
  skipped: Array<{ order_no: string; reason: string }>;
}
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
  due_date: string | null;
  total_amount: string;
  item_count: number;
  external_total: string;
  diff: string;
  settled_amount: string;
  outstanding: string;
  settled_at: string | null;
  status: string;
  status_label?: string;
  audited_at: string | null;
  created_at: string;
  lines?: StatementLine[];
}
export interface StatementPayment {
  id: string;
  statement: string;
  amount: string;
  method: string;
  method_label: string;
  paid_at: string;
  reference_no: string;
  remark: string;
  created_by_name: string;
  created_at: string;
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
  partial: "部分结算",
  settled: "已结算",
};
export const PAYMENT_METHOD_LABEL: Record<string, string> = {
  bank: "银行转账",
  cash: "现金",
  wechat: "微信",
  alipay: "支付宝",
  offset: "冲抵/对冲",
  acceptance: "承兑汇票",
  other: "其他",
};

// ── 对账总览 / 账龄 ─────────────────────────────────────
export interface DirSummary {
  total: number;
  settled: number;
  outstanding: number;
  count: number;
  draft: number;
  confirmed: number;
  partial: number;
  settled_count: number;
}
export interface TopCounterparty {
  counterparty_id: string;
  counterparty_name: string;
  outstanding: number;
  count: number;
}
export interface StatementOverview {
  receivable: DirSummary;
  payable: DirSummary;
  overdue: { receivable: { amount: number; count: number }; payable: { amount: number; count: number } };
  period: { label: string; count: number; receivable: number; payable: number };
  top_receivable: TopCounterparty[];
  top_payable: TopCounterparty[];
  net_position: number;
}
export interface AgingRow {
  counterparty_id: string;
  counterparty_name: string;
  b0_30: number;
  b31_60: number;
  b61_90: number;
  b90: number;
  total: number;
}
export interface AgingReport {
  direction: "receivable" | "payable";
  rows: AgingRow[];
  totals: { b0_30: number; b31_60: number; b61_90: number; b90: number; total: number };
}

// ── 单据血缘（订单 → 运单 → 对账单）──────────────────────
export interface LineageStatement {
  id: string;
  statement_no: string;
  direction: "receivable" | "payable";
  counterparty_type: string;
  counterparty_name: string;
  status: string;
  status_label: string;
  total_amount: number;
  settled_amount: number;
  outstanding: number;
  period_start: string;
  period_end: string;
}
export interface LineageExpense {
  direction: string;
  expense_item_code: string;
  amount: number;
  payee_type: string;
  payee_ref: string;
  risk_status: string;
}
export interface LineageWaybill {
  id: string;
  waybill_no: string;
  status: string;
  status_label: string;
  carrier_name: string;
  dispatch_type: string;
  batch_no: string;
  receivable: number;
  payable: number;
  expenses: LineageExpense[];
  statements: LineageStatement[];
}
export interface LineageBatch {
  batch_no: string;
  carrier_name: string;
  status: string;
  statement_no: string;
  order_count: number;
  total_payable: number;
}
export interface OrderLineage {
  order: {
    id: string;
    order_no: string;
    status: string;
    status_label: string;
    customer_name: string;
    business_type: string;
    quoted_amount: number;
    created_at: string;
  };
  waybills: LineageWaybill[];
  batches: LineageBatch[];
  ar_statements: LineageStatement[];
  ap_statements: LineageStatement[];
  summary: { waybill_count: number; receivable_total: number; payable_total: number; gross: number; statement_count: number };
}

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
export interface Customer {
  id: string; code: string; name: string;
  category?: string; level?: string; level_label?: string;
  contact_name?: string; contact_phone?: string; wechat_group?: string; settlement_type?: string;
  credit_limit?: number | string; credit_days?: number; billing_day?: number; is_active?: boolean;
}
export interface CarrierExpiryAlert { field: string; label: string; date: string; expired: boolean }
export interface CarrierPerformance {
  deals: number; route_hits: number; on_time_rate: number; exception_rate: number;
  receipt_timely_rate: number; recent_deal_price: number | null; has_history: boolean;
  frequent_routes?: Array<{ origin: string; destination: string; deals: number }>;
}
export interface Carrier {
  id: string; code: string; name: string;
  carrier_type?: string; carrier_type_label?: string;
  contact_name?: string; contact_phone?: string; city?: string; service_area?: string;
  settlement_type?: string; is_active?: boolean;
  grade?: string; grade_label?: string; blacklisted?: boolean; blacklist_reason?: string;
  business_license_no?: string; transport_license_no?: string; qualification_expiry?: string | null;
  contract_expiry?: string | null; insurance_expiry?: string | null; tax_no?: string;
  credit_limit?: number | string; credit_days?: number; billing_day?: number;
  dispatch_blocked?: string;
  expiry_alerts?: CarrierExpiryAlert[];
  performance?: CarrierPerformance | null;
}
export interface CustomerAddrRow { address: string; contact_name: string; contact_phone: string; count: number }
export interface CustomerOrderBrief {
  order_no: string; status: string; status_label: string; route: string; cargo: string;
  quoted_amount: number; created_at: string | null;
}
export interface CustomerContext {
  customer_id: string; name: string;
  profile: { settlement_type: string; credit_limit: number; credit_days: number; billing_day: number };
  credit: { limit: number; outstanding: number; available: number | null; used_pct: number | null; over_limit: boolean };
  common_routes: string[];
  common_pickups: CustomerAddrRow[];
  common_deliveries: CustomerAddrRow[];
  recent_orders: CustomerOrderBrief[];
  open_orders: CustomerOrderBrief[];
  counts: { total: number; open: number; exceptions: number; receipt_pending: number };
}
export interface LookupAnswer {
  kind: "waybill" | "order" | "vehicle" | "driver" | "customer" | "none";
  title?: string;
  waybill_no?: string;
  order_no?: string;
  customer_id?: string;
  driver_phone?: string;
  fields?: Array<{ label: string; value: string }>;
  actions?: string[];
}
export interface LookupResult {
  kind: "waybill" | "order" | "customer" | "carrier" | "statement";
  title: string;
  subtitle: string;
  path: string;
}
export interface LookupResponse {
  answer: LookupAnswer;
  results: LookupResult[];
}
export interface FinanceCardData {
  waybill_no: string; customer_name: string; carrier_name: string;
  receivable: number; payable: number; other_fee: number; gross_margin: number;
  margin_pct: number | null; exception_deduction: number;
  receipt_ok: boolean; reconcilable: boolean; blockers: string[];
}
export interface ReplyCardData {
  waybill_no: string; route: string; status: string; status_label: string;
  driver_name: string; driver_phone: string; plate_no: string;
  latest_node: { node: string; at: string | null } | null;
  eta: string | null; receipt_status: string; exception: string | null; copy_text: string;
}
export interface CarrierLanePrice {
  id: string; carrier: string; carrier_name?: string;
  origin_city: string; dest_city: string; vehicle_type?: string; vehicle_length_m?: number | string;
  standard_price: number | string; min_price?: number | string; max_price?: number | string;
  last_deal_price?: number | string;
  effective_from?: string | null; effective_to?: string | null;
  is_preferred?: boolean; is_recommended?: boolean; note?: string; is_active?: boolean;
}

// ── 指标与经营看板 ────────────────────────────────────────────
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
export interface Vehicle {
  id: string; plate_no: string; vehicle_type: string; vehicle_class?: string; vehicle_class_label?: string;
  body_type?: string; body_type_label?: string; vehicle_length_m?: number | string;
  dispatch_source?: string; dispatch_source_label?: string;
  carrier?: string; carrier_name?: string; load_capacity_ton?: number | string; volume_capacity_cbm?: number | string;
  road_transport_cert_no?: string; inspection_expiry?: string | null; insurance_expiry?: string | null;
  maintenance_due_date?: string | null; is_active?: boolean;
}
export interface Driver {
  id: string; name: string; phone: string; employment_type?: string; employment_label?: string;
  license_type?: string; license_no?: string; license_expiry?: string | null;
  qualification_cert_no?: string; qualification_expiry?: string | null;
  carrier?: string; carrier_name?: string; is_active?: boolean;
}

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

// ── 组织与权限 ────────────────────────────────────────────
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
  role_names?: string[];
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

export interface Role {
  id: string;
  code: string;
  name: string;
  data_scope: string;
  data_scope_label: string;
  is_active: boolean;
  permission_codes: string[];
  permission_count: number;
}

export interface RoleAssignment {
  id: string;
  role: string;
  role_code: string;
  role_name: string;
  username: string;
  organization_name: string;
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
