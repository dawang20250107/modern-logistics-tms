import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import { fmtMoney, fmtRelative } from "../api/format";
import { toast } from "../api/toast";
import { ExceptionQueue } from "../components/ExceptionQueue";
import { StateView } from "../components/StateView";
import { IconSparkles, IconTruck, IconZap, IconAlert, IconSearch, IconWarning, IconMoney, IconDragHandle, IconCheckCircle, IconMapPin, IconGitBranch, IconX } from "../components/Icons";
import { TrajectoryMap, type Trajectory } from "../components/TrajectoryMap";
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

// 手工提报异常类型（与后端 exception_type 选项一致）
const MANUAL_EXC_TYPES: [string, string][] = [
  ["transit_delay", "在途超时"], ["route_deviation", "偏航/路线异常"], ["cargo_damage", "货损货差"],
  ["vehicle_breakdown", "车辆故障"], ["detained", "扣车扣货"], ["customer_complaint", "客户投诉"],
  ["temperature", "冷链温度异常"], ["abnormal_stop", "异常停车"], ["other", "其他"],
];
const EXC_LEVELS: [string, string][] = [["high", "高"], ["medium", "中"], ["low", "低"]];
const RISK_TAG: Record<string, string> = { high: "high", medium: "medium", low: "low" };

type DrawerTab = "dispatch" | "track" | "exception";
type MenuState = { x: number; y: number; order: Order } | null;

const CUST_LEVEL_TONE: Record<string, string> = { S: "tag-info", A: "tag-low", B: "tag-info", C: "tag-medium", D: "tag-none" };

// 锁定/分派状态渲染：让调度一眼看清这单归谁、能不能派
function renderLock(o: Order) {
  switch (o.lock_state) {
    case "mine": return <span className="tag tag-low tag-act">我锁定</span>;
    case "locked": return <span className="tag tag-medium" title={`锁定人：${o.claimed_by_name}`}>他人锁定 · {o.claimed_by_name}</span>;
    case "assigned_mine": return <span className="tag tag-info">分派给我</span>;
    case "assigned_other": return <span className="tag tag-none" title={`已分派给：${o.assigned_to_name}`}>分派 · {o.assigned_to_name}</span>;
    default: return <span className="muted small">未锁定</span>;
  }
}

