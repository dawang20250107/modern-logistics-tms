import { useMutation } from "@tanstack/react-query";
import { useMemo, useRef, useState } from "react";

import { apiPost } from "../api/client";
import { fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import { useModalA11y } from "../api/useModalA11y";
import type { BatchDispatchResult, Carrier, Order } from "../api/types";
import { ALLOCATION_LABEL } from "../api/types";

// 批量派承运商：把多个订单一次委托给同一承运商/网货平台，生成派车批次 + N 张独立运单。
// 应付分摊在前端做实时预览（与后端一致：按吨占比 / 均摊 / 逐单指定）。
export function BatchDispatchModal({
  orders, carriers, onClose, onDone,
}: {
  orders: Order[];
  carriers: Carrier[];
  onClose: () => void;
  onDone: (r: BatchDispatchResult) => void;
}) {
  const [dispatchType, setDispatchType] = useState("third_party");
  const [carrier, setCarrier] = useState("");
  const [platformName, setPlatformName] = useState("");
  const [totalPayable, setTotalPayable] = useState("");
  const [allocation, setAllocation] = useState("by_weight");
  const [manual, setManual] = useState<Record<string, string>>({});
  const [note, setNote] = useState("");
  const cardRef = useRef<HTMLDivElement>(null);
  useModalA11y(true, cardRef, onClose);

  const totalWeight = useMemo(() => orders.reduce((s, o) => s + (Number(o.cargo_weight_ton) || 0), 0), [orders]);
  const customers = useMemo(() => [...new Set(orders.map((o) => o.customer_name || "散客"))], [orders]);
  const total = Number(totalPayable) || 0;

  // 实时分摊预览（镜像后端 _allocate_payable，末单兜底吸收误差）
  const preview = useMemo(() => {
    const map: Record<string, number> = {};
    if (allocation === "manual") {
      orders.forEach((o) => { map[o.id] = Number(manual[o.id]) || 0; });
      return map;
    }
    if (total <= 0) { orders.forEach((o) => { map[o.id] = 0; }); return map; }
    let running = 0;
    orders.forEach((o, i) => {
      const last = i === orders.length - 1;
      if (allocation === "by_weight" && totalWeight > 0) {
        map[o.id] = last ? Math.round((total - running) * 100) / 100
          : Math.round((total * (Number(o.cargo_weight_ton) || 0) / totalWeight) * 100) / 100;
      } else {
        map[o.id] = last ? Math.round((total - running) * 100) / 100 : Math.round((total / orders.length) * 100) / 100;
      }
      running += map[o.id];
    });
    return map;
  }, [orders, allocation, total, totalWeight, manual]);

  const manualSum = useMemo(() => orders.reduce((s, o) => s + (Number(manual[o.id]) || 0), 0), [orders, manual]);

  const submit = useMutation({
    mutationFn: () => apiPost<BatchDispatchResult>("/orders/batch-dispatch", {
      ids: orders.map((o) => o.id),
      dispatch_type: dispatchType,
      carrier: dispatchType === "third_party" ? carrier : undefined,
      platform_name: dispatchType === "platform" ? platformName : undefined,
      total_payable: allocation === "manual" ? manualSum : total,
      allocation,
      manual_payables: allocation === "manual" ? manual : undefined,
      note,
    }),
    onSuccess: (r) => {
      toast.success(`批次 ${r.batch_no} 已生成：${r.ok.length} 张运单${r.skipped.length ? ` · 跳过 ${r.skipped.length}` : ""}`);
      onDone(r);
    },
    onError: (e: Error) => toast.error(e.message || "批次派单失败"),
  });

  const missingReason = orders.length === 0 ? "无可派订单"
    : dispatchType === "third_party" && !carrier ? "请选择承运商"
    : dispatchType === "platform" && !platformName ? "请填写网货平台"
    : (allocation === "manual" ? manualSum <= 0 : total <= 0) ? "请填写议定应付"
    : "";
  const canSubmit = !missingReason;

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div ref={cardRef} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="bd-title" className="modal-card bd-modal" onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => { if ((e.ctrlKey || e.metaKey) && e.key === "Enter" && canSubmit && !submit.isPending) { e.preventDefault(); submit.mutate(); } }}
      >
        <div className="bd-head">
          <div>
            <div className="bd-title">批量派承运商</div>
            <div className="muted small" style={{ marginTop: 3 }}>
              {orders.length} 单 · {customers.length} 个客户（{customers.slice(0, 3).join("、")}{customers.length > 3 ? "…" : ""}） · 合计 {totalWeight.toFixed(2)} 吨
            </div>
          </div>
          <button className="btn-ghost" onClick={onClose}>关闭 [Esc]</button>
        </div>

        <div className="bd-body">
          {/* 承运通道 */}
          <div className="bd-field">
            <label>承运通道</label>
            <div className="seg-toggle">
              <button className={`seg-btn${dispatchType === "third_party" ? " on" : ""}`} onClick={() => setDispatchType("third_party")}>外包承运商</button>
              <button className={`seg-btn${dispatchType === "platform" ? " on" : ""}`} onClick={() => setDispatchType("platform")}>网货平台</button>
            </div>
          </div>
          {dispatchType === "third_party" ? (
            <div className="bd-field">
              <label>承运商</label>
              <select className="search" value={carrier} onChange={(e) => setCarrier(e.target.value)}>
                <option value="">选择承运商…</option>
                {carriers.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </div>
          ) : (
            <div className="bd-field">
              <label>网货平台</label>
              <input className="search" value={platformName} onChange={(e) => setPlatformName(e.target.value)} placeholder="如 满帮 / 路歌" />
            </div>
          )}

          {/* 应付与分摊 */}
          <div className="bd-row2">
            <div className="bd-field">
              <label>批次总议定应付（元）</label>
              <input className="search" inputMode="decimal" value={totalPayable} disabled={allocation === "manual"}
                onChange={(e) => setTotalPayable(e.target.value)} placeholder={allocation === "manual" ? "逐单填写，自动汇总" : "与承运商议定的总运费"} />
            </div>
            <div className="bd-field">
              <label>分摊方式</label>
              <select className="search" value={allocation} onChange={(e) => setAllocation(e.target.value)}>
                {Object.entries(ALLOCATION_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </div>
          </div>

          {/* 分摊预览 */}
          <div className="bd-field">
            <label>分摊预览{allocation === "manual" ? "（逐单填写）" : "（自动计算，落各运单应付快照）"}</label>
            <div className="bd-preview">
              <table className="table" style={{ fontSize: 12.5 }}>
                <thead><tr><th>订单号</th><th>客户</th><th>线路</th><th className="num">货量</th><th className="num">分摊应付</th></tr></thead>
                <tbody>
                  {orders.map((o) => (
                    <tr key={o.id}>
                      <td className="mono small">{o.order_no}</td>
                      <td className="small">{o.customer_name || "散客"}</td>
                      <td className="small">{o.origin} → {o.destination}</td>
                      <td className="num small">{o.cargo_weight_ton}吨</td>
                      <td className="num">
                        {allocation === "manual" ? (
                          <input className="search" inputMode="decimal" style={{ width: 96, textAlign: "right", padding: "3px 6px" }}
                            value={manual[o.id] ?? ""} onChange={(e) => setManual((m) => ({ ...m, [o.id]: e.target.value }))} placeholder="0" />
                        ) : <b>{fmtMoney(preview[o.id] ?? 0)}</b>}
                      </td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr>
                    <td colSpan={4} className="right"><b>合计</b></td>
                    <td className="num"><b style={{ color: "var(--accent)" }}>{fmtMoney(allocation === "manual" ? manualSum : total)}</b></td>
                  </tr>
                </tfoot>
              </table>
            </div>
          </div>

          <div className="bd-field">
            <label>备注（可选）</label>
            <input className="search" value={note} onChange={(e) => setNote(e.target.value)} placeholder="如：区域循环拼车 / 月度框架委托" />
          </div>
        </div>

        <div className="bd-foot">
          <span className="muted small">{missingReason ? <span style={{ color: "var(--amber)" }}>▸ {missingReason}后可批派</span> : "一批 → " + orders.length + " 张独立运单（各自回单/签收/对账），批次统一对账"}</span>
          <div style={{ flex: 1 }} />
          <button className="btn-ghost" onClick={onClose}>取消</button>
          <button className="btn-primary" disabled={!canSubmit || submit.isPending} onClick={() => submit.mutate()} title={missingReason || "Ctrl+Enter 提交"}>
            {submit.isPending ? "生成批次中…" : `确认批派 ${orders.length} 单`}
          </button>
        </div>
      </div>
    </div>
  );
}
