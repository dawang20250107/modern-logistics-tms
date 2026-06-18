import type { ReactNode } from "react";
import { Link } from "react-router-dom";

interface Props {
  icon?: string;
  title: string;
  hint?: ReactNode;
  actionLabel?: string;
  actionTo?: string;
}

export function EmptyState({ icon = "📭", title, hint, actionLabel, actionTo }: Props) {
  return (
    <div className="empty-state">
      <div className="empty-icon">{icon}</div>
      <div className="empty-title">{title}</div>
      {hint && <div className="empty-hint muted small">{hint}</div>}
      {actionLabel && actionTo && (
        <Link className="btn-primary" to={actionTo} style={{ textDecoration: "none", marginTop: 12 }}>
          {actionLabel}
        </Link>
      )}
    </div>
  );
}
