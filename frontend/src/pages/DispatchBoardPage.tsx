import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { toast } from "../api/toast";
import { EmptyState } from "../components/EmptyState";
import type { Carrier, DispatchSuggestion, Order, Paginated, Vehicle } from "../api/types";
import { BUSINESS_TYPE_LABEL, DISPATCH_TYPE_LABEL, PRIORITY_LABEL } from "../api/types";
import { useEventStream } from "../api/useEventStream";

export function DispatchBoardPage() {
  const queryClient = useQueryClient();
  const [active, setActive] = useState<Order | null>(null);
  const [suggestion, setSuggestion] = useState<DispatchSuggestion | null>(null);
  const [dispatchType, setDispatchType] = useState("third_party");
  const [carrierId, setCarrierId] = useState("");
  const [vehicleId, setVehicleId] = useState("");

  const pool = useQuery({
    queryKey: ["pool"],
    queryFn: () => apiGet<Paginated<Order>>("/orders/pool"),
    refetchInterval: 15000,
  });
  const carriers = useQuery({ queryKey: ["carriers"], queryFn: () => apiGet<Paginated<Carrier>>("/carriers?page_size=200") });
  const vehicles = useQuery({ queryKey: ["vehicles"], queryFn: () => apiGet<Paginated<Vehicle>>("/vehicles?page_size=200") });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["pool"] });

  // 订单池实时变化即刷新（多客服建单 / 多调度抢单）
  useEventStream((e) => {
    if (["order_pooled", "order_claimed", "order_dispatched"].includes(e.type)) invalidate();
  });

  const claim = useMutation({
    mutationFn: (id: string) => apiPost(`/orders/${id}/claim`, {}),
    onSuccess: () => { toast.success("认领成功"); invalidate(); },
  });
  const suggest = useMutation({
    mutationFn: (id: string) => apiGet<DispatchSuggestion>(`/orders/${id}/dispatch-suggestion`),
    onSuccess: (data) => {
      setSuggestion(data);
      setDispatchType(data.suggested_dispatch_type);
      if (data.best_vehicle) setVehicleId("");
    },
  });
  const adopt = () => {
    if (!suggestion) return;
    const type = suggestion.suggested_dispatch_type;
    setDispatchType(type);
    if (type === "third_party") {
      setCarrierId(suggestion.best_carrier?.carrier_id ?? "");
    } else {
      setVehicleId(suggestion.best_vehicle?.vehicle_id ?? "");
    }
  };

  const dispatch = useMutation({
    mutationFn: (id: string) =>
      apiPost(`/orders/${id}/dispatch`, {
        dispatch_type: dispatchType,
        carrier: carrierId || undefined,
        vehicle: vehicleId || undefined,
      }),
    onSuccess: () => {
      setActive(null);
      setSuggestion(null);
      toast.success("派单成功，已生成运单");
      invalidate();
    },
  });

  const orders = pool.data?.items ?? [];

  return (
    <div className="stack">
      <div className="ct-grid">
        <div className="panel">
          <div className="panel-head">订单池 · 待派 {orders.length}</div>
          {pool.isLoading ? (
            <div className="muted" style={{ padding: 16 }}>加载中…</div>
          ) : orders.length === 0 ? (
            <EmptyState icon="🅿️" title="订单池为空" hint="已确认订单进池后将在此等待派单" actionLabel="去建单" actionTo="/intake" />
          ) : (
            <table className="table">
              <thead>
                <tr><th>订单号</th><th>线路</th><th>类型</th><th>优先级</th><th>货量</th><th>认领</th><th>操作</th></tr>
              </thead>
              <tbody>
                {orders.map((o) => (
                  <tr key={o.id} style={active?.id === o.id ? { background: "#f1f5fb" } : {}}>
                    <td className="mono small">{o.order_no}</td>
                    <td>{o.origin} → {o.destination}</td>
                    <td>{BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type}{o.is_hazardous ? " ⚠危" : ""}</td>
                    <td><span className={`tag tag-${o.priority === "vip" ? "high" : o.priority === "urgent" ? "medium" : "none"}`}>{PRIORITY_LABEL[o.priority]}</span></td>
                    <td>{o.cargo_weight_ton}吨</td>
                    <td className="small">{o.claimed_by_name || "-"}</td>
                    <td>
                      <button className="btn-ghost" disabled={claim.isPending} onClick={() => claim.mutate(o.id)}>认领</button>
                      <button className="btn-ghost" onClick={() => { setActive(o); setSuggestion(null); suggest.mutate(o.id); }}>派单</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <div className="panel">
          <div className="panel-head">
            AI 派单建议
            {active && <span className="ai-pill">{active.order_no}</span>}
          </div>
          {!active ? (
            <div className="muted small" style={{ padding: 16 }}>从订单池选「派单」查看 AI 建议</div>
          ) : suggest.isPending ? (
            <div className="muted" style={{ padding: 16 }}>分析中…</div>
          ) : suggestion ? (
            <div style={{ padding: 16 }} className="stack">
              {suggestion.external_signals.length > 0 && (
                <div>
                  {suggestion.external_signals.map((s, i) => (
                    <div key={i} className={`tag tag-${s.level === "high" ? "high" : "medium"}`} style={{ marginRight: 6, marginBottom: 6, display: "inline-block" }}>
                      {s.note}
                    </div>
                  ))}
                </div>
              )}
              <div className="kv" style={{ padding: 0 }}>
                <div>
                  <span>推荐车辆</span>
                  <b>
                    {suggestion.best_vehicle?.plate_no ?? "无可用自有车"}
                    {suggestion.best_vehicle && suggestion.best_vehicle.compliance_ok === false && (
                      <span className="tag tag-high" style={{ marginLeft: 6 }}>⚠ {suggestion.best_vehicle.compliance?.join("/")}证件过期</span>
                    )}
                  </b>
                </div>
                <div><span>最优承运商</span><b>{suggestion.best_carrier ? `${suggestion.best_carrier.carrier} ¥${suggestion.best_carrier.quote}` : "—"}</b></div>
                <div><span>建议派单类型</span><b>{DISPATCH_TYPE_LABEL[suggestion.suggested_dispatch_type]}</b></div>
              </div>

              <button
                className="btn-ghost"
                style={{ alignSelf: "flex-start" }}
                disabled={suggestion.suggested_dispatch_type === "third_party" ? !suggestion.best_carrier : !suggestion.best_vehicle}
                onClick={adopt}
              >
                ⚡ 采纳建议（自动回填运力）
              </button>

              {suggestion.vehicle_candidates.length > 0 && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                  {suggestion.vehicle_candidates.map((v) => (
                    <span key={v.plate_no} className={`tag tag-${v.compliance_ok === false ? "high" : "low"}`}>
                      {v.plate_no} · 装载{Math.round(v.utilization * 100)}%
                      {v.compliance_ok === false && ` ⚠${v.compliance?.join("/")}过期`}
                    </span>
                  ))}
                </div>
              )}

              <div className="form-row" style={{ padding: 0 }}>
                <select value={dispatchType} onChange={(e) => setDispatchType(e.target.value)}>
                  {Object.entries(DISPATCH_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                </select>
                {dispatchType === "third_party" ? (
                  <select value={carrierId} onChange={(e) => setCarrierId(e.target.value)}>
                    <option value="">选承运商</option>
                    {(carriers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
                  </select>
                ) : (
                  <select value={vehicleId} onChange={(e) => setVehicleId(e.target.value)}>
                    <option value="">选车辆</option>
                    {(vehicles.data?.items ?? []).map((v) => <option key={v.id} value={v.id}>{v.plate_no}</option>)}
                  </select>
                )}
                <button
                  className="btn-primary"
                  disabled={dispatch.isPending || (dispatchType === "third_party" ? !carrierId : !vehicleId)}
                  onClick={() => dispatch.mutate(active.id)}
                >
                  {dispatch.isPending ? "派单中…" : "确认派单"}
                </button>
              </div>
              {(dispatchType === "third_party" ? !carrierId : !vehicleId) && (
                <div className="muted small" style={{ padding: 0 }}>
                  请先选择{dispatchType === "third_party" ? "承运商" : "车辆"}再派单
                </div>
              )}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
