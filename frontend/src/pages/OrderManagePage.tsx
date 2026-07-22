import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { apiDownload, apiGet, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { fmtDateTime, fmtMoney, fmtRelative } from "../api/format";
import { toast } from "../api/toast";
import type { DispatchBatch, DispatchBatchDetail, Order, OrderEvent, Paginated } from "../api/types";
import {
  BATCH_STATUS_LABEL, BUSINESS_TYPE_LABEL, ORDER_CHANNEL_LABEL, ORDER_EVENT_LABEL, ORDER_STATUS_LABEL, PRIORITY_LABEL, SETTLEMENT_LABEL, SLA_STATUS_LABEL, SOURCE_TYPE_LABEL,
} from "../api/types";
import { DataTable, type DataColumn } from "../components/DataTable";
import { CopyCode } from "../components/CopyCode";
import { ExceptionRegisterModal } from "../components/ExceptionRegisterModal";
import { FilterBuilder, activeConditionCount, describeCondition, EMPTY_MODEL, type FilterFieldDef, type FilterModel } from "../components/FilterBuilder";
import { useModalA11y } from "../api/useModalA11y";
import { useServerTable } from "../api/useServerTable";
import { StateView } from "../components/StateView";
import { StatusTag } from "../components/StatusTag";
import { WaybillsPage } from "./WaybillsPage";

const LEVEL_TONE: Record<string, string> = { S: "tag-info", A: "tag-low", B: "tag-info", C: "tag-medium", D: "tag-none" };
const LOCK_LABEL: Record<string, string> = { mine: "我锁定", locked: "他人锁定", assigned_mine: "分派给我", assigned_other: "已分派", free: "" };
const LOCK_TONE: Record<string, string> = { mine: "tag-low", locked: "tag-medium", assigned_mine: "tag-info", assigned_other: "tag-none", free: "" };

const enumOpts = (rec: Record<string, string>) => Object.entries(rec).map(([value, label]) => ({ value, label }));
// 订单高级筛选字段（文本/枚举/数值/日期）
const ORDER_FILTER_FIELDS: FilterFieldDef[] = [
  { key: "order_no", label: "订单号", type: "text", accessor: (o) => (o as Order).order_no },
  { key: "customer", label: "客户", type: "text", accessor: (o) => (o as Order).customer_name || "" },
  { key: "route", label: "线路", type: "text", accessor: (o) => `${(o as Order).origin || ""}→${(o as Order).destination || ""}` },
  { key: "creator", label: "建单人", type: "text", accessor: (o) => (o as Order).created_by_name || "" },
  { key: "status", label: "订单状态", type: "enum", options: enumOpts(ORDER_STATUS_LABEL), accessor: (o) => (o as Order).status },
  { key: "channel", label: "渠道", type: "enum", options: enumOpts(ORDER_CHANNEL_LABEL), accessor: (o) => (o as Order).channel },
  { key: "business_type", label: "业务类型", type: "enum", options: enumOpts(BUSINESS_TYPE_LABEL), accessor: (o) => (o as Order).business_type },
  { key: "priority", label: "优先级", type: "enum", options: enumOpts(PRIORITY_LABEL), accessor: (o) => (o as Order).priority },
  { key: "settlement", label: "结算方式", type: "enum", options: enumOpts(SETTLEMENT_LABEL), accessor: (o) => (o as Order).settlement_type },
  { key: "level", label: "客户等级", type: "enum", options: ["S", "A", "B", "C", "D"].map((v) => ({ value: v, label: `${v} 级` })), accessor: (o) => (o as Order).customer_level || "" },
  { key: "sla", label: "SLA", type: "enum", options: enumOpts(SLA_STATUS_LABEL), accessor: (o) => (o as Order).sla_status },
  { key: "exception", label: "异常", type: "enum", options: [{ value: "1", label: "有异常" }, { value: "0", label: "无异常" }], accessor: (o) => ((o as Order).exception_count ?? 0) > 0 ? "1" : "0" },
  { key: "amount", label: "报价(元)", type: "number", accessor: (o) => Number((o as Order).quoted_amount) || 0 },
  { key: "weight", label: "货量(吨)", type: "number", accessor: (o) => Number((o as Order).cargo_weight_ton) || 0 },
  { key: "created_at", label: "建单时间", type: "date", accessor: (o) => (o as Order).created_at },
];

// ── 订单视图（护城河主视图）──────────────────────────────
function OrdersTab() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [model, setModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showBuilder, setShowBuilder] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [excOrder, setExcOrder] = useState<Order | null>(null);

  // 保存的筛选视图（localStorage：搜索词 + 高级条件模型）
  type Preset = { name: string; search: string; model: FilterModel };
  const PRESET_KEY = "om-order-presets-v3";
  const [presets, setPresets] = useState<Preset[]>(() => {
    try { return JSON.parse(localStorage.getItem(PRESET_KEY) || "[]"); } catch { return []; }
  });
  const persistPresets = (next: Preset[]) => { setPresets(next); localStorage.setItem(PRESET_KEY, JSON.stringify(next)); };
  const applyPreset = (p: Preset) => { setSearch(p.search); setModel(p.model ?? EMPTY_MODEL); };
  const savePreset = () => {
    const name = window.prompt("为当前筛选视图命名：", "")?.trim();
    if (!name) return;
    persistPresets([...presets.filter((x) => x.name !== name), { name, search, model }]);
    toast.success(`已保存筛选视图「${name}」`);
  };
  const activeCount = activeConditionCount(model, ORDER_FILTER_FIELDS);
  const anyFilter = Boolean(search) || activeCount > 0;
  const resetFilters = () => { setSearch(""); setModel(EMPTY_MODEL); };
  // 一键状态筛选（stat 卡片 / 快捷）：替换为单条状态条件
  const quickStatus = (st: string) => setModel((m) => {
    const others = m.conditions.filter((c) => c.field !== "status");
    const has = m.conditions.some((c) => c.field === "status" && Array.isArray(c.value) && (c.value as string[]).includes(st) && (c.value as string[]).length === 1);
    return has ? { ...m, conditions: others } : { combinator: m.combinator, conditions: [...others, { id: `st${Date.now()}`, field: "status", op: "in", value: [st] }] };
  });
  const statusActive = (st: string) => model.conditions.some((c) => c.field === "status" && Array.isArray(c.value) && (c.value as string[]).length === 1 && (c.value as string[])[0] === st);

  // 服务端筛选 + 分页 + 排序（高级筛选/搜索/排序全部下沉后端，对全量生效）
  const st = useServerTable<Order>({
    queryKey: ["orders-manage"],
    path: "/orders",
    pageSize: 50,
    defaultSort: { field: "created_at", dir: "desc" },
    model,
    search,
  });
  const rows = st.rows;
  const total = st.total;
  // 台账概览：用 funnel 聚合（服务端全量计数，不受分页影响）
  const funnel = useQuery({
    queryKey: ["orders", "funnel"],
    queryFn: () => apiGet<{ by_status: Record<string, number>; today_created: number; total: number }>("/orders/funnel"),
    refetchInterval: 30000,
  });
  const dispatchers = useQuery({
    queryKey: ["dispatchers"],
    queryFn: () => apiGet<{ is_chief: boolean; dispatchers: Array<{ id: string; name: string }> }>("/orders/dispatchers"),
  });
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["orders-manage"] });
    queryClient.invalidateQueries({ queryKey: ["orders", "funnel"] });
  };

  const BATCH_LABEL: Record<string, string> = { confirm: "确认", pool: "进池", cancel: "取消", delete: "删除" };
  const batch = useMutation({
    mutationFn: (v: { action: string; ids: string[] }) =>
      apiPost<{ ok_count: number; failed: Array<{ order_no: string; error: string }> }>("/orders/batch", v),
    onSuccess: (r, v) => {
      toast.success(`批量${BATCH_LABEL[v.action]}完成：成功 ${r.ok_count}${r.failed?.length ? ` · 失败 ${r.failed.length}` : ""}`);
      setSelected(new Set()); invalidate();
    },
  });
  const merge = useMutation({
    mutationFn: (ids: string[]) => apiPost<Order>("/orders/merge", { ids }),
    onSuccess: (o) => { toast.success(`已合单：${o.order_no}`); setSelected(new Set()); invalidate(); },
  });
  const [assignTo, setAssignTo] = useState("");
  const [drawer, setDrawer] = useState<Order | null>(null);
  const assign = useMutation({
    mutationFn: (v: { ids: string[]; dispatcher: string }) => apiPost<{ assigned: string[]; dispatcher: string }>("/orders/assign", v),
    onSuccess: (r) => { toast.success(`已分派 ${r.assigned.length} 单给 ${r.dispatcher}`); setSelected(new Set()); setAssignTo(""); invalidate(); },
    onError: (e: Error) => toast.error(e.message),
  });
  // 批量改字段（优先级 / 结算方式）
  const batchUpdate = useMutation({
    mutationFn: (v: { field: string; value: string; ids: string[] }) =>
      apiPost<{ ok_count: number; failed: Array<{ order_no: string; error: string }> }>("/orders/batch-update", v),
    onSuccess: (r, v) => {
      const fl = v.field === "priority" ? PRIORITY_LABEL[v.value] : SETTLEMENT_LABEL[v.value];
      toast.success(`批量改为「${fl}」：成功 ${r.ok_count}${r.failed?.length ? ` · 跳过 ${r.failed.length}` : ""}`);
      setSelected(new Set()); invalidate();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const timeline = useQuery({
    queryKey: ["order-timeline", drawer?.id],
    queryFn: () => apiGet<OrderEvent[]>(`/orders/${drawer!.id}/timeline`),
    enabled: Boolean(drawer),
  });

  const drawerRef = useRef<HTMLDivElement>(null);
  useModalA11y(Boolean(drawer), drawerRef, () => setDrawer(null));

  // 台账概览（funnel 服务端聚合，全量计数，不受分页/筛选影响）
  const stats = useMemo(() => {
    const bs = funnel.data?.by_status ?? {};
    return {
      total: funnel.data?.total ?? 0,
      pending: (bs.draft ?? 0) + (bs.pending_confirm ?? 0),
      pooled: (bs.pooled ?? 0) + (bs.dispatching ?? 0),
      dispatched: (bs.converted ?? 0) + (bs.completed ?? 0),
      today: funnel.data?.today_created ?? 0,
    };
  }, [funnel.data]);

  const runBatch = async (action: string) => {
    const ids = [...selected];
    if (!ids.length) return;
    if (action === "cancel" || action === "delete") {
      if (!(await confirmAction({ message: `确定批量${BATCH_LABEL[action]} ${ids.length} 个订单？不可恢复。`, tone: "danger", confirmText: `批量${BATCH_LABEL[action]}` }))) return;
    }
    batch.mutate({ action, ids });
  };

  const toggle = (id: string) => setSelected((s) => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n; });
  const toggleAll = () => setSelected((prev) => (rows.length > 0 && rows.every((o) => prev.has(o.id)) ? new Set() : new Set(rows.map((o) => o.id))));

  const columns: DataColumn<Order>[] = [
    { key: "order_no", header: "订单号 (DD)", width: 170, alwaysVisible: true, sortField: "order_no", sortValue: (o) => o.order_no, exportValue: (o) => o.order_no, render: (o) => <Link className="link mono doc-order" to={`/orders/${o.id}`} title="订单">{o.order_no}</Link> },
    { key: "customer", header: "客户", width: 160, filterable: true, filterValue: (o) => o.customer_name || "散客", sortField: "customer__name", sortValue: (o) => o.customer_name || "", exportValue: (o) => o.customer_name || "散客", render: (o) => <span>{o.customer_name || "散客"}{o.customer_level && <span className={`tag ${LEVEL_TONE[o.customer_level] ?? "tag-none"}`} style={{ marginLeft: 4 }}>{o.customer_level}</span>}{(o.exception_count ?? 0) > 0 && <span className={`tag tag-${o.exception_level === "high" ? "high" : o.exception_level === "low" ? "low" : "medium"}`} style={{ marginLeft: 4 }} title="未闭环异常">⚠{(o.exception_count ?? 0) > 1 ? o.exception_count : ""}</span>}</span> },
    { key: "channel", header: "渠道", width: 90, filterable: true, filterValue: (o) => ORDER_CHANNEL_LABEL[o.channel] ?? o.channel, sortField: "channel", sortValue: (o) => o.channel, exportValue: (o) => ORDER_CHANNEL_LABEL[o.channel] ?? o.channel, render: (o) => <span className="small">{ORDER_CHANNEL_LABEL[o.channel] ?? o.channel}</span> },
    { key: "route", header: "线路", width: 150, sortValue: (o) => `${o.origin}${o.destination}`, exportValue: (o) => `${o.origin || "?"}→${o.destination || "?"}`, render: (o) => <><b>{o.origin || "?"}</b> → <b>{o.destination || "?"}</b></> },
    { key: "biz", header: "业务", width: 90, filterable: true, filterValue: (o) => BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type, sortField: "business_type", sortValue: (o) => o.business_type, exportValue: (o) => BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type, render: (o) => <span className="small">{BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type}{o.business_type === "hazmat" || o.is_hazardous ? <span className="tag tag-high" style={{ marginLeft: 4 }}>危</span> : ""}</span> },
    { key: "cargo", header: "货量", width: 110, align: "right", sortField: "cargo_weight_ton", sortValue: (o) => Number(o.cargo_weight_ton) || 0, exportValue: (o) => `${o.cargo_weight_ton}吨/${o.cargo_quantity}件`, render: (o) => <span className="num">{o.cargo_weight_ton}吨/{o.cargo_quantity}件</span> },
    { key: "amount", header: "报价", width: 110, align: "right", sortField: "quoted_amount", sortValue: (o) => Number(o.quoted_amount) || 0, exportValue: (o) => Number(o.quoted_amount) || 0, render: (o) => <span className="num">{Number(o.quoted_amount) > 0 ? fmtMoney(o.quoted_amount) : "—"}</span> },
    { key: "priority", header: "优先级", width: 92, filterable: true, filterValue: (o) => PRIORITY_LABEL[o.priority] ?? o.priority, sortField: "priority", sortValue: (o) => o.priority, exportValue: (o) => PRIORITY_LABEL[o.priority] ?? o.priority, render: (o) => <span className={`tag tag-${o.priority === "vip" ? "high" : o.priority === "urgent" ? "medium" : "none"}`}>{PRIORITY_LABEL[o.priority]}</span> },
    { key: "status", header: "订单状态", width: 100, filterable: true, filterValue: (o) => ORDER_STATUS_LABEL[o.status] ?? o.status, sortField: "status", sortValue: (o) => o.status, exportValue: (o) => ORDER_STATUS_LABEL[o.status] ?? o.status, render: (o) => <StatusTag kind="order" value={o.status} /> },
    { key: "sla", header: "SLA", width: 84, filterable: true, filterValue: (o) => SLA_STATUS_LABEL[o.sla_status] ?? o.sla_status, sortField: "sla_status", sortValue: (o) => o.sla_status, exportValue: (o) => SLA_STATUS_LABEL[o.sla_status] ?? o.sla_status, render: (o) => <StatusTag kind="sla" value={o.sla_status} /> },
    { key: "waybill", header: "关联运单 (YD)", width: 150, sortValue: (o) => (o.waybill_nos ?? []).length, exportValue: (o) => (o.waybill_nos ?? []).join(" "), render: (o) => (o.waybill_nos ?? []).length > 0 ? <span style={{ display: "inline-flex", alignItems: "center", gap: 3, overflow: "hidden" }}>{o.waybill_nos.slice(0, 1).map((no) => <Link key={no} className="doc-waybill mono small" to={`/waybills/${no}`} title="运单">{no}</Link>)}{o.waybill_nos.length > 1 && <span className="tag tag-none small" title={o.waybill_nos.join("、")}>+{o.waybill_nos.length - 1}</span>}</span> : <span className="muted small">未生成</span> },
    { key: "creator", header: "建单人", width: 100, filterable: true, filterValue: (o) => o.created_by_name || "-", sortValue: (o) => o.created_by_name || "", exportValue: (o) => o.created_by_name || "", render: (o) => <span className="small muted">{o.created_by_name || "-"}</span> },
    { key: "created", header: "建单时间", width: 130, sortField: "created_at", sortValue: (o) => o.created_at, exportValue: (o) => fmtDateTime(o.created_at), render: (o) => <span className="small" title={fmtDateTime(o.created_at)}>{fmtRelative(o.created_at)}</span> },
    {
      key: "actions", header: "操作", width: 150, alwaysVisible: true, sticky: "right",
      render: (o) => (
        <div className="row-actions" onClick={(e) => e.stopPropagation()}>
          {(o.status === "draft" || o.status === "pending_confirm") && <button disabled={batch.isPending} onClick={() => batch.mutate({ action: "confirm", ids: [o.id] })}>确认</button>}
          {(o.status === "confirmed" || o.status === "pending_confirm") && <button disabled={batch.isPending} onClick={() => batch.mutate({ action: "pool", ids: [o.id] })}>进池</button>}
          {o.status === "pooled" && <Link className="link small" to="/dispatch-board">去派单</Link>}
          <Link className="link small" to={`/orders/${o.id}`}>详情</Link>
        </div>
      ),
    },
  ];

  // 行右键菜单：批量/单条常用动作直达
  const rowMenu = (o: Order): { label: string; onClick: () => void; danger?: boolean; disabled?: boolean }[] => [
    { label: "查看详情", onClick: () => setDrawer(o) },
    ...((o.status === "draft" || o.status === "pending_confirm") ? [{ label: "确认订单", onClick: () => batch.mutate({ action: "confirm", ids: [o.id] }) }] : []),
    ...((o.status === "confirmed" || o.status === "pending_confirm") ? [{ label: "进池", onClick: () => batch.mutate({ action: "pool", ids: [o.id] }) }] : []),
    { label: "登记异常", onClick: () => setExcOrder(o) },
    { label: "完整详情页", onClick: () => { window.location.href = `/orders/${o.id}`; } },
    { label: "取消订单", danger: true, disabled: o.status === "cancelled" || o.status === "converted", onClick: async () => { if (await confirmAction({ message: `取消订单 ${o.order_no}？`, tone: "danger", confirmText: "取消订单" })) batch.mutate({ action: "cancel", ids: [o.id] }); } },
  ];

  const batchBar = selected.size > 0 ? (
    <div className="batch-bar">
      <span>已选 <b style={{ color: "var(--accent)" }}>{selected.size}</b> 单</span>
      <div style={{ flex: 1 }} />
      <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("confirm")}>确认</button>
      <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("pool")}>进池</button>
      <button className="btn-ghost" disabled={merge.isPending || selected.size < 2} onClick={async () => { if (await confirmAction({ message: `将 ${selected.size} 张订单合并为一张？原单作废。`, confirmText: "合单" })) merge.mutate([...selected]); }}>合单</button>
      <select className="search" style={{ minWidth: 120, padding: "6px 10px" }} value="" disabled={batchUpdate.isPending} onChange={(e) => { if (e.target.value) batchUpdate.mutate({ field: "priority", value: e.target.value, ids: [...selected] }); e.target.value = ""; }}>
        <option value="">改优先级…</option>
        {Object.entries(PRIORITY_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
      </select>
      <select className="search" style={{ minWidth: 120, padding: "6px 10px" }} value="" disabled={batchUpdate.isPending} onChange={(e) => { if (e.target.value) batchUpdate.mutate({ field: "settlement_type", value: e.target.value, ids: [...selected] }); e.target.value = ""; }}>
        <option value="">改结算…</option>
        {Object.entries(SETTLEMENT_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
      </select>
      {dispatchers.data?.is_chief && (
        <span style={{ display: "inline-flex", gap: 6, alignItems: "center" }}>
          <select className="search" style={{ minWidth: 130, padding: "6px 10px" }} value={assignTo} onChange={(e) => setAssignTo(e.target.value)}>
            <option value="">分派给…</option>
            {(dispatchers.data?.dispatchers ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
          </select>
          <button className="btn-ghost" disabled={!assignTo || assign.isPending} onClick={() => assign.mutate({ ids: [...selected], dispatcher: assignTo })} title={!assignTo ? "请先选择调度员" : "分给所选调度员"}>分单</button>
        </span>
      )}
      <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("cancel")}>取消</button>
      <button className="btn-danger-ghost" disabled={batch.isPending} onClick={() => runBatch("delete")}>删除</button>
      <button className="btn-ghost" onClick={() => setSelected(new Set())}>清除</button>
    </div>
  ) : null;

  return (
    <>
    {/* 台账概览（超紧凑单行）+ 快捷状态筛选 */}
    <div className="om-stats om-stats-slim">
      <div className="om-stat"><div className="om-stat-n">{stats.total}</div><div className="om-stat-l">订单总数</div></div>
      <button className={`om-stat om-clickable${statusActive("pending_confirm") ? " on" : ""}`} onClick={() => quickStatus("pending_confirm")}><div className="om-stat-n" style={{ color: stats.pending ? "var(--amber)" : undefined }}>{stats.pending}</div><div className="om-stat-l">待确认 →</div></button>
      <button className={`om-stat om-clickable${statusActive("pooled") ? " on" : ""}`} onClick={() => quickStatus("pooled")}><div className="om-stat-n" style={{ color: stats.pooled ? "var(--blue)" : undefined }}>{stats.pooled}</div><div className="om-stat-l">池中待派 →</div></button>
      <div className="om-stat"><div className="om-stat-n" style={{ color: stats.dispatched ? "var(--green)" : undefined }}>{stats.dispatched}</div><div className="om-stat-l">已派/完成</div></div>
      <div className="om-stat"><div className="om-stat-n">{stats.today}</div><div className="om-stat-l">今日新建</div></div>
    </div>

    <div className="panel om-panel">
      {/* 活动筛选条件 chips（在工具条上方，仅有条件时出现） */}
      {activeCount > 0 && (
        <div className="om-chips">
          <span className="muted small">条件（{model.combinator === "and" ? "全部满足" : "任一满足"}）：</span>
          {model.conditions.map((c) => {
            const label = describeCondition(c, ORDER_FILTER_FIELDS);
            if (!label) return null;
            return <span key={c.id} className="filter-chip">{label}<button onClick={() => setModel((m) => ({ ...m, conditions: m.conditions.filter((x) => x.id !== c.id) }))}>×</button></span>;
          })}
          <button className="linkish small" onClick={() => setModel(EMPTY_MODEL)}>清空条件</button>
        </div>
      )}

      {st.isError ? (
        <StateView kind="error" onRetry={() => st.refetch()} />
      ) : (
        <DataTable<Order>
          columns={columns} rows={rows} rowKey={(o) => o.id} viewKey="order-manage-v2" exportName="订单台账"
          selectable selected={selected} onToggle={toggle} onToggleAll={toggleAll} stickyFirst batchBar={batchBar}
          onRowClick={(o) => setDrawer(o)} rowMenu={rowMenu}
          hideExport server={st.server} fill
          emptyState={anyFilter
            ? <StateView kind="empty" title="没有匹配的订单" hint="调整筛选条件再试。" />
            : <StateView kind="empty" scene="cs-empty" />}
          toolbarLeft={
            <>
              <span className="om-title" style={{ marginRight: 2 }}>订单台账<span className="ai-pill">{total}</span></span>
              <input className="search" style={{ minWidth: 180, flex: 1, maxWidth: 300 }} placeholder="搜索 订单号 / 客户 / 电话 / 线路" value={search} onChange={(e) => setSearch(e.target.value)} />
              <div style={{ position: "relative" }}>
                <button className={`btn-ghost${activeCount > 0 || showBuilder ? " on-accent" : ""}`} onClick={(e) => { e.stopPropagation(); setShowBuilder((v) => !v); }}>
                  <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 5h18l-7 8v5l-4 2v-7z" /></svg>
                    高级筛选{activeCount > 0 ? ` · ${activeCount}` : ""}
                  </span>
                </button>
                {showBuilder && <FilterBuilder fields={ORDER_FILTER_FIELDS} model={model} onChange={setModel} onClose={() => setShowBuilder(false)} />}
              </div>
              {presets.length > 0 && (
                <select className="search" style={{ maxWidth: 130, minWidth: 96 }} value="" onChange={(e) => { const p = presets.find((x) => x.name === e.target.value); if (p) applyPreset(p); e.target.value = ""; }}>
                  <option value="">筛选视图…</option>
                  {presets.map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
                </select>
              )}
              {selected.size > 0 && <span className="muted small">已选 {selected.size}</span>}
            </>
          }
          toolbarRight={
            <>
              {anyFilter && <button className="linkish small" onClick={resetFilters}>重置</button>}
              <button className="btn-ghost" disabled={!anyFilter} onClick={savePreset}>保存视图</button>
              <button className="btn-ghost" onClick={() => apiDownload("/orders/export?page_size=5000", "orders.csv")}>导出全部</button>
            </>
          }
        />
      )}
    </div>

    {/* 登记异常（右键菜单）*/}
    {excOrder && <ExceptionRegisterModal order={excOrder} onClose={() => setExcOrder(null)} onDone={() => { setExcOrder(null); invalidate(); }} />}

    {/* 订单详情抽屉 */}
    {drawer && (
      <div className="wb-overlay" onClick={() => setDrawer(null)}>
        <div ref={drawerRef} className="wb-drawer" onClick={(e) => e.stopPropagation()} tabIndex={-1} role="dialog" aria-modal="true" aria-label="订单详情">
          <div className="wb-drawer-head">
            <div>
              <div className="mono" style={{ fontSize: 15, fontWeight: 650 }}><CopyCode value={drawer.order_no} /></div>
              <div className="muted small" style={{ marginTop: 2 }}>{drawer.origin || "?"} → {drawer.destination || "?"}</div>
            </div>
            <button className="btn-ghost" onClick={() => setDrawer(null)}>关闭 [Esc]</button>
          </div>
          <div className="wb-drawer-body">
            <div className="stack" style={{ gap: 14 }}>
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
                <StatusTag kind="order" value={drawer.status} />
                {drawer.sla_status && drawer.sla_status !== "pending" && <StatusTag kind="sla" value={drawer.sla_status} />}
                <span>{drawer.customer_name || "散客"}</span>
                {drawer.customer_level && <span className={`tag ${LEVEL_TONE[drawer.customer_level] ?? "tag-none"}`}>{drawer.customer_level} 级</span>}
                {drawer.lock_state && drawer.lock_state !== "free" && <span className={`tag ${LOCK_TONE[drawer.lock_state]}`}>{LOCK_LABEL[drawer.lock_state]}{(drawer.claimed_by_name || drawer.assigned_to_name) ? ` · ${drawer.claimed_by_name || drawer.assigned_to_name}` : ""}</span>}
              </div>

              <div className="section-label">契约信息</div>
              <div className="kv">
                <div><span>渠道</span><b>{ORDER_CHANNEL_LABEL[drawer.channel] ?? drawer.channel}</b></div>
                <div><span>客户分类</span><b>{SOURCE_TYPE_LABEL[drawer.source_type] ?? drawer.source_type}</b></div>
                <div><span>业务类型</span><b>{BUSINESS_TYPE_LABEL[drawer.business_type] ?? drawer.business_type}</b></div>
                <div><span>优先级</span><b>{PRIORITY_LABEL[drawer.priority] ?? drawer.priority}</b></div>
                <div><span>结算方式</span><b>{SETTLEMENT_LABEL[drawer.settlement_type] ?? drawer.settlement_type ?? "—"}</b></div>
                <div><span>报价</span><b>{Number(drawer.quoted_amount) > 0 ? fmtMoney(drawer.quoted_amount) : "—"}</b></div>
                <div><span>建单人</span><b>{drawer.created_by_name || "—"}</b></div>
                <div><span>来源</span><b className="small">{drawer.source || "—"}</b></div>
                <div><span>建单时间</span><b className="small">{fmtDateTime(drawer.created_at)}</b></div>
              </div>

              <div className="section-label">货物明细</div>
              {(drawer.cargo_items ?? []).length > 0 ? (
                <div className="table-wrap"><table className="table" style={{ fontSize: 12.5 }}>
                  <thead><tr><th>品名</th><th className="num">件数</th><th className="num">重量(吨)</th><th className="num">体积(方)</th><th>包装</th></tr></thead>
                  <tbody>{drawer.cargo_items.map((c, i) => <tr key={i}><td>{c.name || "—"}</td><td className="num">{c.quantity}</td><td className="num">{c.weight_ton}</td><td className="num">{c.volume_cbm}</td><td className="small">{c.package_type || "—"}</td></tr>)}</tbody>
                </table></div>
              ) : <div className="muted small">合计 {drawer.cargo_weight_ton}吨 / {drawer.cargo_quantity}件 / {drawer.cargo_volume_cbm}方 · {drawer.cargo_desc || "无明细"}</div>}

              {(drawer.waybill_nos ?? []).length > 0 && (
                <>
                  <div className="section-label">关联运单</div>
                  <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                    {drawer.waybill_nos.map((no) => <Link key={no} className="tag tag-info mono" to={`/waybills/${no}`}>{no}</Link>)}
                  </div>
                </>
              )}

              <div className="section-label">时间线</div>
              {timeline.isLoading ? <span className="muted small">加载中…</span> : (timeline.data ?? []).length === 0 ? <span className="muted small">暂无事件</span> : (
                <ul className="timeline">
                  {(timeline.data ?? []).map((ev) => (
                    <li key={ev.id}><span className="dot" /><div><span className="tl-type">{ORDER_EVENT_LABEL[ev.event_type] ?? ev.event_type}</span> <span className="muted small">{fmtDateTime(ev.event_time)} · {ev.actor_name || "系统"}</span>{ev.to_status && <span className="muted small"> · → {ORDER_STATUS_LABEL[ev.to_status] ?? ev.to_status}</span>}</div></li>
                  ))}
                </ul>
              )}
            </div>
          </div>
          <div className="wb-actions" style={{ borderTop: "1px solid var(--line)" }}>
            {(drawer.status === "draft" || drawer.status === "pending_confirm") && <button className="btn-ghost" onClick={() => { batch.mutate({ action: "confirm", ids: [drawer.id] }); setDrawer(null); }}>确认</button>}
            {(drawer.status === "confirmed" || drawer.status === "pending_confirm") && <button className="btn-ghost" onClick={() => { batch.mutate({ action: "pool", ids: [drawer.id] }); setDrawer(null); }}>进池</button>}
            {drawer.status === "pooled" && <Link className="btn-primary" to="/dispatch-board" style={{ textDecoration: "none" }}>去派单</Link>}
            <Link className="btn-ghost" to={`/orders/${drawer.id}`} style={{ textDecoration: "none" }}>完整详情页</Link>
          </div>
        </div>
      </div>
    )}
    </>
  );
}

// 派车批次高级筛选字段（与后端 server_filter_fields 对齐）
const BATCH_FILTER_FIELDS: FilterFieldDef[] = [
  { key: "batch_no", label: "批次号", type: "text", accessor: (b) => (b as DispatchBatch).batch_no },
  { key: "carrier", label: "承运商/平台", type: "text", accessor: (b) => (b as DispatchBatch).carrier_name || (b as DispatchBatch).platform_name || "" },
  { key: "status", label: "状态", type: "enum", options: Object.entries(BATCH_STATUS_LABEL).map(([value, label]) => ({ value, label })), accessor: (b) => (b as DispatchBatch).status },
  { key: "payable", label: "总应付(元)", type: "number", accessor: (b) => Number((b as DispatchBatch).total_payable) || 0 },
  { key: "count", label: "运单数", type: "number", accessor: (b) => (b as DispatchBatch).order_count },
  { key: "weight", label: "总货量(吨)", type: "number", accessor: (b) => Number((b as DispatchBatch).total_weight_ton) || 0 },
];

// ── 批次视图（派车批次台账：多单一次委托同一承运商）──────────────
function BatchesTab() {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("");
  const [drawer, setDrawer] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [model, setModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showBuilder, setShowBuilder] = useState(false);
  const batchActiveCount = activeConditionCount(model, BATCH_FILTER_FIELDS);
  const anyFilter = Boolean(search) || Boolean(status) || batchActiveCount > 0;
  const st = useServerTable<DispatchBatch>({
    queryKey: ["dispatch-batches"],
    path: "/dispatch-batches",
    pageSize: 50,
    defaultSort: { field: "created_at", dir: "desc" },
    model,
    search,
    extraParams: { status: status || undefined },
  });
  const detail = useQuery({
    queryKey: ["dispatch-batch", drawer],
    queryFn: () => apiGet<DispatchBatchDetail>(`/dispatch-batches/${drawer}`),
    enabled: Boolean(drawer),
  });
  // 一键生成承运商应付对账单（可带承运商回单金额做差异稽核）
  const [externalTotal, setExternalTotal] = useState("");
  const genStatement = useMutation({
    mutationFn: (id: string) => apiPost<{ statement_no: string; reused: boolean }>(`/dispatch-batches/${id}/statement`, { external_total: Number(externalTotal) || 0 }),
    onSuccess: (r) => {
      toast.success(r.reused ? `该批次已对账：${r.statement_no}` : `已生成承运商应付对账单：${r.statement_no}`);
      setExternalTotal("");
      queryClient.invalidateQueries({ queryKey: ["dispatch-batch"] });
      queryClient.invalidateQueries({ queryKey: ["dispatch-batches"] });
    },
    onError: (e: Error) => toast.error(e.message || "生成对账单失败"),
  });

  const drawerRef = useRef<HTMLDivElement>(null);
  useModalA11y(Boolean(drawer), drawerRef, () => setDrawer(null));

  const columns: DataColumn<DispatchBatch>[] = [
    { key: "batch_no", header: "批次号", width: 165, alwaysVisible: true, sortField: "batch_no", sortValue: (b) => b.batch_no, exportValue: (b) => b.batch_no, render: (b) => <span className="mono">{b.batch_no}</span> },
    { key: "carrier", header: "承运商 / 平台", width: 150, sortField: "carrier__name", sortValue: (b) => b.carrier_name || b.platform_name, exportValue: (b) => b.carrier_name || b.platform_name, render: (b) => <span>{b.carrier_name || b.platform_name || "—"}<span className="tag tag-info small" style={{ marginLeft: 6 }}>{b.dispatch_type_label}</span></span> },
    { key: "customers", header: "涉及客户", width: 200, sortValue: (b) => b.customer_summary.join(","), exportValue: (b) => b.customer_summary.join("、"), render: (b) => <span className="small">{b.customer_summary.length > 1 ? <span className="tag tag-medium" style={{ marginRight: 4 }}>跨客户</span> : null}{b.customer_summary.slice(0, 2).join("、")}{b.customer_summary.length > 2 ? ` +${b.customer_summary.length - 2}` : ""}</span> },
    { key: "count", header: "运单数", width: 80, align: "right", sortField: "order_count", sortValue: (b) => b.order_count, exportValue: (b) => b.order_count, render: (b) => <b>{b.order_count}</b> },
    { key: "weight", header: "总货量", width: 100, align: "right", sortField: "total_weight_ton", sortValue: (b) => Number(b.total_weight_ton), exportValue: (b) => b.total_weight_ton, render: (b) => <span className="num">{Number(b.total_weight_ton).toFixed(1)}吨</span> },
    { key: "payable", header: "总应付", width: 120, align: "right", sortField: "total_payable", sortValue: (b) => Number(b.total_payable), exportValue: (b) => Number(b.total_payable), render: (b) => <span className="num">{fmtMoney(b.total_payable)}</span> },
    { key: "alloc", header: "分摊", width: 90, sortValue: (b) => b.allocation, exportValue: (b) => b.allocation_label, render: (b) => <span className="small muted">{b.allocation_label}</span> },
    { key: "status", header: "状态", width: 90, sortField: "status", sortValue: (b) => b.status, exportValue: (b) => b.status_label, render: (b) => <StatusTag kind="batch" value={b.status} /> },
    { key: "creator", header: "建批人", width: 100, sortValue: (b) => b.created_by_name, exportValue: (b) => b.created_by_name, render: (b) => <span className="small muted">{b.created_by_name || "—"}</span> },
    { key: "created", header: "建批时间", width: 130, sortField: "created_at", sortValue: (b) => b.created_at, exportValue: (b) => fmtDateTime(b.created_at), render: (b) => <span className="small" title={fmtDateTime(b.created_at)}>{fmtRelative(b.created_at)}</span> },
  ];

  return (
    <div className="panel om-panel">
      {batchActiveCount > 0 && (
        <div className="om-chips">
          <span className="muted small">条件（{model.combinator === "and" ? "全部满足" : "任一满足"}）：</span>
          {model.conditions.map((c) => {
            const label = describeCondition(c, BATCH_FILTER_FIELDS);
            if (!label) return null;
            return <span key={c.id} className="filter-chip">{label}<button onClick={() => setModel((m) => ({ ...m, conditions: m.conditions.filter((x) => x.id !== c.id) }))}>×</button></span>;
          })}
          <button className="linkish small" onClick={() => setModel(EMPTY_MODEL)}>清空条件</button>
        </div>
      )}
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
        <button className={`chip${status === "" ? " chip-on" : ""}`} onClick={() => setStatus("")}>全部</button>
        {Object.entries(BATCH_STATUS_LABEL).map(([k, v]) => <button key={k} className={`chip${status === k ? " chip-on" : ""}`} onClick={() => setStatus(status === k ? "" : k)}>{v}</button>)}
      </div>
      {st.isError ? (
        <StateView kind="error" onRetry={() => st.refetch()} />
      ) : (
        <DataTable<DispatchBatch>
          columns={columns} rows={st.rows} rowKey={(b) => b.id} viewKey="dispatch-batches" exportName="派车批次"
          stickyFirst server={st.server} fill hideExport onRowClick={(b) => setDrawer(b.id)}
          emptyState={anyFilter
            ? <StateView kind="empty" title="没有匹配的派车批次" hint="调整搜索/筛选条件再试。" />
            : <StateView kind="empty" title="暂无派车批次" hint="在调度工作台选中多单 →「批量派承运商」即可生成批次。" />}
          toolbarLeft={
            <>
              <span className="om-title" style={{ marginRight: 2 }}>派车批次<span className="ai-pill">{st.total}</span></span>
              <input className="search" style={{ minWidth: 180, flex: 1, maxWidth: 280 }} placeholder="搜索 批次号 / 承运商" value={search} onChange={(e) => setSearch(e.target.value)} />
              <div style={{ position: "relative" }}>
                <button className={`btn-ghost${batchActiveCount > 0 || showBuilder ? " on-accent" : ""}`} onClick={(e) => { e.stopPropagation(); setShowBuilder((v) => !v); }}>
                  <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 5h18l-7 8v5l-4 2v-7z" /></svg>
                    高级筛选{batchActiveCount > 0 ? ` · ${batchActiveCount}` : ""}
                  </span>
                </button>
                {showBuilder && <FilterBuilder fields={BATCH_FILTER_FIELDS} model={model} onChange={setModel} onClose={() => setShowBuilder(false)} />}
              </div>
            </>
          }
          toolbarRight={<span className="muted small">批次 = 承运商应付统一对账分组</span>}
        />
      )}

      {drawer && (
        <div className="wb-overlay" onClick={() => setDrawer(null)}>
          <div ref={drawerRef} className="wb-drawer" onClick={(e) => e.stopPropagation()} tabIndex={-1} role="dialog" aria-modal="true" aria-label="派车批次详情">
            <div className="wb-drawer-head">
              <div>
                <div className="mono" style={{ fontSize: 15, fontWeight: 650 }}>{detail.data?.batch_no ?? "批次"}</div>
                <div className="muted small" style={{ marginTop: 2 }}>{detail.data?.carrier_name || detail.data?.platform_name} · {detail.data?.order_count} 单 · {detail.data?.allocation_label}</div>
              </div>
              <button className="btn-ghost" onClick={() => setDrawer(null)}>关闭 [Esc]</button>
            </div>
            <div className="wb-drawer-body">
              {detail.isLoading ? <StateView kind="loading" compact /> : detail.data && (
                <div className="stack" style={{ gap: 14 }}>
                  <div className="kv">
                    <div><span>承运通道</span><b>{detail.data.dispatch_type_label}</b></div>
                    <div><span>承运商/平台</span><b>{detail.data.carrier_name || detail.data.platform_name || "—"}</b></div>
                    <div><span>总应付</span><b>{fmtMoney(detail.data.total_payable)}</b></div>
                    <div><span>分摊方式</span><b>{detail.data.allocation_label}</b></div>
                    <div><span>总货量</span><b>{Number(detail.data.total_weight_ton).toFixed(2)} 吨</b></div>
                    <div><span>状态</span><b>{detail.data.status_label}</b></div>
                    <div><span>建批人</span><b>{detail.data.created_by_name || "—"}</b></div>
                    <div><span>建批时间</span><b className="small">{fmtDateTime(detail.data.created_at)}</b></div>
                  </div>
                  {detail.data.note && <div className="muted small">备注：{detail.data.note}</div>}
                  <div className="section-label">批次内运单（各自独立回单/签收/对账）</div>
                  <div className="table-wrap"><table className="table" style={{ fontSize: 12.5 }}>
                    <thead><tr><th>运单号</th><th>订单号</th><th>客户</th><th>线路</th><th className="num">货量</th><th className="num">分摊应付</th><th>状态</th></tr></thead>
                    <tbody>
                      {detail.data.waybills.map((w) => (
                        <tr key={w.id}>
                          <td><Link className="link mono small" to={`/waybills/${w.waybill_no}`}>{w.waybill_no}</Link></td>
                          <td className="mono small">{w.order_no}</td>
                          <td className="small">{w.customer_name || "散客"}</td>
                          <td className="small">{w.origin} → {w.destination}</td>
                          <td className="num small">{w.cargo_weight_ton}吨</td>
                          <td className="num">{w.payable != null ? fmtMoney(w.payable) : "—"}</td>
                          <td><StatusTag kind="waybill" value={w.status} /></td>
                        </tr>
                      ))}
                    </tbody>
                  </table></div>
                </div>
              )}
            </div>
            {detail.data && detail.data.dispatch_type === "third_party" && (
              <div className="wb-actions" style={{ borderTop: "1px solid var(--line)" }}>
                {detail.data.statement_no ? (
                  <>
                    <span className="muted small" style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                      <span className="tag tag-low">已对账</span>{detail.data.statement_no}
                    </span>
                    <div style={{ flex: 1 }} />
                    <Link className="btn-ghost" to="/reconciliation" style={{ textDecoration: "none" }}>去对账中心</Link>
                  </>
                ) : (
                  <>
                    <span className="muted small">批次内 {detail.data.order_count} 单应付归集为一张对账单</span>
                    <div style={{ flex: 1 }} />
                    <input className="search" style={{ width: 150, padding: "6px 10px" }} value={externalTotal}
                      onChange={(e) => setExternalTotal(e.target.value)} placeholder="承运商回单金额（选填）" title="填写承运商侧金额，生成对账单时自动做差异稽核" />
                    <button className="btn-primary" disabled={genStatement.isPending} onClick={() => genStatement.mutate(detail.data!.id)}>
                      {genStatement.isPending ? "生成中…" : "生成承运商对账单"}
                    </button>
                  </>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export function OrderManagePage() {
  const [tab, setTab] = useState<"order" | "waybill" | "batch">("order");
  return (
    <div className={`stack${tab === "order" || tab === "waybill" || tab === "batch" ? " table-page" : ""}`}>
      <div className="seg-toggle" style={{ alignSelf: "flex-start" }}>
        <button className={`seg-btn${tab === "order" ? " on" : ""}`} onClick={() => setTab("order")}>订单</button>
        <button className={`seg-btn${tab === "waybill" ? " on" : ""}`} onClick={() => setTab("waybill")}>运单</button>
        <button className={`seg-btn${tab === "batch" ? " on" : ""}`} onClick={() => setTab("batch")}>批次</button>
      </div>
      {tab === "order" ? <OrdersTab /> : tab === "waybill" ? <WaybillsPage embedded /> : <BatchesTab />}
    </div>
  );
}
