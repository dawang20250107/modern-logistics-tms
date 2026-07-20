import { useEffect } from "react";

import { useConfirm } from "../api/confirm";

export function ConfirmDialog() {
  const { request, resolve } = useConfirm();

  const danger = request?.tone === "danger";

  useEffect(() => {
    if (!request) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.preventDefault(); resolve(false); }
      // 危险操作（删除/作废）不绑定回车=确认，避免误触；仅普通确认支持回车快速通过
      if (e.key === "Enter" && !danger) { e.preventDefault(); resolve(true); }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [request, resolve, danger]);

  if (!request) return null;
  return (
    <div className="modal-overlay" onClick={() => resolve(false)}>
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="confirm-title" onClick={(e) => e.stopPropagation()}>
        <div className="modal-title" id="confirm-title">{request.title}</div>
        <div className="modal-body">{request.message}</div>
        <div className="modal-actions">
          {/* 危险场景把初始焦点放在「取消」上，回车不会直接执行破坏性操作 */}
          <button className="btn-ghost" autoFocus={danger} onClick={() => resolve(false)}>取消</button>
          <button
            className={danger ? "btn-danger" : "btn-primary"}
            autoFocus={!danger}
            onClick={() => resolve(true)}
          >
            {request.confirmText}
          </button>
        </div>
      </div>
    </div>
  );
}
