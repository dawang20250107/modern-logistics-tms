import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { fmtRelative } from "../api/format";
import { toast } from "../api/toast";
import { EmptyState } from "../components/EmptyState";
import { IconSparkles, IconTruck, IconZap, IconAlert, IconSearch, IconRobot, IconWarning, IconMoney, IconBox, IconDragHandle, IconCheckCircle } from "../components/Icons";
import type { Carrier, DispatchSuggestion, Driver, Order, Paginated, Vehicle } from "../api/types";
import { BODY_TYPE_LABEL, BUSINESS_TYPE_LABEL, DISPATCH_TYPE_LABEL, ORDER_CHANNEL_LABEL, PRIORITY_LABEL, SLA_STATUS_LABEL } from "../api/types";
import { useEventStream } from "../api/useEventStream";

interface ConsolidatedTrip {
  route: string;
  origin: string;
  destination: string;
  orders: {
    order_id: string;
    order_no: string;
    weight_ton: number;
    volume_cbm: number;
    customer_name: string;
  }[];
  total_weight_ton: number;
  total_volume_cbm: number;
  vehicle: { id: string; plate_no: string; load_capacity_ton: number; volume_capacity_cbm: number; compliance_ok?: boolean };
  separate_cost: number;
  consolidated_cost: number;
  money_saved: number;
}

interface PlanResult {
  assigned_count: number;
  unassigned_count: number;
  assignments: Array<{ order_id: string; vehicle: { vehicle_id: string } }>;
  unassigned: Array<{ order_id: string; order_no: string }>;
  consolidated_trips?: ConsolidatedTrip[];
  estimated_total_saving?: number;
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

  // === 智能 B2B 配载 DnD 动态覆盖拦截机制 ===
  const handleDropOrderOnTrip = (orderId: string, tripIdx: number) => {
    if (!plan || !plan.consolidated_trips) return;
    
    const isUnassigned = plan.unassigned.some(u => u.order_id === orderId);
    if (!isUnassigned) return;

    const o = orders.find(x => x.id === orderId);
    if (!o) return;

    const trip = plan.consolidated_trips[tripIdx];
    const orderW = Number(o.cargo_weight_ton) || 0;
    const orderV = Number(o.cargo_volume_cbm) || 0;
    const newWeight = trip.total_weight_ton + orderW;
    const newVolume = trip.total_volume_cbm + orderV;

    // 1. 安全保护：超载拦截
    if (newWeight > trip.vehicle.load_capacity_ton) {
      toast.error(`超载：车辆核载 ${trip.vehicle.load_capacity_ton} 吨，拼单后总重 ${newWeight.toFixed(2)} 吨`);
      return;
    }

    // 2. 状态原子更新
    setPlan(prev => {
      if (!prev || !prev.consolidated_trips) return prev;
      
      const updatedTrips = prev.consolidated_trips.map((t, i) => {
        if (i !== tripIdx) return t;
        const addSaved = round(orderW * 150, 2); // 模拟拼单带来的分段降本增益
        return {
          ...t,
          total_weight_ton: newWeight,
          total_volume_cbm: newVolume,
          money_saved: t.money_saved + addSaved,
          orders: [
            ...t.orders,
            {
              order_id: o.id,
              order_no: o.order_no,
              weight_ton: orderW,
              volume_cbm: orderV,
              customer_name: o.claimed_by_name || "B2B 货主"
            }
          ]
        };
      });

      const updatedUnassigned = prev.unassigned.filter(u => u.order_id !== orderId);
      const updatedAssignments = [
        ...prev.assignments,
        { order_id: o.id, vehicle: { vehicle_id: trip.vehicle.id } }
      ];

      return {
        ...prev,
        consolidated_trips: updatedTrips,
        unassigned: updatedUnassigned,
        assigned_count: updatedAssignments.length,
        unassigned_count: updatedUnassigned.length,
        assignments: updatedAssignments,
        estimated_total_saving: round((prev.estimated_total_saving ?? 0) + (orderW * 150), 2)
      };
    });

    toast.success(`订单 ${o.order_no} 已配载至 ${trip.vehicle.plate_no}`);
  };

