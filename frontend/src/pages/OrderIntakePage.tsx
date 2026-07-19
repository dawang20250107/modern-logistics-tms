import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { CustomerContextPanel } from "../components/CustomerContextPanel";
import { ExceptionReport } from "../components/ExceptionReport";
import { OrderLifecycle } from "../components/OrderLifecycle";
import { StructuredOrderForm } from "../components/StructuredOrderForm";

// 客服工作台：订单流转纵览 + 全宽建单 + 客户上下文弹窗 + 异常提报。
// 订单台账统一交给「订单管理」，此处专注"高效建单"。
export function OrderIntakePage() {
  const queryClient = useQueryClient();
  const [ctxCustomer, setCtxCustomer] = useState("");
  const [showCtx, setShowCtx] = useState(false);
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["orders"] });

  useEffect(() => {
    if (!showCtx) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setShowCtx(false); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [showCtx]);

  return (
    <div className="stack">
      <OrderLifecycle />

      <StructuredOrderForm
        onCreated={invalidate}
        onCustomerChange={(id) => { setCtxCustomer(id); setShowCtx(Boolean(id)); }}
      />

      {/* 已选合同客户后，可随时唤起上下文，不占建单主空间 */}
      {ctxCustomer && !showCtx && (
        <button className="ctx-reopen" onClick={() => setShowCtx(true)}>
          查看已选客户上下文（账期/授信/常用线路/未完成单）
        </button>
      )}

      <ExceptionReport />

      {showCtx && ctxCustomer && (
        <div className="modal-overlay" onClick={() => setShowCtx(false)}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <span>客户上下文</span>
              <button className="btn-ghost" onClick={() => setShowCtx(false)}>关闭 [Esc]</button>
            </div>
            <div className="modal-body">
              <CustomerContextPanel customerId={ctxCustomer} />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
