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

export interface Waybill {
  id: string;
  waybill_no: string;
  customer_name: string;
  carrier_name: string;
  vehicle_plate: string;
  driver_name: string;
  route_name: string;
  origin: string;
  destination: string;
  status: string;
  dispatch_status: string;
  risk_level: RiskLevel;
  receipt_status: string;
  eta_drift_minutes: number;
  planned_arrival: string | null;
  estimated_arrival: string | null;
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

export interface WaybillDetail extends Waybill {
  timeline: WaybillEvent[];
  agent_suggestions: AgentSuggestion[];
  next_statuses: string[];
}

export interface ExpenseLine {
  id: string;
  direction: string;
  expense_item_code: string;
  amount: number;
  risk_status: string;
}

export interface CostSummary {
  waybill_no: string;
  receivables: ExpenseLine[];
  payables: ExpenseLine[];
  external_expenses: ExpenseLine[];
  gross_profit: number;
  gross_margin: number;
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

export interface DispatchSuggestion {
  order_no: string;
  vehicle_candidates: Array<{ vehicle_id?: string; plate_no: string; utilization: number; compliance?: string[]; compliance_ok?: boolean }>;
  carrier_quotes: Array<{ carrier_id?: string; carrier: string; quote: number }>;
  external_signals: Array<{ type: string; level: string; note: string }>;
  suggested_dispatch_type: string;
  best_vehicle: { vehicle_id?: string; plate_no: string; compliance?: string[]; compliance_ok?: boolean } | null;
  best_carrier: { carrier_id?: string; carrier: string; quote: number } | null;
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
  created_at: string;
  lines?: StatementLine[];
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
  expense_item_code: string;
  customer: string | null;
  customer_name: string;
  carrier: string | null;
  carrier_name: string;
  route_name: string;
  vehicle_type: string;
  base_price: string;
  price_per_ton: string;
  min_price: string;
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
export interface Vehicle { id: string; plate_no: string; vehicle_type: string; }
export interface Driver { id: string; name: string; phone: string; }

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
