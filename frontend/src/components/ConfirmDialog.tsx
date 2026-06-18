import { useConfirm } from "../api/confirm";

export function ConfirmDialog() {
  const { request, resolve } = useConfirm();
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
