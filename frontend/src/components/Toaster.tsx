import { dismissToast, useToasts } from "../api/toast";

const ICON = { error: "✕", success: "✓", info: "ℹ" } as const;

export function Toaster() {
  const toasts = useToasts();
  if (toasts.length === 0) return null;
  return (
    <div className="toaster" role="status" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={`toast toast-${t.kind}`} onClick={() => dismissToast(t.id)}>
          <span className="toast-icon">{ICON[t.kind]}</span>
          <span className="toast-msg">{t.message}</span>
          <span className="toast-x">×</span>
        </div>
      ))}
    </div>
  );
}
