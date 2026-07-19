import { ORDER_STATUS_LABEL, SLA_STATUS_LABEL, STATUS_LABEL } from "../api/types";

// 状态色规范：语义 tone → 统一 tag 类。低饱和、强对比、状态清晰。
export type Tone = "neutral" | "info" | "progress" | "success" | "warning" | "danger";
const TONE_CLASS: Record<Tone, string> = {
  neutral: "tag-none",
  info: "tag-info",
  progress: "tag-info",
  success: "tag-low",
  warning: "tag-medium",
  danger: "tag-high",
};

type Kind = "waybill" | "order" | "receipt" | "sla" | "channel";

const WAYBILL_TONE: Record<string, Tone> = {
  pending_dispatch: "neutral", dispatched: "info", loaded: "progress", departed: "progress",
  in_transit: "progress", arrived: "info", signed: "success", delivered: "success",
  settled: "success", voided: "danger",
};
const ORDER_TONE: Record<string, Tone> = {
  draft: "neutral", pending_confirm: "warning", confirmed: "info", pooled: "info",
  dispatching: "progress", converted: "success", completed: "success", cancelled: "neutral",
};
const RECEIPT_TONE: Record<string, Tone> = { returned: "success", audited: "success", pending: "neutral", not_due: "neutral" };
const RECEIPT_LABEL: Record<string, string> = { returned: "已回收", audited: "已核销", pending: "待追回", not_due: "未到期" };
const SLA_TONE: Record<string, Tone> = { on_time: "success", at_risk: "warning", breached: "danger", pending: "neutral" };
const CHANNEL_TONE: Record<string, Tone> = { 自营: "success", 外包: "info", 网货: "warning" };

function resolve(kind: Kind, value: string): { label: string; tone: Tone } {
  switch (kind) {
    case "waybill": return { label: STATUS_LABEL[value] ?? value, tone: WAYBILL_TONE[value] ?? "info" };
    case "order": return { label: ORDER_STATUS_LABEL[value] ?? value, tone: ORDER_TONE[value] ?? "info" };
    case "receipt": return { label: RECEIPT_LABEL[value] ?? "待追回", tone: RECEIPT_TONE[value] ?? "neutral" };
    case "sla": return { label: SLA_STATUS_LABEL[value] ?? value, tone: SLA_TONE[value] ?? "neutral" };
    case "channel": return { label: value, tone: CHANNEL_TONE[value] ?? "neutral" };
    default: return { label: value, tone: "neutral" };
  }
}

export function StatusTag({ kind, value, title, suffix }: { kind: Kind; value: string; title?: string; suffix?: string }) {
  const { label, tone } = resolve(kind, value);
  return <span className={`tag ${TONE_CLASS[tone]}`} title={title}>{label}{suffix ?? ""}</span>;
}
