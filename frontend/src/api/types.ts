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
