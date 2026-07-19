// 五态统一视图：加载 / 错误 / 空 / 离线 / 无权限（信息层级一致、反馈明确）
type StateKind = "loading" | "error" | "empty" | "offline" | "forbidden";

const PRESET: Record<StateKind, { icon: string; title: string; hint: string }> = {
  loading: { icon: "◔", title: "加载中…", hint: "正在获取数据，请稍候。" },
  error: { icon: "!", title: "加载失败", hint: "数据获取出错，请重试或稍后再来。" },
  empty: { icon: "∅", title: "暂无数据", hint: "当前没有符合条件的记录。" },
  offline: { icon: "⚡", title: "网络已断开", hint: "连接恢复后将自动重试。" },
  forbidden: { icon: "🔒", title: "无访问权限", hint: "你的角色暂无权限查看此内容，请联系管理员。" },
};

export function StateView({
  kind, title, hint, action, onRetry, compact,
}: {
  kind: StateKind;
  title?: string;
  hint?: string;
  action?: React.ReactNode;
  onRetry?: () => void;
  compact?: boolean;
}) {
  const p = PRESET[kind];
  if (kind === "loading") {
    return (
      <div style={{ padding: compact ? "16px" : "28px 18px", display: "flex", flexDirection: "column", gap: 10 }}>
        {[1, 0.75, 0.5, 0.3].slice(0, compact ? 2 : 4).map((o, i) => (
          <div key={i} className="skeleton" style={{ width: "100%", height: 28, opacity: o }} />
        ))}
      </div>
    );
  }
  return (
    <div className={`state-view state-${kind}`}>
      <div className="state-icon">{p.icon}</div>
      <div className="state-title">{title ?? p.title}</div>
      <div className="state-hint muted small">{hint ?? p.hint}</div>
      {(onRetry || action) && (
        <div className="state-actions">
          {onRetry && <button className="btn-ghost" onClick={onRetry}>重试</button>}
          {action}
        </div>
      )}
    </div>
  );
}
