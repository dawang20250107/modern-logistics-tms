import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { apiGet } from "../api/client";
import { useModalA11y } from "../api/useModalA11y";
import { fmtRelative } from "../api/format";
import type { Order, Paginated } from "../api/types";
import { BUSINESS_TYPE_LABEL, ORDER_CHANNEL_LABEL, ORDER_STATUS_LABEL, PRIORITY_LABEL } from "../api/types";
import { useServerTable } from "../api/useServerTable";
import { CustomerContextPanel } from "../components/CustomerContextPanel";
import { DataTable, type DataColumn } from "../components/DataTable";
import { ExceptionRegisterModal } from "../components/ExceptionRegisterModal";
import { FilterBuilder, activeConditionCount, describeCondition, EMPTY_MODEL, type FilterFieldDef, type FilterModel } from "../components/FilterBuilder";
import { OrderLifecycle } from "../components/OrderLifecycle";
import { StateView } from "../components/StateView";
import { StatusTag } from "../components/StatusTag";
import { StructuredOrderForm } from "../components/StructuredOrderForm";

const enumOpts = (m: Record<string, string>) => Object.entries(m).map(([value, label]) => ({ value, label }));

const CS_POOL_FILTER_FIELDS: FilterFieldDef[] = [
  { key: "order_no", label: "订单号", type: "text", accessor: (o) => (o as Order).order_no },
  { key: "customer", label: "客户", type: "text", accessor: (o) => (o as Order).customer_name || "" },
  { key: "route", label: "线路", type: "text", accessor: (o) => `${(o as Order).origin || ""}→${(o as Order).destination || ""}` },
  { key: "status", label: "订单状态", type: "enum", options: enumOpts(ORDER_STATUS_LABEL), accessor: (o) => (o as Order).status },
  { key: "channel", label: "渠道", type: "enum", options: enumOpts(ORDER_CHANNEL_LABEL), accessor: (o) => (o as Order).channel },
  { key: "business_type", label: "业务类型", type: "enum", options: enumOpts(BUSINESS_TYPE_LABEL), accessor: (o) => (o as Order).business_type },
  { key: "priority", label: "优先级", type: "enum", options: enumOpts(PRIORITY_LABEL), accessor: (o) => (o as Order).priority },
  { key: "exception", label: "异常", type: "enum", options: [{ value: "1", label: "有异常" }, { value: "0", label: "无异常" }], accessor: (o) => ((o as Order).exception_count ?? 0) > 0 ? "1" : "0" },
  { key: "created_at", label: "建单时间", type: "date", accessor: (o) => (o as Order).created_at },
];