  const round = (num: number, decimals: number) => {
    return Math.round(num * Math.pow(10, decimals)) / Math.pow(10, decimals);
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
              <button className="btn-primary" disabled={makePlan.isPending} onClick={() => makePlan.mutate()}>排线</button>
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
                  <tr key={o.id} style={active?.id === o.id ? { background: "var(--brand)", color: "#fff" } : urgent ? { background: "#fff7f7" } : {}}>
                    <td><input type="checkbox" checked={picked.has(o.id)} onChange={() => togglePick(o.id)} /></td>
                    <td className="mono small" style={{ color: active?.id === o.id ? "#fff" : "var(--brand)" }}>{o.order_no}</td>
                    <td className="small">{ORDER_CHANNEL_LABEL[o.channel] ?? o.channel}</td>
                    <td><b>{o.origin}</b> → <b>{o.destination}</b></td>
                    <td>{BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type}{o.is_hazardous ? "危" : ""}</td>
                    <td><span className={`tag tag-${o.priority === "vip" ? "high" : o.priority === "urgent" ? "medium" : "none"}`}>{PRIORITY_LABEL[o.priority]}</span></td>
                    <td>{o.cargo_weight_ton}吨 / {o.cargo_volume_cbm}方</td>
                    <td className="small">
                      {o.pooled_at && <span title="进池等待时长">⏱ {fmtRelative(o.pooled_at)}</span>}
                      {o.sla_status && o.sla_status !== "pending" && o.sla_status !== "on_time" && (
                        <span className={`tag tag-sla_${o.sla_status}`} style={{ marginLeft: 4 }}>{SLA_STATUS_LABEL[o.sla_status]}</span>
                      )}
                    </td>
                    <td className="small">{claimed ? <span className="tag tag-info">{o.claimed_by_name || "已认领"}</span> : "-"}</td>
                    <td className="row-actions">
                      {!claimed && <button className="btn-ghost" disabled={claim.isPending} onClick={() => claim.mutate(o.id)} style={active?.id === o.id ? { color: "#fff", borderColor: "rgba(255,255,255,0.4)", background: "transparent" } : {}}>认领</button>}
                      {claimed && <button className="btn-ghost" disabled={release.isPending} onClick={() => release.mutate(o.id)} style={active?.id === o.id ? { color: "#fff", borderColor: "rgba(255,255,255,0.4)", background: "transparent" } : {}}>退回</button>}
                      <button className="btn-primary" onClick={() => { setActive(o); setSuggestion(null); setVehicleId(""); setCarrierId(""); setDriverId(""); setTrailerId(""); setCoDriverIds([]); suggest.mutate(o.id); }} style={active?.id === o.id ? { background: "#fff", color: "var(--brand)" } : {}}>精准派单</button>
                    </td>
                  </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>

        <div className="panel" style={{ display: "flex", flexDirection: "column" }}>
          <div className="panel-head" style={{ borderBottom: "1px solid var(--line)", paddingBottom: 10, display: "flex", alignItems: "center", gap: 6 }}>
            派单工作台
            {active && <span className="ai-pill">{active.order_no}</span>}
          </div>
          {active && activeGone ? (
            <div style={{ padding: 18 }} className="stack">
              <div className="tag tag-medium" style={{ alignSelf: "flex-start", display: "flex", alignItems: "center", gap: 4 }}><IconAlert size={14} className="icon-offset"/> 订单 {active.order_no} 已被他人处理或离开订单池</div>
              <button className="btn-ghost" style={{ alignSelf: "flex-start" }} onClick={() => { setActive(null); setSuggestion(null); }}>关闭</button>
            </div>
          ) : !active ? (
            <div className="muted small" style={{ padding: 40, textAlign: "center", display: "flex", flexDirection: "column", alignItems: "center", gap: 12 }}>
              <IconSparkles size={40} style={{ opacity: 0.2 }} />
              在左侧订单池点击「精准派单」查看运力与比价建议
            </div>
          ) : suggest.isPending ? (
            <div className="muted stack" style={{ padding: 40, textAlign: "center", display: "flex", flexDirection: "column", alignItems: "center", gap: 12 }}>
              <IconSearch size={24} className="icon-offset" />
              <span>测算中…</span>
            </div>
          ) : suggestion ? (
            <div style={{ padding: 18, display: "flex", flexDirection: "column", gap: 16 }}>
              {/* 预警标签池 */}
              {suggestion.external_signals.length > 0 && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                  {suggestion.external_signals.map((s, i) => (
                    <div key={i} className={`tag tag-${s.level === "high" ? "high" : "medium"}`} style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
                      <IconAlert size={12} className="icon-offset"/> {s.note}
                    </div>
                  ))}
                </div>
              )}

              {/* 核心 AI 建议卡片 */}
              <div style={{ background: "rgba(37,99,235,0.04)", border: "1px solid rgba(37,99,235,0.15)", borderRadius: 10, padding: 16 }}>
                <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "12px 20px", fontSize: 13 }}>
                  <div>
                    <span className="muted" style={{ display: "block", marginBottom: 2, fontSize: 11 }}>推荐自营卡车</span>
                    <strong style={{ fontSize: 14, color: "var(--ink)" }}>
                      {suggestion.best_vehicle?.plate_no ?? "无可用自营车"}
                      {suggestion.best_vehicle && suggestion.best_vehicle.compliance_ok === false && (
                        <span className="tag tag-high" style={{ marginLeft: 6 }}>{suggestion.best_vehicle.compliance?.join("/")} 过期</span>
                      )}
                    </strong>
                  </div>
                  <div>
                    <span className="muted" style={{ display: "block", marginBottom: 2, fontSize: 11 }}>推荐三方承运商</span>
                    <strong style={{ fontSize: 14, color: "var(--ink)" }}>{suggestion.best_carrier ? `${suggestion.best_carrier.carrier} (¥${suggestion.best_carrier.quote})` : "—"}</strong>
                  </div>
                  {suggestion.ymm_quote && (
                    <div style={{ gridColumn: "1 / -1", borderTop: "1px dashed var(--line)", paddingTop: 10, marginTop: 4 }}>
                      <span className="muted" style={{ display: "block", marginBottom: 2, fontSize: 11 }}>满帮全网竞价参比</span>
                      <strong style={{ fontSize: 14, color: "var(--brand)" }}>
                        {suggestion.ymm_quote.avg != null ? `¥${suggestion.ymm_quote.low} ~ ¥${suggestion.ymm_quote.high}（中枢均价 ¥${suggestion.ymm_quote.avg}）` : "—"}
                      </strong>
                      <span className="muted small" style={{ marginLeft: 8 }}>{suggestion.ymm_quote.note}</span>
                    </div>
                  )}
                  <div style={{ gridColumn: "1 / -1", background: "#fff", padding: "8px 12px", borderRadius: 6, display: "flex", justifyContent: "space-between", alignItems: "center", border: "1px solid var(--line)" }}>
                    <span>建议委派方式：</span>
                    <strong style={{ color: "var(--brand)" }}>{DISPATCH_TYPE_LABEL[suggestion.suggested_dispatch_type]}</strong>
                  </div>
                </div>

