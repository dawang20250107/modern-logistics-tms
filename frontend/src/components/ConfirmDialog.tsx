import { useEffect } from "react";

import { useConfirm } from "../api/confirm";

export function ConfirmDialog() {
  const { request, resolve } = useConfirm();

  useEffect(() => {
    if (!request) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") resolve(false);
      if (e.key === "Enter") resolve(true);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [request, resolve]);

  if (!request) return null;
  return (
    <div className="modal-overlay" onClick={() => resolve(false)}>
      <div className="modal" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <div className="modal-title">{request.title}</div>
        <div className="modal-body">{request.message}</div>
        <div className="modal-actions">
          <button className="btn-ghost" onClick={() => resolve(false)}>取消</button>
          <button
            className={request.tone === "danger" ? "btn-danger" : "btn-primary"}
            autoFocus
            onClick={() => resolve(true)}
          >
            {request.confirmText}
          </button>
        </div>
      </div>
    </div>
  );
}