// 客服订单池：与全站表格能力对齐的 DataTable（服务端搜索/高级筛选/排序/分页）。
// 双击行/右键可「登记异常」，同步调度与订单管理。
function CsOrderPool() {
  const queryClient = useQueryClient();
  const [excOrder, setExcOrder] = useState<Order | null>(null);
  const [onlyException, setOnlyException] = useState(false);
  const [search, setSearch] = useState("");
  const [model, setModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showBuilder, setShowBuilder] = useState(false);

  const activeCount = activeConditionCount(model, CS_POOL_FILTER_FIELDS);
  // 「仅看异常」下沉服务端：在筛选模型上附加异常条件（AND 语义）
  const effectiveModel = useMemo<FilterModel>(() => {
    if (!onlyException) return model;
    return {
      combinator: "and",
      conditions: [
        { id: "__exc", field: "exception", op: "in", value: ["1"] },
        ...(model.combinator === "and" ? model.conditions : []),
      ],
    };
  }, [onlyException, model]);

  const st = useServerTable<Order>({
    queryKey: ["cs-order-pool"],
    path: "/orders",
    pageSize: 50,
    defaultSort: { field: "created_at", dir: "desc" },
    model: effectiveModel,
    search,
  });
  const anyFilter = Boolean(search.trim()) || onlyException || activeCount > 0;

  const columns: DataColumn<Order>[] = [
    { key: "order_no", header: "订单号 (DD)", width: 172, alwaysVisible: true, sortField: "order_no", sortValue: (o) => o.order_no, exportValue: (o) => o.order_no, render: (o) => <Link className="mono small doc-order" to={`/orders/${o.id}`} onClick={(e) => e.stopPropagation()}>{o.order_no}</Link> },
    { key: "customer", header: "客户", width: 170, sortField: "customer__name", sortValue: (o) => o.customer_name || "", exportValue: (o) => o.customer_name || "散客", render: (o) => (
      <span className="small">{o.customer_name || "散客"}{(o.exception_count ?? 0) > 0 && <span className={`tag tag-${o.exception_level === "high" ? "high" : o.exception_level === "low" ? "low" : "medium"}`} style={{ marginLeft: 4 }} title="未闭环异常">⚠ 异常{(o.exception_count ?? 0) > 1 ? `×${o.exception_count}` : ""}</span>}</span>
    ) },
    { key: "route", header: "线路", width: 150, sortValue: (o) => `${o.origin}${o.destination}`, exportValue: (o) => `${o.origin}→${o.destination}`, render: (o) => <span className="small"><b>{o.origin}</b> → <b>{o.destination}</b></span> },
    { key: "channel", header: "渠道", width: 100, sortField: "channel", exportValue: (o) => ORDER_CHANNEL_LABEL[o.channel] ?? o.channel, render: (o) => <span className="small muted">{ORDER_CHANNEL_LABEL[o.channel] ?? o.channel}</span> },
    { key: "status", header: "订单状态", width: 116, sortField: "status", sortValue: (o) => o.status, exportValue: (o) => ORDER_STATUS_LABEL[o.status] ?? o.status, render: (o) => <StatusTag kind="order" value={o.status} /> },
    { key: "created_at", header: "建单", width: 108, sortField: "created_at", sortValue: (o) => o.created_at, exportValue: (o) => o.created_at, render: (o) => <span className="small muted" title={o.created_at}>{fmtRelative(o.created_at)}</span> },
    { key: "act", header: "操作", width: 120, alwaysVisible: true, sticky: "right", render: (o) => (
      <div className="row-actions" onClick={(e) => e.stopPropagation()}>
        <button onClick={() => setExcOrder(o)}>登记异常</button>
        <Link className="link small" to={`/orders/${o.id}`}>详情</Link>
      </div>
    ) },
  ];

  const rowMenu = (o: Order) => [
    { label: "登记异常", onClick: () => setExcOrder(o) },
    { label: "订单详情", onClick: () => { window.location.href = `/orders/${o.id}`; } },
  ];

  return (
    <div className="panel om-panel">
      {activeCount > 0 && (
        <div className="om-chips">
          <span className="muted small">条件（{model.combinator === "and" ? "全部满足" : "任一满足"}）：</span>
          {model.conditions.map((c) => {
            const label = describeCondition(c, CS_POOL_FILTER_FIELDS);
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
          columns={columns} rows={st.rows} rowKey={(o) => o.id} viewKey="cs-order-pool" exportName="客服订单池"
          stickyFirst server={st.server} fill hideExport rowMenu={rowMenu} onRowDoubleClick={(o) => setExcOrder(o)}
          emptyState={anyFilter
            ? <StateView kind="empty" title="没有匹配的订单" hint="调整筛选条件再试。" />
            : <StateView kind="empty" title="暂无订单" hint="在「建单工作台」建单后将在此跟进。" />}
          toolbarLeft={
            <>
              <span className="om-title" style={{ marginRight: 2 }}>订单池<span className="ai-pill">{st.total}</span></span>
              <input className="search" style={{ minWidth: 180, flex: 1, maxWidth: 300 }} placeholder="搜索 订单号 / 客户 / 线路" value={search} onChange={(e) => setSearch(e.target.value)} />
              <div style={{ position: "relative" }}>
                <button className={`btn-ghost${activeCount > 0 || showBuilder ? " on-accent" : ""}`} onClick={(e) => { e.stopPropagation(); setShowBuilder((v) => !v); }}>
                  <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 5h18l-7 8v5l-4 2v-7z" /></svg>
                    高级筛选{activeCount > 0 ? ` · ${activeCount}` : ""}
                  </span>
                </button>
                {showBuilder && <FilterBuilder fields={CS_POOL_FILTER_FIELDS} model={model} onChange={setModel} onClose={() => setShowBuilder(false)} />}
              </div>
              <button className={`chip${onlyException ? " chip-on" : ""}`} onClick={() => setOnlyException((v) => !v)}>仅看异常</button>
            </>
          }
          toolbarRight={<Link className="link small" to="/waybills">去订单管理 →</Link>}
        />
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

// 客服工作台：Tab 切换「建单工作台」（流转纵览 + 全宽建单 + 客户上下文）与「订单池」（登记异常 + 顶级筛选）。
export function OrderIntakePage() {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<"build" | "pool">("build");
  const [ctxCustomer, setCtxCustomer] = useState("");
  const [showCtx, setShowCtx] = useState(false);
  // Tab 计数徽标：服务端全量计数（page_size=1 仅取 total）
  const poolQ = useQuery({
    queryKey: ["cs-order-pool-count"],
    queryFn: () => apiGet<Paginated<Order>>("/orders?page_size=1"),
    refetchInterval: 30000,
  });
  const poolCount = poolQ.data?.total ?? 0;

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["orders"] });
    queryClient.invalidateQueries({ queryKey: ["cs-order-pool"] });
  };

  const ctxRef = useRef<HTMLDivElement>(null);
  useModalA11y(showCtx && Boolean(ctxCustomer), ctxRef, () => setShowCtx(false));

  return (
    <div className={`stack${tab === "pool" ? " table-page" : ""}`}>
      <div className="cs-tabbar">
        <div className="seg-tabs">
          <button className={tab === "build" ? "active" : ""} onClick={() => setTab("build")}>建单工作台</button>
          <button className={tab === "pool" ? "active" : ""} onClick={() => setTab("pool")}>订单池{poolCount ? <span className="ai-pill" style={{ marginLeft: 6 }}>{poolCount}</span> : null}</button>
        </div>
        <div style={{ flex: 1 }} />
        <Link className="link small" to="/waybills">订单管理（全量台账）→</Link>
      </div>

      {tab === "build" && (
        <>
          <OrderLifecycle />
          <StructuredOrderForm
            onCreated={invalidate}
            onCustomerChange={(id) => { setCtxCustomer(id); setShowCtx(Boolean(id)); }}
          />
          {ctxCustomer && !showCtx && (
            <button className="ctx-reopen" onClick={() => setShowCtx(true)}>
              查看已选客户上下文（账期/授信/常用线路/未完成单）
            </button>
          )}
        </>
      )}

      {tab === "pool" && <CsOrderPool />}

      {showCtx && ctxCustomer && (
        <div className="modal-overlay" onClick={() => setShowCtx(false)}>
          <div ref={ctxRef} tabIndex={-1} role="dialog" aria-modal="true" aria-label="客户上下文" className="modal-card" onClick={(e) => e.stopPropagation()}>
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
