import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { fmtRelative } from "../api/format";
import { toast } from "../api/toast";
import { EmptyState } from "../components/EmptyState";
import type { Carrier, DispatchSuggestion, Driver, Order, Paginated, Vehicle } from "../api/types";
import { BUSINESS_TYPE_LABEL, DISPATCH_TYPE_LABEL, ORDER_CHANNEL_LABEL, PRIORITY_LABEL, SLA_STATUS_LABEL } from "../api/types";
import { useEventStream } from "../api/useEventStream";

interface PlanAssignment {
  order_id: string;
  order_no: string;
  route: string;
  weight_ton: number;
  vehicle: { vehicle_id: string; plate_no: string; utilization: number; compliance_ok?: boolean };
}
interface PlanResult {
  assigned_count: number;
  unassigned_count: number;
  assignments: PlanAssignment[];
  unassigned: Array<{ order_id: string; order_no: string }>;
}

export function DispatchBoardPage() {
  const queryClient = useQueryClient();
  const [active, setActive] = useState<Order | null>(null);
  const [suggestion, setSuggestion] = useState<DispatchSuggestion | null>(null);
  const [dispatchType, setDispatchType] = useState("third_party");
  const [carrierId, setCarrierId] = useState("");
  const [vehicleId, setVehicleId] = useState("");
  const [driverId, setDriverId] = useState("");
  const [trailerId, setTrailerId] = useState("");
  const [coDriverIds, setCoDriverIds] = useState<string[]>([]);
  const [mineOnly, setMineOnly] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [plan, setPlan] = useState<PlanResult | null>(null);

  const pool = useQuery({
    queryKey: ["pool", mineOnly],
    queryFn: () => apiGet<Paginated<Order>>(`/orders/pool${mineOnly ? "?mine=1" : ""}`),
    refetchInterval: 15000,
  });
  const carriers = useQuery({ queryKey: ["carriers"], queryFn: () => apiGet<Paginated<Carrier>>("/carriers?page_size=200") });
  const vehicles = useQuery({ queryKey: ["vehicles"], queryFn: () => apiGet<Paginated<Vehicle>>("/vehicles?page_size=200") });
  const drivers = useQuery({ queryKey: ["drivers"], queryFn: () => apiGet<Paginated<Driver>>("/drivers?page_size=200") });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["pool"] });

  // 订单池实时变化即刷新（多客服建单 / 多调度抢单）
  useEventStream((e) => {
    if (["order_pooled", "order_claimed", "order_dispatched"].includes(e.type)) invalidate();
  });

  const claim = useMutation({
    mutationFn: (id: string) => apiPost(`/orders/${id}/claim`, {}),
    onSuccess: () => { toast.success("认领成功"); invalidate(); },
  });
  const release = useMutation({
    mutationFn: (id: string) => apiPost(`/orders/${id}/release`, {}),
    onSuccess: () => { toast.success("已退回订单池"); invalidate(); },
  });

  const togglePick = (id: string) => setPicked((s) => {
    const n = new Set(s);
    if (n.has(id)) n.delete(id);
    else n.add(id);
    return n;
  });
  const makePlan = useMutation({
    mutationFn: () => apiPost<PlanResult>("/orders/dispatch-plan", { ids: [...picked] }),
    onSuccess: (d) => { setPlan(d); toast.success(`已排线：分配 ${d.assigned_count} 单，${d.unassigned_count} 单待三方`); },
  });
  const planDispatch = useMutation({
    mutationFn: (a: { order_id: string; vehicle_id: string }) =>
      apiPost(`/orders/${a.order_id}/dispatch`, { dispatch_type: "own_vehicle", vehicle: a.vehicle_id }),
  });
  const confirmPlan = async () => {
    if (!plan) return;
    for (const a of plan.assignments) {
      await planDispatch.mutateAsync({ order_id: a.order_id, vehicle_id: a.vehicle.vehicle_id });
    }
    toast.success(`已按排线派单 ${plan.assignments.length} 单`);
    setPlan(null);
    setPicked(new Set());
    invalidate();
  };
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
        driver: driverId || undefined,
        trailer: trailerId || undefined,
        co_drivers: coDriverIds.filter((x) => x && x !== driverId),
      }),
    onSuccess: () => {
      setActive(null);
      setSuggestion(null);
      setVehicleId("");
      setCarrierId("");
      setDriverId("");
      setTrailerId("");
      setCoDriverIds([]);
      toast.success("派单成功，已生成运单");
      invalidate();
    },
  });

  const orders = pool.data?.items ?? [];
  // 并发：正在处理的订单若已被他人认领/派出而离开订单池，提示并避免误派
  const activeGone = Boolean(active) && !pool.isLoading && !orders.some((o) => o.id === active?.id);

  return (
    <div className="stack">
      <div className="ct-grid">
        <div className="panel">
          <div className="panel-head">
            订单池 · {mineOnly ? "我认领的" : "全部"} {orders.length}
            <label className="switch-mini" style={{ fontWeight: 400 }}>
              <input type="checkbox" checked={mineOnly} onChange={(e) => setMineOnly(e.target.checked)} /> 仅看我认领
            </label>
          </div>
          {picked.size > 0 && (
            <div className="batch-bar">
              <span>已选 {picked.size} 单</span>
              <button className="btn-primary" disabled={makePlan.isPending} onClick={() => makePlan.mutate()}>🧭 智能排线</button>
              <button className="btn-ghost" onClick={() => { setPicked(new Set()); setPlan(null); }}>清除</button>
            </div>
          )}
          {pool.isLoading ? (
            <div className="muted" style={{ padding: 16 }}>加载中…</div>
          ) : orders.length === 0 ? (
            <EmptyState icon="🅿️" title={mineOnly ? "暂无我认领的订单" : "订单池为空"} hint="已确认订单进池后将在此等待派单" actionLabel="去建单" actionTo="/intake" />
          ) : (
            <table className="table">
              <thead>
                <tr><th style={{ width: 32 }}></th><th>订单号</th><th>来源</th><th>线路</th><th>类型</th><th>优先级</th><th>货量</th><th>等待/时效</th><th>认领</th><th>操作</th></tr>
              </thead>
              <tbody>
                {orders.map((o) => {
                  const claimed = o.status === "dispatching";
                  const urgent = o.sla_status === "breached" || o.sla_status === "at_risk" || o.priority === "vip";
                  return (
                  <tr key={o.id} style={active?.id === o.id ? { background: "#f1f5fb" } : urgent ? { background: "#fff7f7" } : {}}>
                    <td><input type="checkbox" checked={picked.has(o.id)} onChange={() => togglePick(o.id)} /></td>
                    <td className="mono small">{o.order_no}</td>
                    <td className="small">{ORDER_CHANNEL_LABEL[o.channel] ?? o.channel}</td>
                    <td>{o.origin} → {o.destination}</td>
                    <td>{BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type}{o.is_hazardous ? " ⚠危" : ""}</td>
                    <td><span className={`tag tag-${o.priority === "vip" ? "high" : o.priority === "urgent" ? "medium" : "none"}`}>{PRIORITY_LABEL[o.priority]}</span></td>
                    <td>{o.cargo_weight_ton}吨</td>
                    <td className="small">
                      {o.pooled_at && <span title="进池等待时长">⏱ {fmtRelative(o.pooled_at)}</span>}
                      {o.sla_status && o.sla_status !== "pending" && o.sla_status !== "on_time" && (
                        <span className={`tag tag-sla_${o.sla_status}`} style={{ marginLeft: 4 }}>{SLA_STATUS_LABEL[o.sla_status]}</span>
                      )}
                    </td>
                    <td className="small">{claimed ? <span className="tag tag-info">{o.claimed_by_name || "已认领"}</span> : "-"}</td>
                    <td className="row-actions">
                      {!claimed && <button className="btn-ghost" disabled={claim.isPending} onClick={() => claim.mutate(o.id)}>认领</button>}
                      {claimed && <button className="btn-ghost" disabled={release.isPending} onClick={() => release.mutate(o.id)}>退回</button>}
                      <button className="btn-ghost" onClick={() => { setActive(o); setSuggestion(null); setVehicleId(""); setCarrierId(""); setDriverId(""); setTrailerId(""); setCoDriverIds([]); suggest.mutate(o.id); }}>派单</button>
                    </td>
                  </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>

        <div className="panel">
          <div className="panel-head">
            AI 派单建议
            {active && <span className="ai-pill">{active.order_no}</span>}
          </div>
          {active && activeGone ? (
            <div style={{ padding: 16 }} className="stack">
              <div className="tag tag-medium" style={{ alignSelf: "flex-start" }}>⚠ 订单 {active.order_no} 已被他人处理或离开订单池</div>
              <button className="btn-ghost" style={{ alignSelf: "flex-start" }} onClick={() => { setActive(null); setSuggestion(null); }}>关闭</button>
            </div>
          ) : !active ? (
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
                  <>
                    <select value={vehicleId} onChange={(e) => setVehicleId(e.target.value)}>
                      <option value="">选牵引车/单体车</option>
                      {(vehicles.data?.items ?? []).filter((v) => v.vehicle_class !== "trailer").map((v) => (
                        <option key={v.id} value={v.id}>{v.plate_no}{v.vehicle_class_label ? ` · ${v.vehicle_class_label}` : ""}</option>
                      ))}
                    </select>
                    <select value={trailerId} onChange={(e) => setTrailerId(e.target.value)}>
                      <option value="">选挂车（可选）</option>
                      {(vehicles.data?.items ?? []).filter((v) => v.vehicle_class === "trailer").map((v) => (
                        <option key={v.id} value={v.id}>{v.plate_no}</option>
                      ))}
                    </select>
                    <select value={driverId} onChange={(e) => setDriverId(e.target.value)}>
                      <option value="">选主驾（可选）</option>
                      {(drivers.data?.items ?? []).map((d) => (
                        <option key={d.id} value={d.id}>{d.name}{d.employment_label ? ` · ${d.employment_label}` : ""}</option>
                      ))}
                    </select>
                    <select
                      multiple
                      value={coDriverIds}
                      title="随车司机（副驾/接力，可多选）"
                      style={{ minWidth: 140, height: 64 }}
                      onChange={(e) => setCoDriverIds(Array.from(e.target.selectedOptions, (o) => o.value))}
                    >
                      {(drivers.data?.items ?? []).filter((d) => d.id !== driverId).map((d) => (
                        <option key={d.id} value={d.id}>{d.name}</option>
                      ))}
                    </select>
                  </>
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

      {plan && (
        <div className="panel">
          <div className="panel-head">
            智能排线结果 · 分配 {plan.assigned_count} / 待三方 {plan.unassigned_count}
            <div style={{ display: "flex", gap: 8 }}>
              <button className="btn-primary" disabled={planDispatch.isPending || plan.assignments.length === 0} onClick={confirmPlan}>
                {planDispatch.isPending ? "派单中…" : `一键派单 ${plan.assignments.length} 单`}
              </button>
              <button className="btn-ghost" onClick={() => setPlan(null)}>关闭</button>
            </div>
          </div>
          <table className="table">
            <thead><tr><th>订单号</th><th>线路</th><th>货量</th><th>分配车辆</th><th>装载率</th></tr></thead>
            <tbody>
              {plan.assignments.map((a) => (
                <tr key={a.order_id}>
                  <td className="mono small">{a.order_no}</td>
                  <td>{a.route}</td>
                  <td>{a.weight_ton}吨</td>
                  <td>{a.vehicle.plate_no}{a.vehicle.compliance_ok === false && <span className="tag tag-high" style={{ marginLeft: 4 }}>证件过期</span>}</td>
                  <td>{Math.round(a.vehicle.utilization * 100)}%</td>
                </tr>
              ))}
              {plan.unassigned.map((u) => (
                <tr key={u.order_id} style={{ background: "#fff7f7" }}>
                  <td className="mono small">{u.order_no}</td>
                  <td colSpan={4} className="muted small">无合适自有车，建议改三方承运</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
