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
  status: string;
  contact_name: string;
  contact_phone: string;
  origin: string;
  destination: string;
  cargo_desc: string;
  cargo_quantity: number;
  cargo_weight_ton: string;
  cargo_volume_cbm: string;
  raw_text: string;
  parse_meta: Record<string, unknown>;
  created_at: string;
}
export interface ParsedOrder {
  fields: Record<string, string | number>;
  meta: { source?: string };
}
export const ORDER_CHANNEL_LABEL: Record<OrderChannel, string> = {
  cs: "客服代下",
  self: "客户自助",
  miniprogram: "小程序",
  wechat_group: "微信群",
  api: "开放API",
};
export const ORDER_STATUS_LABEL: Record<string, string> = {
  pending_confirm: "待确认",
  confirmed: "已确认",
  converted: "已转运单",
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