export function DispatchBoardPage() {
  const queryClient = useQueryClient();
  const [active, setActive] = useState<Order | null>(null);
  const [tab, setTab] = useState<DrawerTab>("dispatch");
  const [menu, setMenu] = useState<MenuState>(null);
  const [focusIdx, setFocusIdx] = useState(-1);
  const [suggestion, setSuggestion] = useState<DispatchSuggestion | null>(null);
  const [dispatchType, setDispatchType] = useState("third_party");
  const [carrierId, setCarrierId] = useState("");
  const [vehicleId, setVehicleId] = useState("");
  const [driverId, setDriverId] = useState("");
  const [trailerId, setTrailerId] = useState("");
  const [coDriverIds, setCoDriverIds] = useState<string[]>([]);
  const [platformName, setPlatformName] = useState("");
  const [platformOrderNo, setPlatformOrderNo] = useState("");
  const [agreedPayable, setAgreedPayable] = useState("");
  const [mineOnly, setMineOnly] = useState(false);
  const [urgentOnly, setUrgentOnly] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [plan, setPlan] = useState<PlanResult | null>(null);
  // 手工提报异常表单
  const [excType, setExcType] = useState("transit_delay");
  const [excLevel, setExcLevel] = useState("medium");
  const [excDesc, setExcDesc] = useState("");

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

  // Esc 关闭抽屉；全局点击关闭右键菜单
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") { closeWb(); setMenu(null); } };
    const onClick = () => setMenu(null);
    window.addEventListener("keydown", onKey);
    window.addEventListener("click", onClick);
    return () => { window.removeEventListener("keydown", onKey); window.removeEventListener("click", onClick); };
  }, []);

  const claim = useMutation({
    mutationFn: (id: string) => apiPost(`/orders/${id}/claim`, {}),
    onSuccess: () => { toast.success("已锁定，可由你调派"); invalidate(); },
    onError: (e: Error) => toast.error(e.message || "锁定失败：可能已被他人锁定"),
  });
  const release = useMutation({
    mutationFn: (id: string) => apiPost(`/orders/${id}/release`, {}),
    onSuccess: () => { toast.success("已退回订单池"); invalidate(); },
  });

  // 总调度分单能力探测 + 可分派成员
  const dispatchers = useQuery({
    queryKey: ["dispatchers"],
    queryFn: () => apiGet<{ is_chief: boolean; me: { id: string; name: string }; dispatchers: Array<{ id: string; name: string; username: string }> }>("/orders/dispatchers"),
  });
  const isChief = dispatchers.data?.is_chief ?? false;
  const [assignTo, setAssignTo] = useState("");

  // 批量锁定（逐单 claim，行锁保证并发安全；统计成功/被抢）
  const lockMany = useMutation({
    mutationFn: async (ids: string[]) => {
      let ok = 0; let fail = 0;
      for (const id of ids) {
        try { await apiPost(`/orders/${id}/claim`, {}); ok++; } catch { fail++; }
      }
      return { ok, fail };
    },
    onSuccess: (r) => { toast.success(`锁定完成：成功 ${r.ok}${r.fail ? ` · ${r.fail} 单已被他人锁定` : ""}`); setPicked(new Set()); invalidate(); },
  });
  // 总调度分单
  const assignMany = useMutation({
    mutationFn: (v: { ids: string[]; dispatcher: string }) => apiPost<{ assigned: string[]; skipped: string[]; dispatcher: string }>("/orders/assign", v),
    onSuccess: (r) => { toast.success(`已分派 ${r.assigned.length} 单给 ${r.dispatcher}${r.skipped.length ? ` · 跳过 ${r.skipped.length}` : ""}`); setPicked(new Set()); setAssignTo(""); invalidate(); },
    onError: (e: Error) => toast.error(e.message || "分单失败"),
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
  const recCarrierId = suggestion?.recommendation?.carrier_id ?? suggestion?.best_carrier?.carrier_id ?? "";
  const pickCarrier = (id: string) => { setDispatchType("third_party"); setCarrierId(id); };
  const adopt = () => {
    if (!suggestion) return;
    const type = suggestion.suggested_dispatch_type;
    setDispatchType(type);
    if (type === "third_party") setCarrierId(recCarrierId);
    else if (type === "own_vehicle") setVehicleId(suggestion.best_vehicle?.vehicle_id ?? "");
    // 采纳时带出建议成交价中值，作为议定应付默认值
    const band = suggestion.recommendation?.suggested_price_band;
    if (band) setAgreedPayable(String(Math.round((band[0] + band[1]) / 2)));
    // platform：由调度员手填平台名/单号
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
        platform_name: platformName || undefined,
        platform_order_no: platformOrderNo || undefined,
        agreed_payable_amount: agreedPayable ? Number(agreedPayable) : undefined,
        price_source: agreedPayable ? "manual" : undefined,
      }),
    onSuccess: () => {
      closeWb();
      toast.success("派单成功，已生成运单");
      invalidate();
    },
  });

  // 一键派单：直接按 AI「综合推荐」承运商落单（非最低价），带议定应付金额快照
  const quickDispatch = useMutation({
    mutationFn: (body: Record<string, unknown>) => apiPost(`/orders/${active!.id}/dispatch`, body),
    onSuccess: () => { closeWb(); toast.success("已一键派单，生成运单"); invalidate(); },
  });
  const oneClickDispatch = () => {
    if (!active || !suggestion) return;
    const type = suggestion.suggested_dispatch_type;
    if (type === "own_vehicle") {
      quickDispatch.mutate({ dispatch_type: "own_vehicle", vehicle: suggestion.best_vehicle?.vehicle_id });
      return;
    }
    // 外包：派给综合推荐承运商（recommendation），议定应付取建议价区间中值
    const rec = suggestion.recommendation;
    const band = rec?.suggested_price_band;
    const agreed = band ? Math.round((band[0] + band[1]) / 2) : undefined;
    quickDispatch.mutate({
      dispatch_type: "third_party",
      carrier: recCarrierId,
      agreed_payable_amount: agreed,
      price_source: "recommended",
      quote_id: rec?.carrier_id,
    });
  };
  // 网货平台需手填平台名，不走一键；外包需已有推荐承运商，自营需有可用车
  const canOneClick = suggestion
    ? (suggestion.suggested_dispatch_type === "platform"
        ? false
        : suggestion.suggested_dispatch_type === "own_vehicle"
          ? Boolean(suggestion.best_vehicle)
          : Boolean(recCarrierId))
    : false;

  // 手工提报异常
  const reportException = useMutation({
    mutationFn: () => apiPost("/exceptions", {
      exception_type: excType, level: excLevel, description: excDesc, source: "manual",
    }),
    onSuccess: () => { toast.success("异常已提报"); setExcDesc(""); },
  });

  // 抽屉开合
  function openWb(o: Order, initialTab: DrawerTab = "dispatch") {
    setActive(o);
    setTab(initialTab);
    setSuggestion(null);
    setVehicleId(""); setCarrierId(""); setDriverId(""); setTrailerId(""); setCoDriverIds([]);
    setPlatformName(""); setPlatformOrderNo(""); setAgreedPayable("");
    setExcDesc("");
    if (initialTab === "dispatch") suggest.mutate(o.id);
  }
  function closeWb() {
    setActive(null);
    setSuggestion(null);
  }

  const orders = pool.data?.items ?? [];
  const isUrgent = (o: Order) => o.sla_status === "breached" || o.sla_status === "at_risk" || o.priority === "vip";
  // 紧急优先排序 + 可选仅看紧急
  const rows = [...orders]
    .filter((o) => !urgentOnly || isUrgent(o))
    .sort((a, b) => Number(isUrgent(b)) - Number(isUrgent(a)));
  // 并发：正在处理的订单若已被他人认领/派出而离开订单池，提示并避免误派
  const activeGone = Boolean(active) && !pool.isLoading && !orders.some((o) => o.id === active?.id);
  const trackNo = active?.waybill_nos?.[0];

  // 调度工作流概览：一眼看清 待派 / 紧急 / 临期超时 / 已选
  const wf = {
    pending: orders.length,
    urgent: orders.filter((o) => o.priority === "urgent" || o.priority === "vip").length,
    atRisk: orders.filter((o) => o.sla_status === "at_risk" || o.sla_status === "breached").length,
    picked: picked.size,
  };

  // 键盘选单：抽屉未开时，↑↓ 选单、Enter 拉出派单抽屉（输入框内不接管）
  useEffect(() => {
    if (active || rows.length === 0) return;
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "INPUT" || tag === "SELECT" || tag === "TEXTAREA") return;
      if (e.key === "ArrowDown") { e.preventDefault(); setFocusIdx((i) => Math.min(i + 1, rows.length - 1)); }
      else if (e.key === "ArrowUp") { e.preventDefault(); setFocusIdx((i) => Math.max(i <= 0 ? 0 : i - 1, 0)); }
      else if (e.key === "Enter" && focusIdx >= 0 && focusIdx < rows.length) { e.preventDefault(); openWb(rows[focusIdx]); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [active, rows, focusIdx]);

  // 抽屉「轨迹」tab 的轨迹数据（已派单订单才有运单轨迹）
  const traj = useQuery<Trajectory>({
    queryKey: ["dispatch-traj", trackNo],
    queryFn: () => apiGet<Trajectory>(`/telematics/waybills/${trackNo}/trajectory`),
    enabled: Boolean(active) && tab === "track" && Boolean(trackNo),
  });

  return (
    <div className="stack dispatch-page">
      {/* 调度指挥台：待派→紧急→临期→执行，一眼定位当前该处理什么 */}
      <div className="dispatch-deck">
        <div className="deck-brand">
          <div className="deck-brand-ic"><IconTruck size={22} /></div>
          <div>
            <div className="deck-title">调度指挥台</div>
            <div className="deck-sub">{mineOnly ? "我认领的订单" : "全部待派订单"} · 实时刷新 · ↑↓/Enter 键盘直达</div>
          </div>
        </div>
        <div className="deck-metrics">
          <div className="deck-tile deck-tile-neutral">
            <div className="deck-tile-ic"><IconTruck size={16} /></div>
            <div className="deck-tile-body"><b>{wf.pending}</b><span>待派订单</span></div>
          </div>
          <button className={`deck-tile deck-tile-hot deck-clickable${urgentOnly ? " on" : ""}`} onClick={() => setUrgentOnly((v) => !v)} title="仅看紧急">
            <div className="deck-tile-ic"><IconZap size={16} /></div>
            <div className="deck-tile-body"><b className={wf.urgent ? "num-hot" : ""}>{wf.urgent}</b><span>紧急 {urgentOnly ? "· 已筛" : ""}</span></div>
            {wf.urgent > 0 && <span className="deck-pulse" aria-hidden />}
          </button>
          <div className="deck-tile deck-tile-warn">
            <div className="deck-tile-ic"><IconAlert size={16} /></div>
            <div className="deck-tile-body"><b className={wf.atRisk ? "num-warn" : ""}>{wf.atRisk}</b><span>临期 / 超时</span></div>
          </div>
          <div className="deck-tile deck-tile-accent">
            <div className="deck-tile-ic"><IconCheckCircle size={16} /></div>
            <div className="deck-tile-body"><b className={wf.picked ? "num-accent" : ""}>{wf.picked}</b><span>已选待排线</span></div>
          </div>
        </div>
      </div>
      <div className="panel dispatch-board-panel">
        <div className="panel-head">
          <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
            订单池
            <span className="ai-pill">{mineOnly ? "我认领的" : "全部"} {rows.length}</span>
          </span>
          <div className="panel-actions">
            <button className={`chip${urgentOnly ? " chip-on" : ""}`} onClick={() => setUrgentOnly((v) => !v)}>仅看紧急</button>
            <label className="switch-mini" style={{ fontWeight: 400 }}>
              <input type="checkbox" checked={mineOnly} onChange={(e) => setMineOnly(e.target.checked)} /> 仅看我认领
            </label>
          </div>
        </div>
        {picked.size > 0 && (
          <div className="batch-bar">
            <span>已选 <b style={{ color: "var(--accent)" }}>{picked.size}</b> 单</span>
            <div style={{ flex: 1 }} />
            <button className="btn-ghost" disabled={lockMany.isPending} onClick={() => lockMany.mutate([...picked])}>锁定所选</button>
            {isChief && (
              <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                <select className="search" style={{ minWidth: 150, padding: "6px 10px" }} value={assignTo} onChange={(e) => setAssignTo(e.target.value)}>
                  <option value="">分派给…</option>
                  {(dispatchers.data?.dispatchers ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
                </select>
                <button className="btn-ghost" disabled={!assignTo || assignMany.isPending} onClick={() => assignMany.mutate({ ids: [...picked], dispatcher: assignTo })}>分单</button>
              </span>
            )}
            <button className="btn-primary" disabled={makePlan.isPending} onClick={() => makePlan.mutate()}>智能排线拼单</button>
            <button className="btn-ghost" onClick={() => { setPicked(new Set()); setPlan(null); }}>清除</button>
          </div>
        )}
        {pool.isLoading ? (
          <StateView kind="loading" compact />
        ) : rows.length === 0 ? (
          <StateView
            kind="empty"
            scene="pool-empty"
            title={urgentOnly ? "暂无紧急订单" : mineOnly ? "暂无我认领的订单" : "订单池为空"}
            hint="已确认订单进池后将在此等待派单 · 双击或右键订单可打开派单工作台"
            action={<Link className="btn-primary" to="/intake" style={{ textDecoration: "none" }}>去建单</Link>}
          />
        ) : (
          <div className="table-wrap">
          <table className="table dispatch-pool" aria-label="订单池">
            <thead>
              <tr>
                <th style={{ width: 32 }}></th>
                <th>订单号</th><th>客户</th><th>线路</th><th>类型</th><th>优先级</th><th className="num">货量</th><th className="num">应收</th><th>等待/时效</th><th>锁定 / 分派</th><th style={{ width: 150 }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((o, idx) => {
                const canDispatch = o.dispatchable !== false;
                return (
                  <tr
                    key={o.id}
                    className={`pool-row${active?.id === o.id ? " row-active" : ""}${idx === focusIdx ? " row-focus" : ""}${isUrgent(o) ? " row-urgent" : ""}`}
                    onMouseEnter={() => setFocusIdx(idx)}
                    onDoubleClick={() => canDispatch && openWb(o)}
                    onContextMenu={(e) => { e.preventDefault(); setMenu({ x: e.clientX, y: e.clientY, order: o }); }}
                  >
                    <td onClick={(e) => e.stopPropagation()}><input type="checkbox" checked={picked.has(o.id)} onChange={() => togglePick(o.id)} /></td>
                    <td className="mono">{o.order_no}</td>
                    <td className="small">
                      {o.customer_name || "散客"}
                      {o.customer_level && <span className={`tag ${CUST_LEVEL_TONE[o.customer_level] ?? "tag-none"}`} style={{ marginLeft: 4 }} title="客户等级">{o.customer_level}</span>}
                    </td>
                    <td><b>{o.origin}</b> → <b>{o.destination}</b></td>
                    <td className="small">{BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type}{o.business_type === "hazmat" || o.is_hazardous ? <span className="tag tag-high" style={{ marginLeft: 4 }}>危</span> : ""}</td>
                    <td><span className={`tag tag-${o.priority === "vip" ? "high" : o.priority === "urgent" ? "medium" : "none"}`}>{PRIORITY_LABEL[o.priority]}</span></td>
                    <td className="num">{o.cargo_weight_ton}吨/{o.cargo_volume_cbm}方</td>
                    <td className="num">{o.quoted_amount ? fmtMoney(o.quoted_amount) : "—"}</td>
                    <td className="small">
                      {o.pooled_at && <span title="进池等待时长">{fmtRelative(o.pooled_at)}</span>}
                      {o.sla_status && o.sla_status !== "pending" && o.sla_status !== "on_time" && (
                        <span className={`tag tag-sla_${o.sla_status}`} style={{ marginLeft: 4 }}>{SLA_STATUS_LABEL[o.sla_status]}</span>
                      )}
                    </td>
                    <td className="small">{renderLock(o)}</td>
                    <td className="row-actions" onClick={(e) => e.stopPropagation()}>
                      {(o.lock_state === "free" || o.lock_state === "assigned_mine") && (
                        <button disabled={claim.isPending} onClick={() => claim.mutate(o.id)}>锁定</button>
                      )}
                      {o.lock_state === "mine" && (
                        <button disabled={release.isPending} onClick={() => release.mutate(o.id)}>释放</button>
                      )}
                      <button className="btn-primary" disabled={!canDispatch} title={canDispatch ? "" : "未分派/锁定给你，请由总调度分单或先锁定"} onClick={() => openWb(o)}>派单</button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          </div>
        )}
        {rows.length > 0 && (
          <div className="muted small" style={{ padding: "8px 17px", borderTop: "1px solid var(--line)" }}>
            提示：↑↓ 选单 · Enter 或双击拉出派单工作台 · 右键可认领 / 查看轨迹 / 提报异常。
          </div>
        )}
      </div>

      {/* 拼单配载沙盘（选中多单排线后展开） */}
      {plan && (
        <div className="panel">
          <div className="panel-head" style={{ display: "flex", justifyContent: "space-between", alignItems: "center", borderBottom: "1px solid var(--line)" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
              <span style={{ fontSize: 15, display: "flex", alignItems: "center", gap: 8 }}><IconTruck size={18} className="icon-offset"/> 拼单配载</span>
              {plan.estimated_total_saving && plan.estimated_total_saving > 0 && (
                <span className="tag tag-low" style={{ fontWeight: 700, display: "flex", alignItems: "center", gap: 4 }}>
                  <IconMoney size={14} className="icon-offset"/> 预计节省 {fmtMoney(plan.estimated_total_saving)}
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
            {/* 1. 拼单成功的车厢装载视图 */}
            <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(320px, 1fr))", gap: 20 }}>
              {plan.consolidated_trips?.map((trip, idx) => {
                const weightPct = Math.min(100, (trip.total_weight_ton / trip.vehicle.load_capacity_ton) * 100);
                const volPct = Math.min(100, (trip.total_volume_cbm / trip.vehicle.volume_capacity_cbm) * 100);

                return (
                  <div
                    key={idx}
                    onDragOver={(e) => {
                      e.preventDefault();
                      e.currentTarget.style.borderColor = "var(--accent)";
                      e.currentTarget.style.background = "var(--accent-weak)";
                    }}
                    onDragLeave={(e) => {
                      e.preventDefault();
                      e.currentTarget.style.borderColor = "var(--line)";
                      e.currentTarget.style.background = "var(--panel-2)";
                    }}
                    onDrop={(e) => {
                      e.preventDefault();
                      e.currentTarget.style.borderColor = "var(--line)";
                      e.currentTarget.style.background = "var(--panel-2)";
                      const orderId = e.dataTransfer.getData("text/plain");
                      if (orderId) handleDropOrderOnTrip(orderId, idx);
                    }}
                    style={{ border: "1px solid var(--line)", borderRadius: 10, background: "var(--panel-2)", padding: 16, display: "flex", flexDirection: "column", gap: 12, transition: "all 0.2s" }}
                  >
                    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <b style={{ fontSize: 15 }}>{trip.route}</b>
                      <span className="tag tag-low" style={{ fontWeight: 700 }}>拼单降本 -{fmtMoney(trip.money_saved)}</span>
                    </div>

                    <div className="muted small" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <span>卡车：<strong>{trip.vehicle.plate_no}</strong> (核载{trip.vehicle.load_capacity_ton}t / {trip.vehicle.volume_capacity_cbm}方)</span>
                      {trip.vehicle.compliance_ok === false && <span style={{ color: "var(--red)", display: "flex", alignItems: "center", gap: 4 }}><IconWarning size={14} className="icon-offset"/> 证件过期</span>}
                    </div>

                    {/* 可视化车厢装载图 */}
                    <div style={{ position: "relative", height: 100, background: "var(--bg)", borderRadius: 8, border: "1px solid var(--line-2)", overflow: "hidden", display: "flex", alignItems: "flex-end" }}>
                      <div style={{ position: "absolute", top: 0, right: 0, bottom: 0, width: 34, background: "var(--ink-2)", display: "flex", alignItems: "center", justifyContent: "center", color: "#fff", fontSize: 10, fontWeight: 700 }}>车头</div>
                      <div style={{ display: "flex", flex: 1, height: "100%", marginRight: 34, padding: "4px 0" }}>
                        {trip.orders.map((o, oIdx) => {
                          const orderPct = (o.weight_ton / trip.vehicle.load_capacity_ton) * 100;
                          const shades = ["var(--accent)", "var(--blue)", "var(--violet)", "var(--green)", "var(--amber)"];
                          const bgColor = shades[oIdx % shades.length];
                          return (
                            <div
                              key={o.order_id}
                              style={{ width: `${orderPct}%`, height: "100%", background: bgColor, opacity: 0.88, border: "1px solid rgba(255,255,255,0.2)", margin: "0 2px", borderRadius: 4, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", color: "#fff", fontSize: 10, overflow: "hidden", padding: "2px 4px", cursor: "pointer" }}
                              title={`订单: ${o.order_no} | 客户: ${o.customer_name} | 重量: ${o.weight_ton}t`}
                            >
                              <span style={{ fontWeight: 700 }}>{o.order_no.slice(-6)}</span>
                              <span>{o.weight_ton}t</span>
                            </div>
                          );
                        })}
                      </div>
                    </div>

                    <div style={{ display: "flex", flexDirection: "column", gap: 6, fontSize: 12 }}>
                      <div>
                        <div style={{ display: "flex", justifyContent: "space-between" }}>
                          <span>重量利用率:</span>
                          <b>{trip.total_weight_ton.toFixed(2)}t / {trip.vehicle.load_capacity_ton}t ({Math.round(weightPct)}%)</b>
                        </div>
                        <div style={{ width: "100%", height: 6, background: "var(--line)", borderRadius: 3, overflow: "hidden", marginTop: 4 }}>
                          <div style={{ width: `${weightPct}%`, height: "100%", background: "var(--accent)", borderRadius: 3 }} />
                        </div>
                      </div>
                      <div>
                        <div style={{ display: "flex", justifyContent: "space-between" }}>
                          <span>容积利用率:</span>
                          <b>{trip.total_volume_cbm.toFixed(2)}方 / {trip.vehicle.volume_capacity_cbm}方 ({Math.round(volPct)}%)</b>
                        </div>
                        <div style={{ width: "100%", height: 6, background: "var(--line)", borderRadius: 3, overflow: "hidden", marginTop: 4 }}>
                          <div style={{ width: `${volPct}%`, height: "100%", background: "var(--blue)", borderRadius: 3 }} />
                        </div>
                      </div>
                    </div>

                    <div style={{ background: "var(--panel-2)", padding: 10, borderRadius: 8, border: "1px solid var(--line)" }}>
                      <div style={{ fontSize: 11, fontWeight: 700, color: "var(--muted)", marginBottom: 6, display: "flex", alignItems: "center", gap: 6 }}>
                        <IconSparkles size={12} className="icon-offset"/> 承运商比价
                      </div>
                      <div style={{ display: "flex", justifyContent: "space-between", gap: 6, fontSize: 11, flexWrap: "wrap" }}>
                        <div className="tag tag-low" style={{ fontWeight: 700, display: "flex", alignItems: "center", gap: 4 }}>
                          <IconCheckCircle size={12} className="icon-offset"/> 自营配载：{fmtMoney(trip.consolidated_cost)}
                        </div>
                        <div style={{ background: "var(--panel-3)", padding: "3px 8px", borderRadius: 4, color: "var(--muted)" }}>市场行情：{fmtMoney(Math.round(trip.consolidated_cost * 1.05))}</div>
                      </div>
                    </div>

                    <div style={{ display: "flex", justifyContent: "space-between", borderTop: "1px solid var(--line)", paddingTop: 10, fontSize: 12 }} className="muted">
                      <span>分单单独派车：{fmtMoney(trip.separate_cost)}</span>
                      <span>合拼整车费：{fmtMoney(trip.consolidated_cost)}</span>
                    </div>
                  </div>
                );
              })}
            </div>

            {plan.unassigned && plan.unassigned.length > 0 && (
              <div style={{ border: "1px solid var(--red-line)", borderRadius: 8, background: "var(--red-weak)", padding: 14, color: "var(--red)", borderLeft: "3px solid var(--red)" }}>
                <b style={{ fontSize: 13, display: "flex", alignItems: "center", gap: 6 }}>
                  <IconWarning size={16} className="icon-offset"/> 以下 {plan.unassigned.length} 笔订单无适配自营运力，可拖拽至上方车辆手动配载：
                </b>
                <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 10 }}>
                  {plan.unassigned.map((u) => (
                    <span
                      key={u.order_id}
                      draggable={true}
                      onDragStart={(e) => { e.dataTransfer.setData("text/plain", u.order_id); }}
                      className="tag"
                      style={{ background: "var(--panel)", color: "var(--red)", border: "1px solid var(--red-line)", fontFamily: "var(--font-mono)", padding: "6px 12px", borderRadius: 6, cursor: "grab", display: "inline-flex", alignItems: "center", gap: 6 }}
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

      {/* 右键快捷菜单 */}
      {menu && (
        <ul
          className="ctx-menu"
          style={{ top: menu.y, left: menu.x }}
          onClick={(e) => e.stopPropagation()}
        >
          <li onClick={() => { openWb(menu.order, "dispatch"); setMenu(null); }}><IconZap size={14} className="icon-offset" /> 精准派单</li>
          {menu.order.status !== "dispatching"
            ? <li onClick={() => { claim.mutate(menu.order.id); setMenu(null); }}>认领订单</li>
            : <li onClick={() => { release.mutate(menu.order.id); setMenu(null); }}>退回订单池</li>}
          <li onClick={() => { openWb(menu.order, "track"); setMenu(null); }}><IconMapPin size={14} className="icon-offset" /> 查看轨迹</li>
          <li onClick={() => { openWb(menu.order, "exception"); setMenu(null); }}><IconGitBranch size={14} className="icon-offset" /> 提报异常</li>
        </ul>
      )}

      {/* 派单工作台抽屉 */}
      {active && (
        <div className="wb-overlay" onClick={closeWb}>
          <aside className="wb-drawer" onClick={(e) => e.stopPropagation()}>
            <div className="wb-drawer-head">
              <div>
                <div style={{ fontSize: 15, fontWeight: 650 }}>派单工作台</div>
                <div className="mono small muted">{active.order_no} · {active.origin} → {active.destination}</div>
              </div>
              <button className="btn-ghost" onClick={closeWb} aria-label="关闭" style={{ padding: "6px 8px" }}><IconX size={16} /></button>
            </div>

            <div className="wb-tabs">
              <button className={tab === "dispatch" ? "active" : ""} onClick={() => { setTab("dispatch"); if (!suggestion && !suggest.isPending) suggest.mutate(active.id); }}>派单</button>
              <button className={tab === "track" ? "active" : ""} onClick={() => setTab("track")}>轨迹</button>
              <button className={tab === "exception" ? "active" : ""} onClick={() => setTab("exception")}>提报异常</button>
            </div>

            <div className="wb-drawer-body">
              {activeGone && (
                <div className="tag tag-medium" style={{ display: "flex", alignItems: "center", gap: 4, marginBottom: 12 }}>
                  <IconAlert size={14} className="icon-offset"/> 订单 {active.order_no} 已被他人处理或离开订单池
                </div>
              )}

              {/* ── 派单 tab ── */}
              {tab === "dispatch" && (
                suggest.isPending ? (
                  <div className="muted" style={{ padding: 40, textAlign: "center", display: "flex", flexDirection: "column", alignItems: "center", gap: 12 }}>
                    <IconSearch size={24} className="icon-offset" /><span>测算运力与比价中…</span>
                  </div>
                ) : suggestion ? (
                  <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
                    {suggestion.external_signals.length > 0 && (
                      <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                        {suggestion.external_signals.map((s, i) => (
                          <div key={i} className={`tag tag-${s.level === "high" ? "high" : "medium"}`} style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
                            <IconAlert size={12} className="icon-offset"/> {s.note}
                          </div>
                        ))}
                      </div>
                    )}

                    <div style={{ background: "var(--accent-weak)", border: "1px solid var(--accent-weak-2)", borderRadius: 10, padding: 16 }}>
                      {/* 承运商推荐结论：可执行建议 + 风险说明 + 人工确认 */}
                      {suggestion.recommendation ? (
                        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                            <span className="muted" style={{ fontSize: 11 }}>首选承运商</span>
                            <strong style={{ fontSize: 15, color: "var(--ink)" }}>{suggestion.recommendation.carrier}</strong>
                            <span className={`tag tag-${RISK_TAG[suggestion.recommendation.risk_level] ?? "none"}`}>{suggestion.recommendation.label}</span>
                            {suggestion.recommendation.needs_approval && <span className="tag tag-high">需主管确认</span>}
                          </div>
                          {suggestion.recommendation.suggested_price_band && (
                            <div style={{ fontSize: 13 }}>建议成交价：
                              <strong style={{ color: "var(--accent)" }}>{fmtMoney(suggestion.recommendation.suggested_price_band[0])} ~ {fmtMoney(suggestion.recommendation.suggested_price_band[1])}</strong>
                            </div>
                          )}
                          {suggestion.recommendation.reasons.length > 0 && (
                            <ul style={{ margin: 0, paddingLeft: 18, fontSize: 12.5, color: "var(--ink-2)", lineHeight: 1.7 }}>
                              {suggestion.recommendation.reasons.map((r, i) => <li key={i}>{r}</li>)}
                            </ul>
                          )}
                          {suggestion.recommendation.risk_notes.length > 0 && (
                            <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                              {suggestion.recommendation.risk_notes.map((n, i) => <span key={i} className="tag tag-medium">{n}</span>)}
                            </div>
                          )}
                        </div>
                      ) : (
                        <div className="muted" style={{ fontSize: 13 }}>暂无合适承运商，可走网货平台兜底或手动指派自营车。</div>
                      )}

                      {/* 承运商比选（找合适的，不是找最便宜的） */}
                      {suggestion.carrier_recommendations.length > 0 && (
                        <div style={{ marginTop: 14, overflowX: "auto" }}>
                          <table className="table" style={{ width: "100%", fontSize: 12.5 }}>
                            <thead><tr>
                              <th>承运商</th><th className="num">最近成交</th><th className="num">本次报价</th>
                              <th className="num">准班</th><th className="num">异常</th><th className="num">回单及时</th><th>评价</th><th></th>
                            </tr></thead>
                            <tbody>
                              {suggestion.carrier_recommendations.map((r) => (
                                <tr key={r.carrier_id} className={carrierId === r.carrier_id ? "row-sel" : ""}>
                                  <td>{r.carrier} <span className="muted small">{r.carrier_grade}</span></td>
                                  <td className="num">{r.recent_deal_price ? fmtMoney(r.recent_deal_price) : "—"}</td>
                                  <td className="num">{r.quote != null ? fmtMoney(r.quote) : "—"}</td>
                                  <td className="num">{r.deals ? `${Math.round(r.on_time_rate * 100)}%` : "—"}</td>
                                  <td className="num">{r.deals ? `${Math.round(r.exception_rate * 100)}%` : "—"}</td>
                                  <td className="num">{r.deals ? `${Math.round(r.receipt_timely_rate * 100)}%` : "—"}</td>
                                  <td><span className={`tag tag-${RISK_TAG[r.risk_level] ?? "none"}`}>{r.label}</span></td>
                                  <td><button className="btn-ghost" style={{ padding: "2px 8px", fontSize: 12 }} onClick={() => pickCarrier(r.carrier_id)}>选</button></td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      )}

                      {suggestion.ymm_quote && (
                        <div style={{ borderTop: "1px dashed var(--line-2)", paddingTop: 10, marginTop: 12, fontSize: 13 }}>
                          <span className="muted" style={{ fontSize: 11, marginRight: 8 }}>市场运价参比</span>
                          <strong style={{ color: "var(--accent)" }}>
                            {suggestion.ymm_quote.avg != null ? `¥${suggestion.ymm_quote.low} ~ ¥${suggestion.ymm_quote.high}（中枢 ¥${suggestion.ymm_quote.avg}）` : "—"}
                          </strong>
                          <span className="muted small" style={{ marginLeft: 8 }}>{suggestion.ymm_quote.note}</span>
                        </div>
                      )}
                      <div style={{ background: "var(--panel)", padding: "8px 12px", borderRadius: 6, display: "flex", justifyContent: "space-between", alignItems: "center", border: "1px solid var(--line)", marginTop: 12, fontSize: 13 }}>
                        <span>建议委派方式：</span>
                        <strong style={{ color: "var(--accent)" }}>{DISPATCH_TYPE_LABEL[suggestion.suggested_dispatch_type]}</strong>
                      </div>
                      <div style={{ display: "flex", gap: 8, marginTop: 14 }}>
                        <button
                          className="btn-primary"
                          style={{ flex: 1.6, display: "flex", alignItems: "center", justifyContent: "center", gap: 6 }}
                          disabled={!canOneClick || quickDispatch.isPending || activeGone}
                          onClick={oneClickDispatch}
                        >
                          <IconZap size={14} className="icon-offset"/> {quickDispatch.isPending ? "派单中…" : "一键派单"}
                        </button>
                        <button
                          className="btn-ghost"
                          style={{ flex: 1 }}
                          disabled={!canOneClick}
                          onClick={adopt}
                          title="仅填入下方指派表单，便于手工微调"
                        >
                          采纳并微调
                        </button>
                      </div>
                    </div>

                    {suggestion.vehicle_candidates.length > 0 && (
                      <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                        <span className="muted small" style={{ width: "100%", fontWeight: 700 }}>自营车兜底运力（仅特殊场景使用）：</span>
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

                    <div style={{ borderTop: "1px solid var(--line)", paddingTop: 16, display: "flex", flexDirection: "column", gap: 10 }}>
                      <span className="muted small" style={{ fontWeight: 700 }}>指派</span>
                      <div className="grid-form" style={{ gridTemplateColumns: "1fr" }}>
                        <label>
                          委派方式
                          <select value={dispatchType} onChange={(e) => setDispatchType(e.target.value)}>
                            {Object.entries(DISPATCH_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                          </select>
                        </label>
                        <label>
                          议定应付（元）
                          <input value={agreedPayable} onChange={(e) => setAgreedPayable(e.target.value)} placeholder="与承运商议定的运费，落对账快照" />
                        </label>
                        {dispatchType === "platform" ? (
                          <>
                            <label>
                              网货平台
                              <input value={platformName} onChange={(e) => setPlatformName(e.target.value)} placeholder="如 满帮 / 路歌" />
                            </label>
                            <label>
                              平台单号（可选）
                              <input value={platformOrderNo} onChange={(e) => setPlatformOrderNo(e.target.value)} placeholder="平台侧运单号" />
                            </label>
                          </>
                        ) : dispatchType === "third_party" ? (
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
                          disabled={dispatch.isPending || activeGone || (dispatchType === "platform" ? !platformName : dispatchType === "third_party" ? !carrierId : !vehicleId)}
                          onClick={() => dispatch.mutate(active.id)}
                        >
                          {dispatch.isPending ? "派单中…" : "确认派单"}
                        </button>
                        {(dispatchType === "platform" ? !platformName : dispatchType === "third_party" ? !carrierId : !vehicleId) && (
                          <div className="muted small">请先{dispatchType === "platform" ? "填写网货平台" : `选择${dispatchType === "third_party" ? "承运商" : "车辆"}`}再派单</div>
                        )}
                      </div>
                    </div>
                  </div>
                ) : (
                  <div className="muted small" style={{ padding: 24, textAlign: "center" }}>点击「派单」测算运力与比价建议</div>
                )
              )}

              {/* ── 轨迹 tab ── */}
              {tab === "track" && (
                !trackNo ? (
                  <div className="muted" style={{ padding: 32, textAlign: "center", display: "flex", flexDirection: "column", alignItems: "center", gap: 10 }}>
                    <IconMapPin size={28} style={{ opacity: 0.3 }} />
                    <div>订单尚未派单，暂无在途轨迹。</div>
                    <div className="small">计划线路：{active.origin} → {active.destination}</div>
                  </div>
                ) : traj.isLoading ? (
                  <div className="muted" style={{ padding: 24, textAlign: "center" }}>加载轨迹数据…</div>
                ) : traj.data && traj.data.points?.length ? (
                  <div className="stack" style={{ gap: 10 }}>
                    <div className="mono small muted">运单 {trackNo}</div>
                    <TrajectoryMap traj={traj.data} />
                  </div>
                ) : (
                  <div className="muted small" style={{ padding: 24, textAlign: "center" }}>运单 {trackNo} 暂无轨迹点。</div>
                )
              )}

              {/* ── 提报异常 tab ── */}
              {tab === "exception" && (
                <div className="grid-form" style={{ gridTemplateColumns: "1fr", gap: 12 }}>
                  <label>
                    异常类型
                    <select value={excType} onChange={(e) => setExcType(e.target.value)}>
                      {MANUAL_EXC_TYPES.map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                    </select>
                  </label>
                  <label>
                    紧急程度
                    <select value={excLevel} onChange={(e) => setExcLevel(e.target.value)}>
                      {EXC_LEVELS.map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                    </select>
                  </label>
                  <label>
                    情况描述
                    <textarea
                      value={excDesc}
                      onChange={(e) => setExcDesc(e.target.value)}
                      rows={4}
                      placeholder="请描述异常情况（时间、地点、货物、责任方等）"
                      style={{ padding: "8px 10px", border: "1px solid var(--line-2)", borderRadius: "var(--radius-sm)", fontSize: 13, resize: "vertical" }}
                    />
                  </label>
                  <button className="btn-primary" disabled={!excDesc.trim() || reportException.isPending} onClick={() => reportException.mutate()}>
                    {reportException.isPending ? "提报中…" : "提报异常"}
                  </button>
                  <div className="muted small">提报后进入异常处置流程，可在「异常处置」中跟进指派与关闭。</div>
                </div>
              )}
            </div>
          </aside>
        </div>
      )}

      {/* 异常处置（原独立页并入）：调度对在途异常认领·AI诊断·强制闭环 */}
      <ExceptionQueue />
    </div>
  );
}
