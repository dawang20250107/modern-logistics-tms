import { useCallback, useRef } from "react";

import { useConfirm } from "../api/confirm";
import { useModalA11y } from "../api/useModalA11y";

export function ConfirmDialog() {
  const { request, resolve } = useConfirm();
  const danger = request?.tone === "danger";
  const dialogRef = useRef<HTMLDivElement>(null);
  const close = useCallback(() => resolve(false), [resolve]);

  useModalA11y(Boolean(request), dialogRef, close);

  if (!request) return null;
  return (
    <div className="modal-overlay" onClick={close}>
      <div ref={dialogRef} tabIndex={-1} className="confirm-modal" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title" aria-describedby="confirm-body" onClick={(e) => e.stopPropagation()}>
        <div className="confirm-title" id="confirm-title">{request.title}</div>
        <div className="confirm-body" id="confirm-body">{request.message}</div>
        <div className="confirm-actions">
          {/* 危险场景把初始焦点放在「取消」上，回车不会直接执行破坏性操作 */}
          <button type="button" className="btn-ghost" autoFocus={danger} onClick={close}>取消</button>
          <button
            type="button"
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
