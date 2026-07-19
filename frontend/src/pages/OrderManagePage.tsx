import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { apiDownload, apiGet, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { fmtDateTime, fmtMoney, fmtRelative } from "../api/format";
import { toast } from "../api/toast";
import type { DispatchBatch, DispatchBatchDetail, Order, OrderEvent, Paginated } from "../api/types";
import {
  BATCH_STATUS_LABEL, BUSINESS_TYPE_LABEL, ORDER_CHANNEL_LABEL, ORDER_EVENT_LABEL, ORDER_STATUS_LABEL, PRIORITY_LABEL, SETTLEMENT_LABEL, SOURCE_TYPE_LABEL,
} from "../api/types";
import { DataTable, type DataColumn } from "../components/DataTable";
import { StateView } from "../components/StateView";
import { StatusTag } from "../components/StatusTag";
import { WaybillsPage } from "./WaybillsPage";

const LEVEL_TONE: Record<string, string> = { S: "tag-info", A: "tag-low", B: "tag-info", C: "tag-medium", D: "tag-none" };
const LOCK_LABEL: Record<string, string> = { mine: "我锁定", locked: "他人锁定", assigned_mine: "分派给我", assigned_other: "已分派", free: "" };
const LOCK_TONE: Record<string, string> = { mine: "tag-low", locked: "tag-medium", assigned_mine: "tag-info", assigned_other: "tag-none", free: "" };
// 订单生命周期状态过滤链
const STATUS_CHAIN = ["draft", "pending_confirm", "confirmed", "pooled", "dispatching", "converted", "completed", "cancelled"];

// ── 订单视图（护城河主视图）──────────────────────────────
function OrdersTab() {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("");
  const [channel, setChannel] = useState("");
  const [bizType, setBizType] = useState("");
  const [priority, setPriority] = useState("");
  const [level, setLevel] = useState("");
  const [days, setDays] = useState("30");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());

  // 保存的筛选视图（localStorage 持久化，无需后端）
  type Preset = { name: string; status: string; channel: string; bizType: string; priority: string; level: string; days: string; search: string };
  const PRESET_KEY = "om-order-presets";
  const [presets, setPresets] = useState<Preset[]>(() => {
    try { return JSON.parse(localStorage.getItem(PRESET_KEY) || "[]"); } catch { return []; }
  });
  const persistPresets = (next: Preset[]) => { setPresets(next); localStorage.setItem(PRESET_KEY, JSON.stringify(next)); };
  const applyPreset = (p: Preset) => { setStatus(p.status); setChannel(p.channel); setBizType(p.bizType); setPriority(p.priority); setLevel(p.level); setDays(p.days); setSearch(p.search); };
  const savePreset = () => {
    const name = window.prompt("为当前筛选视图命名：", "")?.trim();
    if (!name) return;
    const p: Preset = { name, status, channel, bizType, priority, level, days, search };
    persistPresets([...presets.filter((x) => x.name !== name), p]);
    toast.success(`已保存筛选视图「${name}」`);
  };
  const anyFilter = Boolean(status || channel || bizType || priority || level || search || days !== "30");
  const resetFilters = () => { setStatus(""); setChannel(""); setBizType(""); setPriority(""); setLevel(""); setDays("30"); setSearch(""); };

  const filterQs = [
    status && `status=${status}`, channel && `channel=${channel}`,
    bizType && `business_type=${bizType}`, priority && `priority=${priority}`,
    search && `search=${encodeURIComponent(search)}`,
  ].filter(Boolean).join("&");

  const q = useQuery({
    queryKey: ["orders-manage", status, channel, bizType, priority, search],
    queryFn: () => apiGet<Paginated<Order>>(`/orders?page_size=300&ordering=-created_at${filterQs ? `&${filterQs}` : ""}`),
  });
  const dispatchers = useQuery({
    queryKey: ["dispatchers"],
    queryFn: () => apiGet<{ is_chief: boolean; dispatchers: Array<{ id: string; name: string }> }>("/orders/dispatchers"),
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["orders-manage"] });

  // 客户等级 + 日期范围在已加载集上二次过滤（服务端未建索引字段）
  const rows = useMemo(() => {
    let items = q.data?.items ?? [];
    if (level) items = items.filter((o) => (o.customer_level || "") === level);
    if (days) {
      const cut = Date.now() - Number(days) * 86400000;
      items = items.filter((o) => new Date(o.created_at).getTime() >= cut);
    }
    return items;
  }, [q.data, level, days]);
  const total = rows.length;

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

  useEffect(() => {
    if (!drawer) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setDrawer(null); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [drawer]);

  // 台账概览（基于已加载集）
  const stats = useMemo(() => {
    const all = q.data?.items ?? [];
    const today = new Date().toDateString();
    return {
      total: all.length,
      pending: all.filter((o) => o.status === "draft" || o.status === "pending_confirm").length,
      pooled: all.filter((o) => o.status === "pooled" || o.status === "dispatching").length,
      dispatched: all.filter((o) => o.status === "converted" || o.status === "completed").length,
      today: all.filter((o) => new Date(o.created_at).toDateString() === today).length,
      amount: all.reduce((s, o) => s + (Number(o.quoted_amount) || 0), 0),
    };
  }, [q.data]);

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
    { key: "order_no", header: "订单号", width: 165, alwaysVisible: true, sortValue: (o) => o.order_no, exportValue: (o) => o.order_no, render: (o) => <Link className="link mono" to={`/orders/${o.id}`}>{o.order_no}</Link> },
    { key: "customer", header: "客户", width: 150, sortValue: (o) => o.customer_name || "", exportValue: (o) => o.customer_name || "散客", render: (o) => <span>{o.customer_name || "散客"}{o.customer_level && <span className={`tag ${LEVEL_TONE[o.customer_level] ?? "tag-none"}`} style={{ marginLeft: 4 }}>{o.customer_level}</span>}</span> },
    { key: "channel", header: "渠道", width: 90, sortValue: (o) => o.channel, exportValue: (o) => ORDER_CHANNEL_LABEL[o.channel] ?? o.channel, render: (o) => <span className="small">{ORDER_CHANNEL_LABEL[o.channel] ?? o.channel}</span> },
    { key: "route", header: "线路", width: 150, sortValue: (o) => `${o.origin}${o.destination}`, exportValue: (o) => `${o.origin || "?"}→${o.destination || "?"}`, render: (o) => <><b>{o.origin || "?"}</b> → <b>{o.destination || "?"}</b></> },
    { key: "biz", header: "业务", width: 90, sortValue: (o) => o.business_type, exportValue: (o) => BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type, render: (o) => <span className="small">{BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type}{o.business_type === "hazmat" || o.is_hazardous ? <span className="tag tag-high" style={{ marginLeft: 4 }}>危</span> : ""}</span> },
    { key: "cargo", header: "货量", width: 110, align: "right", sortValue: (o) => Number(o.cargo_weight_ton) || 0, exportValue: (o) => `${o.cargo_weight_ton}吨/${o.cargo_quantity}件`, render: (o) => <span className="num">{o.cargo_weight_ton}吨/{o.cargo_quantity}件</span> },
    { key: "amount", header: "报价", width: 110, align: "right", sortValue: (o) => Number(o.quoted_amount) || 0, exportValue: (o) => Number(o.quoted_amount) || 0, render: (o) => <span className="num">{Number(o.quoted_amount) > 0 ? fmtMoney(o.quoted_amount) : "—"}</span> },
    { key: "priority", header: "优先级", width: 80, sortValue: (o) => o.priority, exportValue: (o) => PRIORITY_LABEL[o.priority] ?? o.priority, render: (o) => <span className={`tag tag-${o.priority === "vip" ? "high" : o.priority === "urgent" ? "medium" : "none"}`}>{PRIORITY_LABEL[o.priority]}</span> },
    { key: "status", header: "状态", width: 100, sortValue: (o) => o.status, exportValue: (o) => ORDER_STATUS_LABEL[o.status] ?? o.status, render: (o) => <StatusTag kind="order" value={o.status} /> },
    { key: "sla", header: "SLA", width: 84, sortValue: (o) => o.sla_status, exportValue: (o) => o.sla_status, render: (o) => <StatusTag kind="sla" value={o.sla_status} /> },
    { key: "lock", header: "锁定/分派", width: 110, sortValue: (o) => o.lock_state ?? "", exportValue: (o) => LOCK_LABEL[o.lock_state ?? "free"] ?? "", render: (o) => o.lock_state && o.lock_state !== "free" ? <span className={`tag ${LOCK_TONE[o.lock_state]}`} title={o.claimed_by_name || o.assigned_to_name}>{LOCK_LABEL[o.lock_state]}{(o.claimed_by_name || o.assigned_to_name) ? ` · ${o.claimed_by_name || o.assigned_to_name}` : ""}</span> : <span className="muted small">—</span> },
    { key: "creator", header: "建单人", width: 100, sortValue: (o) => o.created_by_name || "", exportValue: (o) => o.created_by_name || "", render: (o) => <span className="small muted">{o.created_by_name || "-"}</span> },
    { key: "created", header: "建单时间", width: 130, sortValue: (o) => o.created_at, exportValue: (o) => fmtDateTime(o.created_at), render: (o) => <span className="small" title={fmtDateTime(o.created_at)}>{fmtRelative(o.created_at)}</span> },
    {
      key: "actions", header: "操作", width: 180, alwaysVisible: true,
      render: (o) => (
        <div className="row-actions" onClick={(e) => e.stopPropagation()}>
          {(o.status === "draft" || o.status === "pending_confirm") && <button disabled={batch.isPending} onClick={() => batch.mutate({ action: "confirm", ids: [o.id] })}>确认</button>}
          {(o.status === "confirmed" || o.status === "pending_confirm") && <button disabled={batch.isPending} onClick={() => batch.mutate({ action: "pool", ids: [o.id] })}>进池</button>}
          {o.status === "pooled" && <Link className="link small" to="/dispatch-board">去派单</Link>}
          <Link className="link small" to={`/orders/${o.id}`}>详情</Link>
          {(o.waybill_nos ?? []).map((no) => <Link key={no} className="link mono small" to={`/waybills/${no}`}>{no}</Link>)}
        </div>
      ),
    },
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
          <button className="btn-ghost" disabled={!assignTo || assign.isPending} onClick={() => assign.mutate({ ids: [...selected], dispatcher: assignTo })}>分单</button>
        </span>
      )}
      <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("cancel")}>取消</button>
      <button className="btn-danger-ghost" disabled={batch.isPending} onClick={() => runBatch("delete")}>删除</button>
      <button className="btn-ghost" onClick={() => setSelected(new Set())}>清除</button>
    </div>
  ) : null;

  return (
    <>
    {/* 台账概览 */}
    <div className="om-stats">
      <div className="om-stat"><div className="om-stat-n">{stats.total}</div><div className="om-stat-l">订单总数</div></div>
      <button className={`om-stat om-clickable${status === "pending_confirm" ? " on" : ""}`} onClick={() => setStatus(status === "pending_confirm" ? "" : "pending_confirm")}><div className="om-stat-n" style={{ color: stats.pending ? "var(--amber)" : undefined }}>{stats.pending}</div><div className="om-stat-l">待确认 →</div></button>
      <button className={`om-stat om-clickable${status === "pooled" ? " on" : ""}`} onClick={() => setStatus(status === "pooled" ? "" : "pooled")}><div className="om-stat-n" style={{ color: stats.pooled ? "var(--blue)" : undefined }}>{stats.pooled}</div><div className="om-stat-l">池中待派 →</div></button>
      <div className="om-stat"><div className="om-stat-n" style={{ color: stats.dispatched ? "var(--green)" : undefined }}>{stats.dispatched}</div><div className="om-stat-l">已派/完成</div></div>
      <div className="om-stat"><div className="om-stat-n">{stats.today}</div><div className="om-stat-l">今日新建</div></div>
      <div className="om-stat"><div className="om-stat-n" style={{ fontSize: 20 }}>{fmtMoney(stats.amount)}</div><div className="om-stat-l">报价合计</div></div>
    </div>

    <div className="panel">
      <div className="panel-head" style={{ flexWrap: "wrap", gap: 10 }}>
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>订单台账<span className="ai-pill">{total}</span></span>
        <button className="btn-ghost" onClick={() => apiDownload(`/orders/export?page_size=5000${filterQs ? `&${filterQs}` : ""}`, "orders.csv")}>导出 CSV</button>
      </div>

      {/* 状态链筛选 */}
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
        <button className={`chip${status === "" ? " chip-on" : ""}`} onClick={() => setStatus("")}>全部状态</button>
        {STATUS_CHAIN.map((s) => <button key={s} className={`chip${status === s ? " chip-on" : ""}`} onClick={() => setStatus(s)}>{ORDER_STATUS_LABEL[s] ?? s}</button>)}
      </div>
      {/* 维度筛选 */}
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8, paddingTop: 0 }}>
        <select className="search" style={{ minWidth: 110, padding: "7px 10px" }} value={channel} onChange={(e) => setChannel(e.target.value)}>
          <option value="">全部渠道</option>{Object.entries(ORDER_CHANNEL_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <select className="search" style={{ minWidth: 100, padding: "7px 10px" }} value={bizType} onChange={(e) => setBizType(e.target.value)}>
          <option value="">全部业务</option>{Object.entries(BUSINESS_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <select className="search" style={{ minWidth: 100, padding: "7px 10px" }} value={priority} onChange={(e) => setPriority(e.target.value)}>
          <option value="">全部优先级</option>{Object.entries(PRIORITY_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <select className="search" style={{ minWidth: 100, padding: "7px 10px" }} value={level} onChange={(e) => setLevel(e.target.value)}>
          <option value="">全部等级</option>{["S", "A", "B", "C", "D"].map((l) => <option key={l} value={l}>{l} 级客户</option>)}
        </select>
        <select className="search" style={{ minWidth: 100, padding: "7px 10px" }} value={days} onChange={(e) => setDays(e.target.value)}>
          <option value="7">近 7 天</option><option value="30">近 30 天</option><option value="90">近 90 天</option><option value="">全部时间</option>
        </select>
        <input className="search" style={{ minWidth: 200, flex: 1 }} placeholder="搜索订单号 / 电话 / 始发 / 目的地" value={search} onChange={(e) => setSearch(e.target.value)} />
      </div>

      {/* 保存的筛选视图 */}
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8, paddingTop: 0, alignItems: "center" }}>
        <span className="muted small" style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M19 21l-7-4-7 4V5a2 2 0 012-2h10a2 2 0 012 2z" /></svg>筛选视图
        </span>
        {presets.length === 0 && <span className="muted small">保存常用筛选组合，一键切换</span>}
        {presets.map((p) => (
          <span key={p.name} className="preset-chip">
            <button className="chip" onClick={() => applyPreset(p)}>{p.name}</button>
            <button className="preset-x" title="删除视图" onClick={() => persistPresets(presets.filter((x) => x.name !== p.name))}>×</button>
          </span>
        ))}
        <div style={{ flex: 1 }} />
        {anyFilter && <button className="linkish small" onClick={resetFilters}>重置筛选</button>}
        <button className="btn-ghost small" disabled={!anyFilter} onClick={savePreset}>+ 保存当前筛选</button>
      </div>

      {q.isLoading ? (
        <StateView kind="loading" compact />
      ) : q.isError ? (
        <StateView kind="error" onRetry={() => q.refetch()} />
      ) : rows.length === 0 ? (
        (status || channel || bizType || priority || level || search) ? <StateView kind="empty" title="没有匹配的订单" hint="调整筛选条件再试。" /> : <StateView kind="empty" scene="cs-empty" />
      ) : (
        <DataTable<Order>
          columns={columns} rows={rows} rowKey={(o) => o.id} viewKey="order-manage" exportName="订单台账"
          selectable selected={selected} onToggle={toggle} onToggleAll={toggleAll} stickyFirst batchBar={batchBar}
          onRowClick={(o) => setDrawer(o)}
          toolbarLeft={<span className="muted small">共 {total} 单{selected.size ? ` · 已选 ${selected.size}` : ""} · 点击行看详情 · 表头排序 · 「列」增减</span>}
        />
      )}
    </div>

    {/* 订单详情抽屉 */}
    {drawer && (
      <div className="wb-overlay" onClick={() => setDrawer(null)}>
        <div className="wb-drawer" onClick={(e) => e.stopPropagation()}>
          <div className="wb-drawer-head">
            <div>
              <div className="mono" style={{ fontSize: 15, fontWeight: 650 }}>{drawer.order_no}</div>
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
                <table className="table" style={{ fontSize: 12.5 }}>
                  <thead><tr><th>品名</th><th className="num">件数</th><th className="num">重量(吨)</th><th className="num">体积(方)</th><th>包装</th></tr></thead>
                  <tbody>{drawer.cargo_items.map((c, i) => <tr key={i}><td>{c.name || "—"}</td><td className="num">{c.quantity}</td><td className="num">{c.weight_ton}</td><td className="num">{c.volume_cbm}</td><td className="small">{c.package_type || "—"}</td></tr>)}</tbody>
                </table>
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

// ── 批次视图（派车批次台账：多单一次委托同一承运商）──────────────
function BatchesTab() {
  const [status, setStatus] = useState("");
  const [drawer, setDrawer] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ["dispatch-batches", status],
    queryFn: () => apiGet<Paginated<DispatchBatch>>(`/dispatch-batches?page_size=200${status ? `&status=${status}` : ""}`),
  });
  const detail = useQuery({
    queryKey: ["dispatch-batch", drawer],
    queryFn: () => apiGet<DispatchBatchDetail>(`/dispatch-batches/${drawer}`),
    enabled: Boolean(drawer),
  });

  useEffect(() => {
    if (!drawer) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setDrawer(null); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [drawer]);

  const rows = q.data?.items ?? [];
  const columns: DataColumn<DispatchBatch>[] = [
    { key: "batch_no", header: "批次号", width: 165, alwaysVisible: true, sortValue: (b) => b.batch_no, exportValue: (b) => b.batch_no, render: (b) => <span className="mono">{b.batch_no}</span> },
    { key: "carrier", header: "承运商 / 平台", width: 150, sortValue: (b) => b.carrier_name || b.platform_name, exportValue: (b) => b.carrier_name || b.platform_name, render: (b) => <span>{b.carrier_name || b.platform_name || "—"}<span className="tag tag-info small" style={{ marginLeft: 6 }}>{b.dispatch_type_label}</span></span> },
    { key: "customers", header: "涉及客户", width: 200, sortValue: (b) => b.customer_summary.join(","), exportValue: (b) => b.customer_summary.join("、"), render: (b) => <span className="small">{b.customer_summary.length > 1 ? <span className="tag tag-medium" style={{ marginRight: 4 }}>跨客户</span> : null}{b.customer_summary.slice(0, 2).join("、")}{b.customer_summary.length > 2 ? ` +${b.customer_summary.length - 2}` : ""}</span> },
    { key: "count", header: "运单数", width: 80, align: "right", sortValue: (b) => b.order_count, exportValue: (b) => b.order_count, render: (b) => <b>{b.order_count}</b> },
    { key: "weight", header: "总货量", width: 100, align: "right", sortValue: (b) => Number(b.total_weight_ton), exportValue: (b) => b.total_weight_ton, render: (b) => <span className="num">{Number(b.total_weight_ton).toFixed(1)}吨</span> },
    { key: "payable", header: "总应付", width: 120, align: "right", sortValue: (b) => Number(b.total_payable), exportValue: (b) => Number(b.total_payable), render: (b) => <span className="num">{fmtMoney(b.total_payable)}</span> },
    { key: "alloc", header: "分摊", width: 90, sortValue: (b) => b.allocation, exportValue: (b) => b.allocation_label, render: (b) => <span className="small muted">{b.allocation_label}</span> },
    { key: "status", header: "状态", width: 90, sortValue: (b) => b.status, exportValue: (b) => b.status_label, render: (b) => <span className={`tag ${b.status === "cancelled" ? "tag-none" : b.status === "completed" ? "tag-low" : "tag-info"}`}>{b.status_label}</span> },
    { key: "creator", header: "建批人", width: 100, sortValue: (b) => b.created_by_name, exportValue: (b) => b.created_by_name, render: (b) => <span className="small muted">{b.created_by_name || "—"}</span> },
    { key: "created", header: "建批时间", width: 130, sortValue: (b) => b.created_at, exportValue: (b) => fmtDateTime(b.created_at), render: (b) => <span className="small" title={fmtDateTime(b.created_at)}>{fmtRelative(b.created_at)}</span> },
  ];

  return (
    <div className="panel">
      <div className="panel-head" style={{ flexWrap: "wrap", gap: 10 }}>
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>派车批次<span className="ai-pill">{rows.length}</span></span>
      </div>
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
        <button className={`chip${status === "" ? " chip-on" : ""}`} onClick={() => setStatus("")}>全部</button>
        {Object.entries(BATCH_STATUS_LABEL).map(([k, v]) => <button key={k} className={`chip${status === k ? " chip-on" : ""}`} onClick={() => setStatus(k)}>{v}</button>)}
      </div>
      {q.isLoading ? (
        <StateView kind="loading" compact />
      ) : q.isError ? (
        <StateView kind="error" onRetry={() => q.refetch()} />
      ) : rows.length === 0 ? (
        <StateView kind="empty" title="暂无派车批次" hint="在调度工作台选中多单 →「批量派承运商」即可生成批次。" />
      ) : (
        <DataTable<DispatchBatch>
          columns={columns} rows={rows} rowKey={(b) => b.id} viewKey="dispatch-batches" exportName="派车批次"
          stickyFirst onRowClick={(b) => setDrawer(b.id)}
          toolbarLeft={<span className="muted small">共 {rows.length} 批 · 点击行看批次内运单 · 批次 = 承运商应付统一对账分组</span>}
        />
      )}

      {drawer && (
        <div className="wb-overlay" onClick={() => setDrawer(null)}>
          <div className="wb-drawer" onClick={(e) => e.stopPropagation()}>
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
                  <table className="table" style={{ fontSize: 12.5 }}>
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
                          <td><span className="tag tag-info small">{w.status_label}</span></td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export function OrderManagePage() {
  const [tab, setTab] = useState<"order" | "waybill" | "batch">("order");
  return (
    <div className="stack">
      <div className="seg-toggle" style={{ alignSelf: "flex-start" }}>
        <button className={`seg-btn${tab === "order" ? " on" : ""}`} onClick={() => setTab("order")}>订单</button>
        <button className={`seg-btn${tab === "waybill" ? " on" : ""}`} onClick={() => setTab("waybill")}>运单</button>
        <button className={`seg-btn${tab === "batch" ? " on" : ""}`} onClick={() => setTab("batch")}>批次</button>
      </div>
      {tab === "order" ? <OrdersTab /> : tab === "waybill" ? <WaybillsPage embedded /> : <BatchesTab />}
    </div>
  );
}
