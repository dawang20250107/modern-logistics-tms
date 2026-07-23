// 五态统一视图：加载 / 错误 / 空 / 离线 / 无权限（信息层级一致、反馈明确）
import {
  IconBox, IconCheckCircle, IconClock, IconInbox, IconLock, IconReceipt, IconTruck, IconWarning, IconWifiOff,
} from "./Icons";

type StateKind = "loading" | "error" | "empty" | "offline" | "forbidden";
const IC = 24;

const PRESET: Record<StateKind, { icon: React.ReactNode; title: string; hint: string }> = {
  loading: { icon: <IconClock size={IC} />, title: "加载中…", hint: "正在获取数据，请稍候。" },
  error: { icon: <IconWarning size={IC} />, title: "加载失败", hint: "数据获取出错，请重试或稍后再来。" },
  empty: { icon: <IconInbox size={IC} />, title: "暂无数据", hint: "当前没有符合条件的记录。" },
  offline: { icon: <IconWifiOff size={IC} />, title: "网络已断开", hint: "连接恢复后将自动重试。" },
  forbidden: { icon: <IconLock size={IC} />, title: "无访问权限", hint: "你的角色暂无权限查看此内容，请联系管理员。" },
};

// 角色化场景文案：五态组件统一，但文案贴合岗位场景（让人知道"现在该做什么"）
const SCENE: Record<string, { icon?: React.ReactNode; title: string; hint: string }> = {
  "cs-empty": { icon: <IconCheckCircle size={IC} />, title: "暂无待跟进订单", hint: "今天的客户订单都已处理完成。" },
  "pool-empty": { icon: <IconClock size={IC} />, title: "订单池为空", hint: "已确认订单进入调度池后，将在这里等待派单。" },
  "driver-empty": { icon: <IconTruck size={IC} />, title: "当前暂无运输任务", hint: "请等待调度派单，有新任务会自动提醒。" },
  "exception-empty": { icon: <IconCheckCircle size={IC} />, title: "暂无异常工单", hint: "有新异常提报或系统预警时会出现在这里。" },
  "recon-empty": { icon: <IconReceipt size={IC} />, title: "暂无对账数据", hint: "生成账期账单后将在这里按客户/承运商归集。" },
  "waybill-empty": { icon: <IconTruck size={IC} />, title: "未找到运单", hint: "调整筛选维度或清空条件再试。" },
  "carrier-empty": { icon: <IconBox size={IC} />, title: "暂无承运商", hint: "先在承运商中心建档，调度即可按线路比价派单。" },
};

export function StateView({
  kind, scene, title, hint, action, onRetry, compact,
}: {
  kind: StateKind;
  scene?: keyof typeof SCENE;
  title?: string;
  hint?: string;
  action?: React.ReactNode;
  onRetry?: () => void;
  compact?: boolean;
}) {
  const s = scene ? SCENE[scene] : undefined;
  const p = { ...PRESET[kind], ...(s ?? {}) };
  if (kind === "loading") {
    return (
      <div className={`state-loading${compact ? " state-compact" : ""}`} role="status" aria-live="polite" aria-label={title ?? p.title}>
        <span className="sr-only">{title ?? p.title}</span>
        {[1, 0.75, 0.5, 0.3].slice(0, compact ? 2 : 4).map((o, i) => (
          <div key={i} className="skeleton" aria-hidden="true" style={{ width: "100%", height: 28, opacity: o }} />
        ))}
      </div>
    );
  }
  return (
    <div
      className={`state-view state-${kind}${compact ? " state-compact" : ""}`}
      role={kind === "error" ? "alert" : "status"}
      aria-live={kind === "error" ? "assertive" : "polite"}
    >
      <div className="state-icon" aria-hidden="true">{p.icon}</div>
      <div className="state-title">{title ?? p.title}</div>
      <div className="state-hint muted small">{hint ?? p.hint}</div>
      {(onRetry || action) && (
        <div className="state-actions">
          {onRetry && <button type="button" className="btn-ghost" onClick={onRetry}>重试</button>}
          {action}
        </div>
      )}
    </div>
  );
}
