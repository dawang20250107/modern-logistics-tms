import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { apiGet } from "../api/client";
import { fmtRelative } from "../api/format";
import type { Order, Paginated } from "../api/types";
import { ORDER_STATUS_LABEL } from "../api/types";
import { CustomerContextPanel } from "../components/CustomerContextPanel";
import { ExceptionRegisterModal } from "../components/ExceptionRegisterModal";
import { OrderLifecycle } from "../components/OrderLifecycle";
import { StateView } from "../components/StateView";
import { StatusTag } from "../components/StatusTag";
import { StructuredOrderForm } from "../components/StructuredOrderForm";

// 客服订单池：客服在建单后跟进的订单，双击行/右键可登记异常（同步调度与订单管理）。
function CsOrderPool() {
  const [menu, setMenu] = useState<{ x: number; y: number; order: Order } | null>(null);
  const [excOrder, setExcOrder] = useState<Order | null>(null);
  const [onlyException, setOnlyException] = useState(false);
  const q = useQuery({
    queryKey: ["cs-order-pool"],
    queryFn: () => apiGet<Paginated<Order>>("/orders?ordering=-created_at&page_size=50"),
    refetchInterval: 20000,
  });
  const queryClient = useQueryClient();

  useEffect(() => {
    const onClick = () => setMenu(null);
    window.addEventListener("click", onClick);
    return () => window.removeEventListener("click", onClick);
  }, []);

  const rows = useMemo(() => {
    const items = q.data?.items ?? [];
    return onlyException ? items.filter((o) => (o.exception_count ?? 0) > 0) : items;
  }, [q.data, onlyException]);
  const excTotal = (q.data?.items ?? []).filter((o) => (o.exception_count ?? 0) > 0).length;

  return (
    <div className="panel">
      <div className="panel-head">
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>订单池<span className="ai-pill">{rows.length}</span></span>
        <div className="panel-actions">
          <button className={`chip${onlyException ? " chip-on" : ""}`} onClick={() => setOnlyException((v) => !v)}>仅看异常{excTotal ? ` ${excTotal}` : ""}</button>
          <Link className="link small" to="/waybills">去订单管理 →</Link>
        </div>
      </div>
      <div className="pool-hint">近期订单跟进池 · 双击行或右键可「登记异常」，登记后订单打标并同步调度与订单管理。全量台账请去订单管理。</div>
      {q.isLoading ? (
        <StateView kind="loading" compact />
      ) : rows.length === 0 ? (
        <StateView kind="empty" title={onlyException ? "暂无异常订单" : "暂无订单"} hint={onlyException ? undefined : "在上方建单后将在此跟进。"} />
      ) : (
        <div className="table-wrap">
          <table className="table dispatch-pool" aria-label="客服订单池">
            <thead>
              <tr><th>订单号</th><th>客户</th><th>线路</th><th>状态</th><th>建单</th><th style={{ width: 120 }}>操作</th></tr>
            </thead>
            <tbody>
              {rows.map((o) => (
                <tr
                  key={o.id} className="pool-row"
                  onDoubleClick={() => setExcOrder(o)}
                  onContextMenu={(e) => { e.preventDefault(); setMenu({ x: e.clientX, y: e.clientY, order: o }); }}
                >
                  <td className="mono small">{o.order_no}</td>
                  <td className="small">
                    {o.customer_name || "散客"}
                    {(o.exception_count ?? 0) > 0 && <span className={`tag tag-${o.exception_level === "high" ? "high" : o.exception_level === "low" ? "low" : "medium"}`} style={{ marginLeft: 4 }} title="未闭环异常">⚠ 异常{(o.exception_count ?? 0) > 1 ? `×${o.exception_count}` : ""}</span>}
                  </td>
                  <td className="small"><b>{o.origin}</b> → <b>{o.destination}</b></td>
                  <td><StatusTag kind="order" value={o.status} /></td>
                  <td className="small muted" title={o.created_at}>{fmtRelative(o.created_at)}</td>
                  <td className="row-actions" onClick={(e) => e.stopPropagation()}>
                    <button onClick={() => setExcOrder(o)}>登记异常</button>
                    <Link className="link small" to={`/orders/${o.id}`}>详情</Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {menu && (
        <ul className="ctx-menu" style={{ top: menu.y, left: menu.x }} onClick={(e) => e.stopPropagation()}>
          <li onClick={() => { setExcOrder(menu.order); setMenu(null); }}>登记异常</li>
          <li onClick={() => { window.location.href = `/orders/${menu.order.id}`; setMenu(null); }}>订单详情</li>
        </ul>
      )}
      {excOrder && (
        <ExceptionRegisterModal
          order={excOrder}
          onClose={() => setExcOrder(null)}
          onDone={() => { setExcOrder(null); queryClient.invalidateQueries({ queryKey: ["cs-order-pool"] }); }}
        />
      )}
    </div>
  );
}

// 客服工作台：订单流转纵览 + 全宽建单 + 客户上下文弹窗 + 订单池（登记异常）。
// 订单台账统一交给「订单管理」，此处专注"高效建单 + 异常登记"。
export function OrderIntakePage() {
  const queryClient = useQueryClient();
  const [ctxCustomer, setCtxCustomer] = useState("");
  const [showCtx, setShowCtx] = useState(false);
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["orders"] });
    queryClient.invalidateQueries({ queryKey: ["cs-order-pool"] });
  };

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

      <CsOrderPool />

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