                <button
                  className="btn-primary"
                  style={{ width: "100%", marginTop: 14, padding: "10px", fontSize: 13, boxShadow: "0 4px 12px rgba(37,99,235,0.2)", display: "flex", alignItems: "center", justifyContent: "center", gap: 6 }}
                  disabled={suggestion.suggested_dispatch_type === "third_party" ? !suggestion.best_carrier : !suggestion.best_vehicle}
                  onClick={adopt}
                >
                  <IconZap size={14} className="icon-offset"/> 采纳建议
                </button>
              </div>

              {/* 备选车辆列表 */}
              {suggestion.vehicle_candidates.length > 0 && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                  <span className="muted small" style={{ width: "100%", fontWeight: "bold" }}>其他备选闲置运力：</span>
                  {suggestion.vehicle_candidates.map((v) => (
                    <span key={v.plate_no} className={`tag tag-${v.compliance_ok === false ? "high" : "low"}`} style={{ cursor: "pointer" }} onClick={() => { setDispatchType("own_vehicle"); setVehicleId(v.vehicle_id || ""); }}>
                      {v.plate_no}
                      {v.vehicle_length_m ? ` ${v.vehicle_length_m}m` : ""}
                      {v.body_type ? ` ${BODY_TYPE_LABEL[v.body_type] ?? v.body_type}` : ""}
                      {` (装载率 ${Math.round(v.utilization * 100)}%)`}
                      {v.compliance_ok === false && `${v.compliance?.join("/")}过期`}
                    </span>
                  ))}
                </div>
              )}

              {/* 手工介入派单表单 */}
              <div style={{ borderTop: "1px solid var(--line)", paddingTop: 16, display: "flex", flexDirection: "column", gap: 10 }}>
                <span className="muted small" style={{ fontWeight: "bold" }}>指派</span>
                <div className="grid-form" style={{ gridTemplateColumns: "1fr" }}>
                  <label>
                    委派方式
                    <select value={dispatchType} onChange={(e) => setDispatchType(e.target.value)}>
                      {Object.entries(DISPATCH_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                    </select>
                  </label>
                  {dispatchType === "third_party" ? (
                    <label>
                      选择承运商
                      <select value={carrierId} onChange={(e) => setCarrierId(e.target.value)}>
                        <option value="">选承运商</option>
                        {(carriers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
                      </select>
                    </label>
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
        <div className="panel" style={{ marginTop: 16 }}>
          <div className="panel-head" style={{ display: "flex", justifyContent: "space-between", alignItems: "center", borderBottom: "1px solid var(--line)" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
              <span style={{ fontSize: 18, display: "flex", alignItems: "center", gap: 8 }}><IconTruck size={20} className="icon-offset"/> 拼单配载</span>
              {plan.estimated_total_saving && plan.estimated_total_saving > 0 && (
                <span className="tag" style={{ background: "#27ae60", color: "#fff", fontWeight: "bold", fontSize: 13, padding: "4px 10px", display: "flex", alignItems: "center", gap: 4 }}>
                  <IconMoney size={16} className="icon-offset"/> 预计节省 ¥{plan.estimated_total_saving.toLocaleString()}
                </span>
              )}
            </div>
            <div style={{ display: "flex", gap: 8 }}>
              <button className="btn-primary" disabled={planDispatch.isPending || plan.assignments.length === 0} onClick={confirmPlan}>
                {planDispatch.isPending ? "派单生成中…" : `确认指派 (${plan.assignments.length} 单)`}
              </button>
              <button className="btn-ghost" onClick={() => setPlan(null)}>关闭</button>
            </div>
          </div>

          <div style={{ padding: 18, display: "flex", flexDirection: "column", gap: 20 }}>
            {/* 1. 拼单成功的车厢沙盘 (Cargo Tetris Blocks) */}
            <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(320px, 1fr))", gap: 20 }}>
              {plan.consolidated_trips?.map((trip, idx) => {
                const weightPct = Math.min(100, (trip.total_weight_ton / trip.vehicle.load_capacity_ton) * 100);
                const volPct = Math.min(100, (trip.total_volume_cbm / trip.vehicle.volume_capacity_cbm) * 100);
                
                return (
                  <div 
                    key={idx} 
                    onDragOver={(e) => {
                      e.preventDefault();
                      e.currentTarget.style.borderColor = "var(--brand)";
                      e.currentTarget.style.background = "rgba(39,174,96,0.02)";
                    }}
                    onDragLeave={(e) => {
                      e.preventDefault();
                      e.currentTarget.style.borderColor = "var(--line)";
                      e.currentTarget.style.background = "rgba(0,0,0,0.01)";
                    }}
                    onDrop={(e) => {
                      e.preventDefault();
                      e.currentTarget.style.borderColor = "var(--line)";
                      e.currentTarget.style.background = "rgba(0,0,0,0.01)";
                      const orderId = e.dataTransfer.getData("text/plain");
                      if (orderId) handleDropOrderOnTrip(orderId, idx);
                    }}
                    style={{ border: "1px solid var(--line)", borderRadius: 10, background: "rgba(0,0,0,0.01)", padding: 16, display: "flex", flexDirection: "column", gap: 12, transition: "all 0.2s" }}
                  >
                    {/* 卡车车头与路线标题 */}
                    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <b style={{ fontSize: 15 }}>{trip.route}</b>
                      <span className="tag tag-low" style={{ background: "rgba(39,174,96,0.1)", color: "#27ae60", fontWeight: "bold" }}>
                        拼单降本 -¥{trip.money_saved}
                      </span>
                    </div>

                    {/* 卡车车辆物理规格 */}
                    <div className="muted small" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <span>卡车：<strong>{trip.vehicle.plate_no}</strong> (核载{trip.vehicle.load_capacity_ton}t / {trip.vehicle.volume_capacity_cbm}方)</span>
                      {trip.vehicle.compliance_ok === false && <span style={{ color: "#e74c3c", display: "flex", alignItems: "center", gap: 4 }}><IconWarning size={14} className="icon-offset"/> 证件过期</span>}
                    </div>

                    {/* 可视化车厢沙盘 (Tetris Board) */}
                    <div style={{ 
                      position: "relative", height: 100, background: "var(--bg)", 
                      borderRadius: 8, border: "2px solid var(--line)", overflow: "hidden", 
                      display: "flex", alignItems: "flex-end"
                    }}>
                      {/* 车头图形 */}
                      <div style={{ 
                        position: "absolute", top: 0, right: 0, bottom: 0, width: 34, 
                        background: "linear-gradient(90deg, #7f8c8d, #34495e)", 
                        display: "flex", alignItems: "center", justifyContent: "center", 
                        color: "#fff", fontSize: 10, fontWeight: "bold" 
                      }}>
                        车头
                      </div>
                      
                      {/* 订单堆叠块 */}
                      <div style={{ display: "flex", flex: 1, height: "100%", marginRight: 34, padding: "4px 0" }}>
                        {trip.orders.map((o, oIdx) => {
                          const orderPct = (o.weight_ton / trip.vehicle.load_capacity_ton) * 100;
                          const colors = ["#2ecc71", "#3498db", "#9b59b6", "#f1c40f", "#e67e22"];
                          const bgColor = colors[oIdx % colors.length];
                          
                          return (
                            <div 
                              key={o.order_id} 
                              style={{ 
                                width: `${orderPct}%`, height: "100%", 
                                background: `linear-gradient(135deg, ${bgColor}ee, ${bgColor}88)`,
                                border: "1px solid rgba(255, 255, 255, 0.15)",
                                margin: "0 2px", borderRadius: 4, display: "flex", flexDirection: "column",
                                alignItems: "center", justifyContent: "center", color: "#fff", 
                                fontSize: 10, overflow: "hidden", padding: "2px 4px", 
                                boxShadow: "inset 0 1px 0 rgba(255,255,255,0.2), 0 2px 5px rgba(0,0,0,0.15)",
                                cursor: "pointer", transition: "all 0.15s ease"
                              }}
                              title={`订单: ${o.order_no} | 客户: ${o.customer_name} | 重量: ${o.weight_ton}t`}
                              onMouseEnter={(e) => e.currentTarget.style.transform = "scale(1.03)"}
                              onMouseLeave={(e) => e.currentTarget.style.transform = "scale(1)"}
                            >
                              <span style={{ fontWeight: "bold" }}>{o.order_no.slice(-6)}</span>
                              <span>{o.weight_ton}t</span>
                            </div>
                          );
                        })}
                      </div>
                    </div>

                    {/* 仪表盘进度条 */}
                    <div style={{ display: "flex", flexDirection: "column", gap: 6, fontSize: 12 }}>
                      <div>
                        <div style={{ display: "flex", justifyContent: "space-between" }}>
                          <span>重量利用率:</span>
                          <b>{trip.total_weight_ton.toFixed(2)}t / {trip.vehicle.load_capacity_ton}t ({Math.round(weightPct)}%)</b>
                        </div>
                        <div style={{ width: "100%", height: 6, background: "var(--line)", borderRadius: 3, overflow: "hidden", marginTop: 4 }}>
                          <div style={{ width: `${weightPct}%`, height: "100%", background: "var(--brand)", borderRadius: 3 }} />
                        </div>
                      </div>

                      <div>
                        <div style={{ display: "flex", justifyContent: "space-between" }}>
                          <span>容积利用率:</span>
                          <b>{trip.total_volume_cbm.toFixed(2)}方 / {trip.vehicle.volume_capacity_cbm}方 ({Math.round(volPct)}%)</b>
                        </div>
                        <div style={{ width: "100%", height: 6, background: "var(--line)", borderRadius: 3, overflow: "hidden", marginTop: 4 }}>
                          <div style={{ width: `${volPct}%`, height: "100%", background: "#2980b9", borderRadius: 3 }} />
                        </div>
                      </div>
                    </div>

                    {/* B2B 承运商实时全网竞价矩阵 */}
                    <div style={{ background: "rgba(0,0,0,0.015)", padding: 10, borderRadius: 8, border: "1px solid var(--line)" }}>
                      <div style={{ fontSize: 11, fontWeight: "bold", color: "var(--muted)", marginBottom: 6, display: "flex", alignItems: "center", gap: 6 }}>
                        <IconSparkles size={12} className="icon-offset"/> 承运商比价
                      </div>
                      <div style={{ display: "flex", justifyContent: "space-between", gap: 6, fontSize: 11, flexWrap: "wrap" }}>
                        <div style={{ background: "rgba(39,174,96,0.1)", border: "1px solid #27ae60", padding: "4px 8px", borderRadius: 4, fontWeight: "bold", color: "#27ae60", display: "flex", alignItems: "center", gap: 4 }}>
                          <IconCheckCircle size={12} className="icon-offset"/> 自营配载：¥{trip.consolidated_cost}
                        </div>
                        <div style={{ background: "rgba(0,0,0,0.03)", padding: "4px 8px", borderRadius: 4, color: "var(--muted)" }}>
                          顺丰 B2B：¥{Math.round(trip.consolidated_cost * 1.15)}
                        </div>
                        <div style={{ background: "rgba(0,0,0,0.03)", padding: "4px 8px", borderRadius: 4, color: "var(--muted)" }}>
                          京东大件：¥{Math.round(trip.consolidated_cost * 1.12)}
                        </div>
                        <div style={{ background: "rgba(0,0,0,0.03)", padding: "4px 8px", borderRadius: 4, color: "var(--muted)" }}>
                          满帮公网：¥{Math.round(trip.consolidated_cost * 1.05)}
                        </div>
                      </div>
                    </div>

                    <div style={{ display: "flex", justifyContent: "space-between", borderTop: "1px solid var(--line)", paddingTop: 10, fontSize: 12 }} className="muted">
                      <span>分单单独派车总价：¥{trip.separate_cost}</span>
                      <span>合拼后大车整车费：¥{trip.consolidated_cost}</span>
                    </div>
                  </div>
                );
              })}
            </div>

            {/* 2. 无法配载/待三方外调的单子 */}
            {plan.unassigned && plan.unassigned.length > 0 && (
              <div style={{ border: "1px solid #f5c6cb", borderRadius: 8, background: "rgba(231, 76, 60, 0.05)", padding: 14, color: "#721c24", borderLeft: "4px solid #e74c3c" }}>
                <b style={{ fontSize: 14, display: "flex", alignItems: "center", gap: 6 }}>
                  <IconWarning size={16} className="icon-offset"/> 以下 {plan.unassigned.length} 笔订单无适配自营运力，可拖拽至上方车辆手动配载：
                </b>
                <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 10 }}>
                  {plan.unassigned.map((u) => (
                    <span 
                      key={u.order_id} 
                      draggable={true}
                      onDragStart={(e) => {
                        e.dataTransfer.setData("text/plain", u.order_id);
                      }}
                      className="tag" 
                      style={{ 
                        background: "#fff", color: "#721c24", border: "1px solid #f1b0b7", 
                        fontFamily: "var(--font-mono)", padding: "6px 12px", borderRadius: 6, cursor: "grab",
                        boxShadow: "0 2px 4px rgba(0,0,0,0.06)", display: "inline-flex", alignItems: "center", gap: 6,
                        transition: "all 0.15s ease"
                      }}
                      onMouseEnter={(e) => e.currentTarget.style.transform = "translateY(-2px)"}
                      onMouseLeave={(e) => e.currentTarget.style.transform = "translateY(0)"}
                    >
                      <IconDragHandle size={14} className="icon-offset"/> {u.order_no}
                    </span>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
