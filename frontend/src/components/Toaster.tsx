import { dismissToast, useToasts } from "../api/toast";

const ICON = { error: "✕", success: "✓", info: "ℹ" } as const;

export function Toaster() {
  const toasts = useToasts();
  if (toasts.length === 0) return null;
  return (
    <div className="toaster" aria-label="操作消息">
      {toasts.map((t) => (
        <div
          key={t.id}
          className={`toast toast-${t.kind}`}
          role={t.kind === "error" ? "alert" : "status"}
          aria-live={t.kind === "error" ? "assertive" : "polite"}
        >
          <span className="toast-icon" aria-hidden="true">{ICON[t.kind]}</span>
          <span className="toast-msg">{t.message}</span>
          <button type="button" className="toast-x" aria-label="关闭消息" onClick={() => dismissToast(t.id)}>×</button>
        </div>
      ))}
    </div>
  );
}
