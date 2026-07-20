import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState, useEffect } from "react";
import { Link, useNavigate } from "react-router-dom";

import { apiGet, apiPatch, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import type { Contract, Paginated, Waybill } from "../api/types";
import { STATUS_LABEL, CHANNEL_TAG } from "../api/types";
import { useServerTable } from "../api/useServerTable";
import { DataTable, type DataColumn } from "../components/DataTable";
import { CopyCode } from "../components/CopyCode";
import { FilterBuilder, activeConditionCount, describeCondition, EMPTY_MODEL, type FilterFieldDef, type FilterModel } from "../components/FilterBuilder";
import { FinanceCard } from "../components/FinanceCard";
import { ReplyCard } from "../components/ReplyCard";
import { StatusTag } from "../components/StatusTag";

const STATUS_CHIPS = ["pending_dispatch", "dispatched", "in_transit", "arrived", "signed", "delivered", "settled"];
const FILTER_KEY = "waybills.filters.v1";

const RECEIPT_OPTS = [{ value: "pending", label: "待追回" }, { value: "returned", label: "已回收" }, { value: "audited", label: "已核销" }];
const CHANNEL_OPTS = [{ value: "自营", label: "自营" }, { value: "外包", label: "外包" }, { value: "网货", label: "网货" }];
// 运单高级筛选字段
const WAYBILL_FILTER_FIELDS: FilterFieldDef[] = [
  { key: "waybill_no", label: "运单号", type: "text", accessor: (w) => (w as Waybill).waybill_no },
  { key: "customer", label: "客户", type: "text", accessor: (w) => (w as Waybill).customer_name || "" },
  { key: "route", label: "线路", type: "text", accessor: (w) => `${(w as Waybill).origin || ""}→${(w as Waybill).destination || ""}` },
  { key: "vehicle", label: "车牌", type: "text", accessor: (w) => (w as Waybill).vehicle_plate || "" },
  { key: "status", label: "运单状态", type: "enum", options: Object.entries(STATUS_LABEL).map(([value, label]) => ({ value, label })), accessor: (w) => (w as Waybill).status },
  { key: "channel", label: "承运通道", type: "enum", options: CHANNEL_OPTS, accessor: (w) => (w as Waybill).channel || "" },
  { key: "receipt", label: "回单状态", type: "enum", options: RECEIPT_OPTS, accessor: (w) => (w as Waybill).receipt_status },
  { key: "receivable", label: "应收(元)", type: "number", accessor: (w) => Number((w as Waybill).receivable_amount) || 0 },
  { key: "payable", label: "应付(元)", type: "number", accessor: (w) => Number((w as Waybill).payable_amount) || 0 },
  { key: "cod", label: "代收货款(元)", type: "number", accessor: (w) => Number((w as Waybill).cod_amount) || 0 },
];

interface ContextMenuState {
  x: number;
  y: number;
  waybill: Waybill;
}

interface PersistedFilters {
  filter: string;
  statusFilter: string;
}

function loadFilters(): PersistedFilters {
  try {
    const raw = localStorage.getItem(FILTER_KEY);
    if (raw) return { filter: "", statusFilter: "", ...JSON.parse(raw) };
  } catch {
    /* ignore */
  }
  return { filter: "", statusFilter: "" };
}

const RECEIPT_LABEL: Record<string, string> = { returned: "已回收", audited: "已核销", pending: "待追回" };

export function WaybillsPage({ embedded = false }: { embedded?: boolean } = {}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const persisted = useMemo(loadFilters, []);

  // === 多维度查询状态（记忆上次筛选） ===
  const [filter, setFilter] = useState(persisted.filter);
  const [statusFilter, setStatusFilter] = useState(persisted.statusFilter);
  const [model, setModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const wbActiveCount = activeConditionCount(model, WAYBILL_FILTER_FIELDS);

  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);
  const [drawerWaybill, setDrawerWaybill] = useState<Waybill | null>(null);

  // === 批量选择状态 ===
  const [selected, setSelected] = useState<Set<string>>(new Set());

  // 服务端筛选 + 分页 + 排序（高级筛选/搜索/状态药丸全部下沉后端，对全量生效）
  const st = useServerTable<Waybill>({
    queryKey: ["waybills", "table"],
    path: "/waybills",
    pageSize: 50,
    defaultSort: { field: "eta_drift_minutes", dir: "desc" },
    model,
    search: filter,
    extraParams: { status: statusFilter || undefined },
  });
  // 状态药丸计数（服务端全量聚合，不受分页/筛选影响）
  const statsQ = useQuery({
    queryKey: ["waybills", "stats"],
    queryFn: () => apiGet<{ by_status: Record<string, number>; total: number }>("/waybills/stats"),
    refetchInterval: 30000,
  });
  const statusCount = (s: string) => statsQ.data?.by_status?.[s] ?? 0;
  const totalCount = statsQ.data?.total ?? 0;

  // 记忆快速筛选
  useEffect(() => {
    try {
      localStorage.setItem(FILTER_KEY, JSON.stringify({ filter, statusFilter }));
    } catch {
      /* ignore */
    }
  }, [filter, statusFilter]);

  // 全局点击关闭右键菜单 + Esc
  useEffect(() => {
    const handleCloseMenu = () => setContextMenu(null);
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setContextMenu(null);
        setDrawerWaybill(null);
      }
    };
    window.addEventListener("click", handleCloseMenu);
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("click", handleCloseMenu);
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, []);

  const rows = st.rows;

  // 选择集随当前页收敛（批量操作作用于当前页选中项）
  const selectedRows = useMemo(() => rows.filter((w) => selected.has(w.id)), [rows, selected]);

  const toggleOne = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const toggleAll = () => {
    setSelected((prev) => {
      if (rows.length > 0 && rows.every((w) => prev.has(w.id))) {
        const next = new Set(prev);
        rows.forEach((w) => next.delete(w.id));
        return next;
      }
      const next = new Set(prev);
      rows.forEach((w) => next.add(w.id));
      return next;
    });
  };
  const clearSelection = () => setSelected(new Set());

  const invalidateWaybills = () => queryClient.invalidateQueries({ queryKey: ["waybills"] });

  const handleRowContextMenu = (e: React.MouseEvent, w: Waybill) => {
    e.preventDefault();
    setContextMenu({ x: e.clientX, y: e.clientY, waybill: w });
  };

  const openContract = useMutation({
    mutationFn: (no: string) => apiGet<Contract | null>(`/waybills/${no}/contract`),
    onSuccess: (c, no) => {
      if (c?.pdf_url) window.open(c.pdf_url, "_blank");
      else toast.info(`运单 ${no} 暂无可下载的合同/回单文件，请先在运单详情页生成承运合同。`);
    },
  });

  const voidWaybill = useMutation({
    mutationFn: (no: string) => apiPost(`/waybills/${no}/transition`, { to_status: "voided", remark: "手动作废" }),
    onSuccess: (_d, no) => {
      toast.success(`运单 ${no} 已作废，关联运力已释放，可重新调度。`);
      invalidateWaybills();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const handleAction = async (action: string, w: Waybill) => {
    setContextMenu(null);
    if (action === "view") navigate(`/waybills/${w.waybill_no}`);
    else if (action === "track") navigate(`/waybills/${w.waybill_no}`);
    else if (action === "print") openContract.mutate(w.waybill_no);
    else if (action === "cancel") {
      const ok = await confirmAction({
        title: "废弃运单",
        message: `确定要废弃运单 ${w.waybill_no} 吗？关联车辆/司机运力将被释放，此操作不可恢复。`,
        tone: "danger",
        confirmText: "废弃",
      });
      if (ok) voidWaybill.mutate(w.waybill_no);
    }
  };

  // === 批量操作 ===
  const [batchBusy, setBatchBusy] = useState(false);

  const exportCsv = () => {
    const exportRows = selectedRows.length ? selectedRows : rows;
    if (!exportRows.length) return;
    const head = ["运单号", "客户", "起点", "终点", "通道", "车牌", "司机", "应收", "应付/成本", "代收货款", "回单", "状态"];
    const esc = (v: string | number) => `"${String(v ?? "").replace(/"/g, '""')}"`;
    const lines = exportRows.map((w) =>
      [
        w.waybill_no,
        w.customer_name || "散客",
        w.origin || "",
        w.destination || "",
        w.channel || "",
        w.vehicle_plate || w.carrier_name || w.platform_name || "",
        w.driver_name || "",
        w.receivable_amount || 0,
        w.payable_amount || 0,
        Number(w.cod_amount) || 0,
        RECEIPT_LABEL[w.receipt_status] ?? w.receipt_status,
        STATUS_LABEL[w.status] ?? w.status,
      ].map(esc).join(","),
    );
    const csv = "﻿" + [head.map(esc).join(","), ...lines].join("\r\n");
    const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `运单导出_${exportRows.length}条.csv`;
    a.click();
    URL.revokeObjectURL(url);
    toast.success(`已导出 ${exportRows.length} 条运单为 CSV`);
  };

  const batchMarkReceipt = async () => {
    if (!selectedRows.length) return;
    const targets = selectedRows.filter((w) => w.receipt_status !== "returned" && w.receipt_status !== "audited");
    if (!targets.length) {
      toast.info("所选运单回单均已回收，无需处理。");
      return;
    }
    setBatchBusy(true);
    let ok = 0;
    for (const w of targets) {
      try {
        await apiPatch(`/waybills/${w.waybill_no}`, { receipt_status: "returned" });
        ok += 1;
      } catch {
        /* 单条失败不阻断整批 */
      }
    }
    setBatchBusy(false);
    toast.success(`已标记 ${ok}/${targets.length} 条运单回单为「已回收」`);
    clearSelection();
    invalidateWaybills();
  };

  const batchVoid = async () => {
    if (!selectedRows.length) return;
    const okConfirm = await confirmAction({
      title: "批量废弃运单",
      message: `确定要废弃选中的 ${selectedRows.length} 条运单吗？关联运力将被释放，此操作不可恢复。`,
      tone: "danger",
      confirmText: `废弃 ${selectedRows.length} 条`,
    });
    if (!okConfirm) return;
    setBatchBusy(true);
    let ok = 0;
    for (const w of selectedRows) {
      try {
        await apiPost(`/waybills/${w.waybill_no}/transition`, { to_status: "voided", remark: "批量作废" });
        ok += 1;
      } catch {
        /* 忽略单条失败（状态不允许作废等） */
      }
    }
    setBatchBusy(false);
    toast.success(`已作废 ${ok}/${selectedRows.length} 条运单`);
    clearSelection();
    invalidateWaybills();
  };

  const handleClearFilters = () => {
    setFilter("");
    setStatusFilter("");
    setModel(EMPTY_MODEL);
  };

  const columns: DataColumn<Waybill>[] = [
    {
      key: "waybill_no", header: "运单号", width: 150, alwaysVisible: true,
      sortField: "waybill_no", sortValue: (w) => w.waybill_no, exportValue: (w) => w.waybill_no,
      render: (w) => <Link className="link mono" to={`/waybills/${w.waybill_no}`}>{w.waybill_no}</Link>,
    },
    { key: "customer", header: "客户", width: 130, sortField: "customer__name", sortValue: (w) => w.customer_name || "散客", exportValue: (w) => w.customer_name || "散客", render: (w) => <span title={w.customer_name}>{w.customer_name || "散客"}</span> },
    { key: "route", header: "线路", width: 150, sortValue: (w) => `${w.origin}${w.destination}`, exportValue: (w) => `${w.origin || "?"}→${w.destination || "?"}`, render: (w) => <>{w.origin || "?"} → {w.destination || "?"}</> },
    {
      key: "cargo", header: "货物", width: 130, exportValue: (w) => `${w.cargo.weight_ton || 0}吨`,
      render: (w) => <span className="small">{w.cargo.quantity ? `${w.cargo.quantity}件 ` : ""}{w.cargo.weight_ton || 0}吨{w.cargo.volume_cbm ? ` / ${w.cargo.volume_cbm}方` : ""}</span>,
    },
    {
      key: "vehicle", header: "车辆 / 司机", width: 150, exportValue: (w) => w.vehicle_plate || w.carrier_name || w.platform_name || "",
      render: (w) => (
        <span className="small">
          {w.vehicle_plate ? <span className="mono">{w.vehicle_plate}</span> : w.carrier_name || (w.channel === "网货" ? (w.platform_name || "平台") : "—")}
          {w.driver_name && <div className="muted" style={{ fontSize: 11 }}>{w.driver_name} {w.driver_phone}</div>}
        </span>
      ),
    },
    {
      key: "channel", header: "通道", width: 100, filterable: true, filterValue: (w) => w.channel || "", sortValue: (w) => w.channel || "", exportValue: (w) => w.channel || "",
      render: (w) => w.channel ? <StatusTag kind="channel" value={w.channel} title={w.dispatch_type_label} suffix={w.channel === "网货" && w.platform_name ? `·${w.platform_name}` : ""} /> : <span className="muted">—</span>,
    },
    { key: "receivable", header: "应收", width: 100, align: "right", sortField: "receivable_total", sortValue: (w) => w.receivable_amount || 0, exportValue: (w) => w.receivable_amount || 0, render: (w) => <>{w.receivable_amount ? fmtMoney(w.receivable_amount) : "—"}</> },
    { key: "payable", header: "应付/成本", width: 110, align: "right", sortField: "payable_total", sortValue: (w) => w.payable_amount || 0, exportValue: (w) => w.payable_amount || 0, render: (w) => <>{w.payable_amount ? fmtMoney(w.payable_amount) : "—"}</> },
    { key: "cod", header: "代收货款", width: 110, align: "right", sortField: "cod_amount", sortValue: (w) => Number(w.cod_amount) || 0, exportValue: (w) => Number(w.cod_amount) || 0, render: (w) => { const cod = Number(w.cod_amount) || 0; return cod > 0 ? <span style={{ color: "var(--amber)", fontWeight: 600 }}>{fmtMoney(cod)}</span> : <>—</>; } },
    { key: "receipt", header: "回单", width: 90, sortField: "receipt_status", sortValue: (w) => w.receipt_status, exportValue: (w) => RECEIPT_LABEL[w.receipt_status] ?? "待追回", render: (w) => <StatusTag kind="receipt" value={w.receipt_status} /> },
    { key: "status", header: "运单状态", width: 100, sortField: "status", sortValue: (w) => w.status, exportValue: (w) => STATUS_LABEL[w.status] ?? w.status, render: (w) => <StatusTag kind="waybill" value={w.status} /> },
    {
      key: "actions", header: "操作", width: 170, alwaysVisible: true,
      render: (w) => (
        <div className="row-actions" onClick={(e) => e.stopPropagation()}>
          <button onClick={() => navigate(`/waybills/${w.waybill_no}`)}>详情</button>
          <button onClick={() => navigate(`/waybills/${w.waybill_no}`)}>追踪</button>
          <button onClick={(e) => handleRowContextMenu(e, w)} aria-label="更多操作">⋯</button>
        </div>
      ),
    },
  ];

  const batchBar = selectedRows.length > 0 ? (
    <div className="batch-bar">
      <span>已选 <b style={{ color: "var(--accent)" }}>{selectedRows.length}</b> 条</span>
      <div style={{ flex: 1 }} />
      <button className="btn-ghost" disabled={batchBusy} onClick={exportCsv}>导出选中</button>
      <button className="btn-ghost" disabled={batchBusy} onClick={batchMarkReceipt}>标记回单已回收</button>
      <button className="btn-danger-ghost" disabled={batchBusy} onClick={batchVoid}>批量作废</button>
      <button className="btn-ghost" disabled={batchBusy} onClick={clearSelection}>取消选择</button>
    </div>
  ) : null;

  const anyFilter = Boolean(filter) || Boolean(statusFilter) || wbActiveCount > 0;

  const body = (
    <>
      <div className="panel om-panel" style={{ overflow: "visible" }}>
        {wbActiveCount > 0 && (
          <div className="om-chips">
            <span className="muted small">条件（{model.combinator === "and" ? "全部满足" : "任一满足"}）：</span>
            {model.conditions.map((c) => {
              const label = describeCondition(c, WAYBILL_FILTER_FIELDS);
              if (!label) return null;
              return <span key={c.id} className="filter-chip">{label}<button onClick={() => setModel((m) => ({ ...m, conditions: m.conditions.filter((x) => x.id !== c.id) }))}>×</button></span>;
            })}
            <button className="linkish small" onClick={() => setModel(EMPTY_MODEL)}>清空条件</button>
          </div>
        )}

        {/* 快速状态药丸栏（服务端全量计数与过滤）*/}
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8, padding: "10px 14px", borderBottom: "1px solid var(--line)", background: "var(--input-bg)" }}>
          <button className={`chip${statusFilter === "" ? " chip-on" : ""}`} onClick={() => setStatusFilter("")}>全部在单 ({totalCount})</button>
          {STATUS_CHIPS.map((s) => (
            <button key={s} className={`chip${statusFilter === s ? " chip-on" : ""}`} onClick={() => setStatusFilter(statusFilter === s ? "" : s)}>
              {STATUS_LABEL[s] ?? s} ({statusCount(s)})
            </button>
          ))}
        </div>

        {st.isError ? (
          <div className="empty-state">
            <div className="empty-title">加载失败</div>
            <button className="btn-ghost" onClick={() => st.refetch()} style={{ marginTop: 10 }}>重试</button>
          </div>
        ) : (
          <DataTable<Waybill>
            columns={columns}
            rows={rows}
            rowKey={(w) => w.id}
            viewKey="waybills"
            exportName="运单"
            selectable
            selected={selected}
            onToggle={toggleOne}
            onToggleAll={toggleAll}
            onRowContextMenu={handleRowContextMenu}
            onRowDoubleClick={setDrawerWaybill}
            rowClassName={() => "waybill-tr"}
            stickyFirst
            server={st.server}
            fill
            hideExport
            batchBar={batchBar}
            emptyState={
              <div className="empty-state">
                <div className="empty-title">未找到运单数据</div>
                <div className="empty-hint muted small">{anyFilter ? "未查找到符合当前筛选组合的运单，可尝试清空筛选。" : "暂无运单。"}</div>
              </div>
            }
            toolbarLeft={
              <>
                <span className="om-title" style={{ marginRight: 2 }}>运单<span className="ai-pill">{st.total}</span></span>
                <input className="search" style={{ minWidth: 180, flex: 1, maxWidth: 280 }} placeholder="搜索单号/线路/车牌/客户" value={filter} onChange={(e) => setFilter(e.target.value)} />
                <div style={{ position: "relative" }}>
                  <button className={`btn-ghost${wbActiveCount > 0 || showAdvanced ? " on-accent" : ""}`} onClick={(e) => { e.stopPropagation(); setShowAdvanced((v) => !v); }}>
                    <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 5h18l-7 8v5l-4 2v-7z" /></svg>
                      高级筛选{wbActiveCount > 0 ? ` · ${wbActiveCount}` : ""}
                    </span>
                  </button>
                  {showAdvanced && <FilterBuilder fields={WAYBILL_FILTER_FIELDS} model={model} onChange={setModel} onClose={() => setShowAdvanced(false)} />}
                </div>
              </>
            }
            toolbarRight={
              <>
                {anyFilter && <button className="linkish small" onClick={handleClearFilters}>重置</button>}
                <button className="btn-ghost" onClick={() => navigate("/intake")}>+ 新建订单</button>
              </>
            }
          />
        )}
      </div>

      {/* 右键快捷菜单 */}
      {contextMenu && (
        <div className="context-menu-wrapper" style={{ top: contextMenu.y, left: contextMenu.x }} onClick={(e) => e.stopPropagation()}>
          <div style={{ padding: "4px 8px 8px", fontSize: 11, fontWeight: "bold", color: "var(--muted)" }}>
            运单 {contextMenu.waybill.waybill_no}
          </div>
          <button onClick={() => handleAction("view", contextMenu.waybill)}>
            <span>查看详情</span> <span className="hotkey">↵</span>
          </button>
          <button onClick={() => handleAction("track", contextMenu.waybill)}><span>在途追踪</span></button>
          <div className="context-divider"></div>
          <button disabled={openContract.isPending} onClick={() => handleAction("print", contextMenu.waybill)}>
            <span>查看合同/回单 PDF</span>
          </button>
          <div className="context-divider"></div>
          <button disabled={voidWaybill.isPending} onClick={() => handleAction("cancel", contextMenu.waybill)} style={{ color: "var(--red)" }}>
            <span>作废运单</span> <span className="hotkey">⌫</span>
          </button>
        </div>
      )}

      {/* 双击侧滑详情抽屉（Precision Graphite） */}
      {drawerWaybill && (
        <div className="wb-overlay" onClick={() => setDrawerWaybill(null)}>
          <div className="wb-drawer" onClick={(e) => e.stopPropagation()}>
            <div className="wb-drawer-head">
              <div>
                <div className="mono" style={{ fontSize: 15, fontWeight: 650 }}><CopyCode value={drawerWaybill.waybill_no} /></div>
                <div className="muted small" style={{ marginTop: 2 }}>{drawerWaybill.origin || "?"} → {drawerWaybill.destination || "?"}</div>
              </div>
              <button className="btn-ghost" onClick={() => setDrawerWaybill(null)}>关闭 [Esc]</button>
            </div>
            <div className="wb-drawer-body">
              <div className="stack" style={{ gap: 14 }}>
                <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                  <StatusTag kind="waybill" value={drawerWaybill.status} />
                  {drawerWaybill.channel && (
                    <span className={`tag ${CHANNEL_TAG[drawerWaybill.channel] ?? "tag-none"}`}>
                      {drawerWaybill.channel}{drawerWaybill.channel === "网货" && drawerWaybill.platform_name ? `·${drawerWaybill.platform_name}` : ""}
                    </span>
                  )}
                  <span className={`tag tag-${drawerWaybill.receipt_status === "returned" || drawerWaybill.receipt_status === "audited" ? "low" : "none"}`}>
                    回单{RECEIPT_LABEL[drawerWaybill.receipt_status] ?? "待追回"}
                  </span>
                </div>

                <div className="kv">
                  <div><span>签约客户</span><b>{drawerWaybill.customer_name || "散客"}</b></div>
                  <div><span>承运方式</span><b>{drawerWaybill.dispatch_type_label || "—"}</b></div>
                  <div><span>车辆车牌</span><b className="mono">{drawerWaybill.vehicle_plate || "—"}</b></div>
                  <div><span>司机</span><b>{drawerWaybill.driver_name ? `${drawerWaybill.driver_name} ${drawerWaybill.driver_phone}` : "—"}</b></div>
                  <div><span>货物</span><b>{drawerWaybill.cargo.quantity ? `${drawerWaybill.cargo.quantity}件 ` : ""}{drawerWaybill.cargo.weight_ton || 0}吨{drawerWaybill.cargo.volume_cbm ? ` / ${drawerWaybill.cargo.volume_cbm}方` : ""}</b></div>
                </div>

                <div className="section-label">费用</div>
                <div className="kv">
                  <div><span>应收</span><b className="num">{drawerWaybill.receivable_amount ? fmtMoney(drawerWaybill.receivable_amount) : "—"}</b></div>
                  <div><span>应付 / 成本</span><b className="num">{drawerWaybill.payable_amount ? fmtMoney(drawerWaybill.payable_amount) : "—"}</b></div>
                  <div><span>代收货款</span><b className="num" style={{ color: Number(drawerWaybill.cod_amount) > 0 ? "var(--amber)" : undefined }}>{Number(drawerWaybill.cod_amount) > 0 ? fmtMoney(drawerWaybill.cod_amount) : "—"}</b></div>
                </div>

                <div className="section-label">单票财务卡</div>
                <FinanceCard waybillNo={drawerWaybill.waybill_no} />

                <div className="section-label">客户回复</div>
                <ReplyCard waybillNo={drawerWaybill.waybill_no} />
              </div>
            </div>
            <div style={{ display: "flex", gap: 10, padding: "14px 18px", borderTop: "1px solid var(--line)" }}>
              <button className="btn-primary" style={{ flex: 1 }} onClick={() => { setDrawerWaybill(null); navigate(`/waybills/${drawerWaybill.waybill_no}`); }}>
                查看完整详情
              </button>
              <button className="btn-ghost" style={{ flex: 1 }} onClick={() => { setDrawerWaybill(null); navigate(`/waybills/${drawerWaybill.waybill_no}`); }}>
                在途追踪
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );

  // 嵌入订单管理时由父级 stack.table-page 提供固定布局；独立路由自带固定布局外壳
  return embedded ? body : <div className="stack table-page" style={{ position: "relative" }}>{body}</div>;
}
