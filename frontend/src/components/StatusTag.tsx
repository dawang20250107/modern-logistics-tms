import { BATCH_STATUS_LABEL, ORDER_STATUS_LABEL, SLA_STATUS_LABEL, STATUS_LABEL } from "../api/types";

// 状态色规范 = 决策语言：状态 → 色彩 tone + 文案 label + 优先级 priority + 是否需要动作 needsAction。
// 视觉不只是"好看"，而是让高频岗位一眼判断"这条要不要我处理、有多急"。
export type Tone = "neutral" | "info" | "progress" | "success" | "warning" | "danger";
const TONE_CLASS: Record<Tone, string> = {
  neutral: "tag-none",
  info: "tag-info",
  progress: "tag-info",
  success: "tag-low",
  warning: "tag-medium",
  danger: "tag-high",
};

export type StatusKind = "waybill" | "order" | "receipt" | "sla" | "channel" | "batch";
export interface StatusMeta { label: string; tone: Tone; priority: number; needsAction: boolean }

// priority：数值越大越需优先关注（可用于列表排序 / 强调）；needsAction：该状态是否是一项待办任务。
const WAYBILL: Record<string, StatusMeta> = {
  pending_dispatch: { label: "待调度", tone: "neutral", priority: 5, needsAction: true },
  dispatched: { label: "已派车", tone: "info", priority: 3, needsAction: false },
  loaded: { label: "已装车", tone: "progress", priority: 3, needsAction: false },
  departed: { label: "已发车", tone: "progress", priority: 3, needsAction: false },
  in_transit: { label: "运输中", tone: "progress", priority: 3, needsAction: false },
  arrived: { label: "已到达", tone: "info", priority: 4, needsAction: true },
  signed: { label: "已签收", tone: "success", priority: 2, needsAction: false },
  delivered: { label: "已送达", tone: "success", priority: 2, needsAction: false },
  settled: { label: "已结算", tone: "success", priority: 0, needsAction: false },
  voided: { label: "已作废", tone: "danger", priority: 1, needsAction: false },
};
const ORDER: Record<string, StatusMeta> = {
  draft: { label: "草稿", tone: "neutral", priority: 2, needsAction: false },
  pending_confirm: { label: "待确认", tone: "warning", priority: 5, needsAction: true },
  confirmed: { label: "已确认", tone: "info", priority: 3, needsAction: false },
  pooled: { label: "已进池", tone: "info", priority: 4, needsAction: true },
  dispatching: { label: "调度中", tone: "progress", priority: 3, needsAction: false },
  converted: { label: "已转运单", tone: "success", priority: 1, needsAction: false },
  completed: { label: "已完成", tone: "success", priority: 0, needsAction: false },
  cancelled: { label: "已取消", tone: "neutral", priority: 0, needsAction: false },
};
// 回单：待追回不是中性信息，而是需要动作的任务
const RECEIPT: Record<string, StatusMeta> = {
  not_due: { label: "未到期", tone: "neutral", priority: 0, needsAction: false },
  pending: { label: "待追回", tone: "warning", priority: 4, needsAction: true },
  returned: { label: "已回收", tone: "success", priority: 1, needsAction: false },
  audited: { label: "已核销", tone: "success", priority: 0, needsAction: false },
  exception: { label: "回单异常", tone: "danger", priority: 5, needsAction: true },
};
const SLA: Record<string, StatusMeta> = {
  pending: { label: "进行中", tone: "neutral", priority: 1, needsAction: false },
  at_risk: { label: "临期", tone: "warning", priority: 4, needsAction: true },
  breached: { label: "超时", tone: "danger", priority: 5, needsAction: true },
  on_time: { label: "准时", tone: "success", priority: 0, needsAction: false },
};
const CHANNEL: Record<string, StatusMeta> = {
  自营: { label: "自营", tone: "success", priority: 1, needsAction: false },
  外包: { label: "外包", tone: "info", priority: 1, needsAction: false },
  网货: { label: "网货", tone: "warning", priority: 2, needsAction: false },
};
// 派车批次状态
const BATCH: Record<string, StatusMeta> = {
  draft: { label: "草稿", tone: "neutral", priority: 2, needsAction: false },
  dispatched: { label: "已派车", tone: "info", priority: 3, needsAction: false },
  partial: { label: "部分完成", tone: "progress", priority: 3, needsAction: false },
  completed: { label: "已完成", tone: "success", priority: 0, needsAction: false },
  cancelled: { label: "已取消", tone: "neutral", priority: 0, needsAction: false },
};

const REGISTRY: Record<StatusKind, Record<string, StatusMeta>> = {
  waybill: WAYBILL, order: ORDER, receipt: RECEIPT, sla: SLA, channel: CHANNEL, batch: BATCH,
};
const FALLBACK_LABEL: Record<StatusKind, Record<string, string>> = {
  waybill: STATUS_LABEL, order: ORDER_STATUS_LABEL, receipt: {}, sla: SLA_STATUS_LABEL, channel: {}, batch: BATCH_STATUS_LABEL,
};

export function statusMeta(kind: StatusKind, value: string): StatusMeta {
  const hit = REGISTRY[kind][value];
  if (hit) return hit;
  return { label: FALLBACK_LABEL[kind][value] ?? value ?? "—", tone: "neutral", priority: 1, needsAction: false };
}

export function StatusTag({
  kind, value, title, suffix, showAction = true,
}: { kind: StatusKind; value: string; title?: string; suffix?: string; showAction?: boolean }) {
  const m = statusMeta(kind, value);
  return (
    <span
      className={`tag ${TONE_CLASS[m.tone]}${m.needsAction && showAction ? " tag-act" : ""}`}
      title={title ?? (m.needsAction ? `${m.label} · 需处理` : m.label)}
    >
      {m.label}{suffix ?? ""}
    </span>
  );
}
