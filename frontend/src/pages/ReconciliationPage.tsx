import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useRef, useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { useModalA11y } from "../api/useModalA11y";
import { fmtDateTime, fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import type {
  AgingReport, Carrier, Customer, Paginated, Statement, StatementAuditResult,
  StatementOverview, StatementPayment,
} from "../api/types";
import { PAYMENT_METHOD_LABEL, STATEMENT_STATUS_LABEL } from "../api/types";
import { DataTable, type DataColumn } from "../components/DataTable";
import { CopyCode } from "../components/CopyCode";
import { FilterBuilder, activeConditionCount, describeCondition, EMPTY_MODEL, type FilterFieldDef, type FilterModel } from "../components/FilterBuilder";
import { useServerTable } from "../api/useServerTable";
import { StateView } from "../components/StateView";

type Tab = "overview" | "statements" | "aging" | "settle";
const num = (v: unknown) => Number(v) || 0;

const STMT_FILTER_FIELDS: FilterFieldDef[] = [
  { key: "no", label: "结算单号", type: "text", accessor: (s) => (s as Statement).statement_no },
  { key: "cp", label: "对手方", type: "text", accessor: (s) => (s as Statement).counterparty_name },
  { key: "dir", label: "方向", type: "enum", options: [{ value: "receivable", label: "应收(AR)" }, { value: "payable", label: "应付(AP)" }], accessor: (s) => (s as Statement).direction },
  { key: "status", label: "状态", type: "enum", options: Object.entries(STATEMENT_STATUS_LABEL).map(([value, label]) => ({ value, label })), accessor: (s) => (s as Statement).status },
  { key: "amt", label: "应结金额", type: "number", accessor: (s) => num((s as Statement).total_amount) },
  { key: "out", label: "未结余额", type: "number", accessor: (s) => num((s as Statement).outstanding) },
  { key: "diff", label: "差异额", type: "number", accessor: (s) => Math.abs(num((s as Statement).diff)) },
];

const STATUS_TAG: Record<string, string> = { draft: "tag-none", confirmed: "tag-info", partial: "tag-medium", settled: "tag-low" };

// ══════════════════════════ 收付款核销弹窗 ══════════════════════════
function SettleModal({ statement, onClose, onDone }: { statement: Statement; onClose: () => void; onDone: () => void }) {
  const outstanding = num(statement.outstanding);
  const [amount, setAmount] = useState(String(outstanding.toFixed(2)));
  const [method, setMethod] = useState("bank");
  const [paidAt, setPaidAt] = useState(new Date().toISOString().slice(0, 10));
  const [ref, setRef] = useState("");
  const [remark, setRemark] = useState("");
  const isAR = statement.direction === "receivable";
  const cardRef = useRef<HTMLDivElement>(null);
  useModalA11y(true, cardRef, onClose);

  const settle = useMutation({
    mutationFn: () => apiPost(`/finance/statements/${statement.id}/settle`, {
      amount, method, paid_at: paidAt, reference_no: ref, remark,
    }),
    onSuccess: () => { toast.success(`${isAR ? "收款" : "付款"}核销成功：${fmtMoney(amount)}`); onDone(); onClose(); },
    onError: (e: Error) => toast.error(e.message),
  });

  const amt = num(amount);
  const invalid = amt <= 0 || amt > outstanding + 0.01;

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div ref={cardRef} className="modal-card" onClick={(e) => e.stopPropagation()} tabIndex={-1}
        onKeyDown={(e) => { if ((e.ctrlKey || e.metaKey) && e.key === "Enter" && !invalid && !settle.isPending) { e.preventDefault(); settle.mutate(); } }}
      >
        <div className="modal-head">
          <div>
            <div style={{ fontWeight: 700, fontSize: 15 }}>{isAR ? "登记收款核销" : "登记付款核销"}</div>
            <div className="muted small" style={{ marginTop: 3 }}>{statement.statement_no} · {statement.counterparty_name}</div>
          </div>
          <button className="linkish" style={{ fontSize: 20, lineHeight: 1 }} onClick={onClose}>×</button>
        </div>
        <div className="settle-summary">
          <div><span>应结金额</span><b>{fmtMoney(statement.total_amount)}</b></div>
          <div><span>已核销</span><b>{fmtMoney(statement.settled_amount)}</b></div>
          <div><span>未结余额</span><b style={{ color: "var(--accent)" }}>{fmtMoney(outstanding)}</b></div>
        </div>
        <div className="grid-form" style={{ padding: "14px 18px" }}>
          <label>本次{isAR ? "收款" : "付款"}金额
            <div style={{ display: "flex", gap: 6 }}>
              <input autoFocus inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="0.00" style={{ flex: 1 }} />
              <button className="btn-ghost" style={{ padding: "0 10px", fontSize: 12 }} onClick={() => setAmount(outstanding.toFixed(2))}>全额</button>
            </div>
          </label>
          <label>{isAR ? "收款" : "付款"}方式
            <select value={method} onChange={(e) => setMethod(e.target.value)}>
              {Object.entries(PAYMENT_METHOD_LABEL).map(([v, l]) => <option key={v} value={v}>{l}</option>)}
            </select>
          </label>
          <label>{isAR ? "收款" : "付款"}日期<input type="date" value={paidAt} onChange={(e) => setPaidAt(e.target.value)} /></label>
          <label>凭证/流水号<input value={ref} onChange={(e) => setRef(e.target.value)} placeholder="银行流水号 / 承兑号" /></label>
          <label style={{ gridColumn: "1 / -1" }}>备注<input value={remark} onChange={(e) => setRemark(e.target.value)} placeholder="部分付款说明 / 尾款约定等" /></label>
        </div>
        {amt > outstanding + 0.01 && (
          <div className="settle-hint" style={{ color: "var(--red)" }}>本次金额超过未结余额 <b>{fmtMoney(outstanding)}</b>，请调整（不可超额核销）。</div>
        )}
        {amt > 0 && amt < outstanding - 0.01 && (
          <div className="settle-hint">部分核销后仍剩 <b>{fmtMoney(outstanding - amt)}</b> 未结，单据将标记「部分结算」。</div>
        )}
        <div className="modal-actions" style={{ padding: "12px 18px", borderTop: "1px solid var(--line)" }}>
          <button className="btn-ghost" onClick={onClose}>取消</button>
          <button className="btn-primary" disabled={invalid || settle.isPending} onClick={() => settle.mutate()}
            title={amt <= 0 ? "请填写核销金额" : amt > outstanding + 0.01 ? "金额不可超过未结余额" : "Ctrl+Enter 提交"}>
            {settle.isPending ? "核销中…" : `确认${isAR ? "收款" : "付款"}`}
          </button>
        </div>
      </div>
    </div>
  );
}

// ══════════════════════════ 对账总览看板 ══════════════════════════
function OverviewTab() {
  const ov = useQuery({ queryKey: ["stmt-overview"], queryFn: () => apiGet<StatementOverview>("/finance/statement-overview") });
  if (ov.isLoading) return <StateView kind="loading" compact />;
  if (ov.isError || !ov.data) return <StateView kind="error" onRetry={() => ov.refetch()} />;
  const d = ov.data;
  const collectRate = d.receivable.total > 0 ? d.receivable.settled / d.receivable.total : 0;
  const payRate = d.payable.total > 0 ? d.payable.settled / d.payable.total : 0;

  const StatusChips = ({ s }: { s: StatementOverview["receivable"] }) => (
    <div className="ov-status">
      <span className="tag tag-none">草稿 {s.draft}</span>
      <span className="tag tag-info">已确认 {s.confirmed}</span>
      <span className="tag tag-medium">部分 {s.partial}</span>
      <span className="tag tag-low">已结 {s.settled_count}</span>
    </div>
  );

  return (
    <div className="stack">
      <div className="ov-kpis">
        <div className="ov-card ov-ar">
          <div className="ov-label">应收未结（AR）</div>
          <div className="ov-value">{fmtMoney(d.receivable.outstanding)}</div>
          <div className="ov-sub">应收合计 {fmtMoney(d.receivable.total)} · 已收 {(collectRate * 100).toFixed(0)}%</div>
          <div className="ov-foot">
            <div className="ov-bar"><div style={{ width: `${Math.min(collectRate * 100, 100)}%`, background: "var(--green)" }} /></div>
            <StatusChips s={d.receivable} />
          </div>
        </div>
        <div className="ov-card ov-ap">
          <div className="ov-label">应付未结（AP）</div>
          <div className="ov-value">{fmtMoney(d.payable.outstanding)}</div>
          <div className="ov-sub">应付合计 {fmtMoney(d.payable.total)} · 已付 {(payRate * 100).toFixed(0)}%</div>
          <div className="ov-foot">
            <div className="ov-bar"><div style={{ width: `${Math.min(payRate * 100, 100)}%`, background: "var(--amber)" }} /></div>
            <StatusChips s={d.payable} />
          </div>
        </div>
        <div className="ov-card ov-net">
          <div className="ov-label">净头寸（应收未结 − 应付未结）</div>
          <div className="ov-value" style={{ color: d.net_position >= 0 ? "var(--green)" : "var(--red)" }}>{fmtMoney(d.net_position)}</div>
          <div className="ov-sub">{d.net_position >= 0 ? "净应收，现金流向好" : "净应付，需备付资金"}</div>
          <div className="ov-split ov-foot">
            <div><span>本期新增单据</span><b>{d.period.count} 张</b></div>
            <div><span>本期应收</span><b>{fmtMoney(d.period.receivable)}</b></div>
            <div><span>本期应付</span><b>{fmtMoney(d.period.payable)}</b></div>
          </div>
        </div>
        <div className={`ov-card ov-overdue${(d.overdue.receivable.amount + d.overdue.payable.amount) > 0 ? " on" : ""}`}>
          <div className="ov-label">逾期敞口（已过约定到期日）</div>
          <div className="ov-value" style={{ color: (d.overdue.receivable.amount + d.overdue.payable.amount) > 0 ? "var(--red)" : "var(--muted)" }}>
            {fmtMoney(d.overdue.receivable.amount + d.overdue.payable.amount)}
          </div>
          <div className="ov-sub">{(d.overdue.receivable.amount + d.overdue.payable.amount) > 0 ? "需重点催收/排款" : "无逾期，账期健康"}</div>
          <div className="ov-split ov-foot">
            <div><span>逾期应收</span><b style={d.overdue.receivable.amount > 0 ? { color: "var(--red)" } : {}}>{fmtMoney(d.overdue.receivable.amount)}（{d.overdue.receivable.count}）</b></div>
            <div><span>逾期应付</span><b style={d.overdue.payable.amount > 0 ? { color: "var(--red)" } : {}}>{fmtMoney(d.overdue.payable.amount)}（{d.overdue.payable.count}）</b></div>
          </div>
        </div>
      </div>

      <div className="ct-grid" style={{ gridTemplateColumns: "1fr 1fr" }}>
        {([["应收 Top 对手方（未结）", d.top_receivable, "var(--green)"], ["应付 Top 对手方（未结）", d.top_payable, "var(--amber)"]] as const).map(([title, rows, color]) => (
          <div className="panel" key={title}>
            <div className="panel-head">{title}</div>
            {rows.length === 0 ? (
              <div className="pad muted small">暂无未结敞口。</div>
            ) : (
              <div className="top-list">
                {rows.map((r, i) => {
                  const max = rows[0].outstanding || 1;
                  return (
                    <div className="top-row" key={r.counterparty_id || i}>
                      <span className="top-rank">{i + 1}</span>
                      <span className="top-name">{r.counterparty_name || "—"}<span className="muted small">（{r.count} 张）</span></span>
                      <span className="top-bar"><i style={{ width: `${(r.outstanding / max) * 100}%`, background: color }} /></span>
                      <b className="top-amt">{fmtMoney(r.outstanding)}</b>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

// ══════════════════════════ 账龄分析 ══════════════════════════
function AgingTab() {
  const [dir, setDir] = useState<"receivable" | "payable">("receivable");
  const aging = useQuery({ queryKey: ["aging", dir], queryFn: () => apiGet<AgingReport>(`/finance/aging?direction=${dir}`) });
  const rows = aging.data?.rows ?? [];
  const totals = aging.data?.totals;
  const isAR = dir === "receivable";

  const cell = (v: number, danger = false) => v > 0
    ? <span style={danger ? { color: "var(--red)", fontWeight: 700 } : { fontWeight: 600 }}>{fmtMoney(v)}</span>
    : <span className="muted">—</span>;

  const cols: DataColumn<AgingReport["rows"][number]>[] = [
    { key: "cp", header: isAR ? "客户" : "承运商", width: 180, alwaysVisible: true, sortValue: (r) => r.counterparty_name, exportValue: (r) => r.counterparty_name, render: (r) => <span style={{ fontWeight: 600 }}>{r.counterparty_name || "—"}</span> },
    { key: "b0", header: "0-30 天", width: 120, align: "right", sortValue: (r) => r.b0_30, exportValue: (r) => r.b0_30, render: (r) => cell(r.b0_30) },
    { key: "b1", header: "31-60 天", width: 120, align: "right", sortValue: (r) => r.b31_60, exportValue: (r) => r.b31_60, render: (r) => cell(r.b31_60) },
    { key: "b2", header: "61-90 天", width: 120, align: "right", sortValue: (r) => r.b61_90, exportValue: (r) => r.b61_90, render: (r) => cell(r.b61_90, true) },
    { key: "b3", header: "90 天以上", width: 120, align: "right", sortValue: (r) => r.b90, exportValue: (r) => r.b90, render: (r) => cell(r.b90, true) },
    { key: "total", header: "合计", width: 130, align: "right", sortValue: (r) => r.total, exportValue: (r) => r.total, render: (r) => <span style={{ fontWeight: 700 }}>{fmtMoney(r.total)}</span> },
  ];

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head" style={{ gap: 12 }}>
          <span>账龄分析</span>
          <div className="seg-tabs">
            <button className={isAR ? "active" : ""} onClick={() => setDir("receivable")}>应收账龄</button>
            <button className={!isAR ? "active" : ""} onClick={() => setDir("payable")}>应付账龄</button>
          </div>
          <div style={{ flex: 1 }} />
          <span className="muted small">按费用发生日至今分桶 · 61 天以上重点关注</span>
        </div>
        {totals && (
          <div className="aging-totals">
            {([["0-30 天", totals.b0_30, false], ["31-60 天", totals.b31_60, false], ["61-90 天", totals.b61_90, true], ["90 天+", totals.b90, true], ["合计", totals.total, false]] as const).map(([label, val, danger]) => (
              <div className="aging-tot" key={label}>
                <span className="muted small">{label}</span>
                <b style={danger && val > 0 ? { color: "var(--red)" } : {}}>{fmtMoney(val)}</b>
                {totals.total > 0 && label !== "合计" && <i className="aging-pct">{((val / totals.total) * 100).toFixed(0)}%</i>}
              </div>
            ))}
          </div>
        )}
        {aging.isLoading ? (
          <StateView kind="loading" compact />
        ) : aging.isError ? (
          <StateView kind="error" onRetry={() => aging.refetch()} />
        ) : rows.length === 0 ? (
          <StateView kind="empty" title="暂无账龄数据" hint="生成费用与对账单后，此处按对手方汇总账龄敞口。" />
        ) : (
          <DataTable
            columns={cols}
            rows={rows}
            rowKey={(r) => r.counterparty_id}
            viewKey={`aging-${dir}`}
            exportName={`账龄-${isAR ? "应收" : "应付"}`}
            stickyFirst
            toolbarLeft={<span className="muted small">共 {rows.length} 个对手方 · 点表头排序</span>}
          />
        )}
      </div>
    </div>
  );
}

// ══════════════════════════ 主页面 ══════════════════════════
export function ReconciliationPage() {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<Tab>("overview");
  const [direction, setDirection] = useState<"receivable" | "payable">("receivable");
  const [counterpartyId, setCounterpartyId] = useState("");
  const [start, setStart] = useState("2026-06-01");
  const [end, setEnd] = useState("2026-06-30");
  const [dueDate, setDueDate] = useState("");
  const [externalTotal, setExternalTotal] = useState("");
  const [expanded, setExpanded] = useState<string>("");
  const [stmtModel, setStmtModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showStmtFilter, setShowStmtFilter] = useState(false);
  const [showGen, setShowGen] = useState(false);
  const [settleTarget, setSettleTarget] = useState<Statement | null>(null);

  const cpType = direction === "receivable" ? "customer" : "carrier";
  const counterparties = useQuery({
    queryKey: ["cp", cpType],
    queryFn: () => apiGet<Paginated<Customer | Carrier>>(`/${cpType === "customer" ? "customers" : "carriers"}?page_size=200`),
  });
  // 对账单台账：服务端筛选 + 分页 + 排序（对全量生效）
  const st = useServerTable<Statement>({
    queryKey: ["statements"],
    path: "/finance/statements",
    pageSize: 50,
    defaultSort: { field: "created_at", dir: "desc" },
    model: stmtModel,
  });
  // 收付款核销队列：所有已确认/部分结算且有未结余额（服务端筛选，不受台账分页影响）
  const SETTLE_FILTER = encodeURIComponent(JSON.stringify({ combinator: "and", conditions: [
    { field: "status", op: "in", value: ["confirmed", "partial"] },
    { field: "out", op: "gt", value: "0.01" },
  ] }));
  const settleQ = useQuery({
    queryKey: ["settle-queue"],
    queryFn: () => apiGet<Paginated<Statement>>(`/finance/statements?page_size=200&filter=${SETTLE_FILTER}`),
  });
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["statements"] });
    queryClient.invalidateQueries({ queryKey: ["settle-queue"] });
    queryClient.invalidateQueries({ queryKey: ["stmt-overview"] });
    queryClient.invalidateQueries({ queryKey: ["aging"] });
  };

  const generate = useMutation({
    mutationFn: () =>
      apiPost<Statement>("/finance/statements/generate", {
        direction, counterparty_type: cpType, counterparty_id: counterpartyId,
        period_start: start, period_end: end, due_date: dueDate || null, external_total: externalTotal || 0,
      }),
    onSuccess: (s) => { toast.success(`对账单已生成：${s.statement_no}（${s.item_count} 笔）`); invalidate(); },
    onError: (e: Error) => toast.error(e.message),
  });
  const confirm = useMutation({
    mutationFn: (id: string) => apiPost(`/finance/statements/${id}/confirm`, {}),
    onSuccess: () => { toast.success("对账单已确认，可进入收付款核销。"); invalidate(); },
    onError: (e: Error) => toast.error(e.message),
  });
  const detail = useQuery({
    queryKey: ["statement", expanded],
    queryFn: () => apiGet<Statement>(`/finance/statements/${expanded}`),
    enabled: Boolean(expanded),
  });
  const payments = useQuery({
    queryKey: ["stmt-payments", expanded],
    queryFn: () => apiGet<StatementPayment[]>(`/finance/statements/${expanded}/payments`),
    enabled: Boolean(expanded),
  });

  const cps = counterparties.data?.items ?? [];
  const items = st.rows; // 当前页
  const stmtActiveCount = activeConditionCount(stmtModel, STMT_FILTER_FIELDS);
  const settleQueue = settleQ.data?.items ?? [];

  const auditOne = useMutation({
    mutationFn: (id: string) => apiPost<StatementAuditResult>(`/finance/statements/${id}/audit`, {}),
    onSuccess: (r) => {
      toast.success(`审计完成：核对 ${r.total_lines} 笔明细，发现 ${r.anomaly_count} 处异常。`);
      queryClient.invalidateQueries({ queryKey: ["statement", expanded] });
      invalidate();
    },
  });
  const auditAll = useMutation({
    mutationFn: async () => {
      const results = await Promise.all(items.map((s) => apiPost<StatementAuditResult>(`/finance/statements/${s.id}/audit`, {})));
      return results.reduce((acc, r) => ({ lines: acc.lines + r.total_lines, anomalies: acc.anomalies + r.anomaly_count }), { lines: 0, anomalies: 0 });
    },
    onSuccess: (r) => {
      toast.success(`批量审计完成：核对 ${items.length} 张账单共 ${r.lines} 笔明细，发现 ${r.anomalies} 处异常。`);
      queryClient.invalidateQueries({ queryKey: ["statement", expanded] });
      invalidate();
    },
  });

  const statusTag = (s: Statement) => <span className={`tag ${STATUS_TAG[s.status] ?? "tag-none"}`}>{s.status_label ?? STATEMENT_STATUS_LABEL[s.status] ?? s.status}</span>;

  const stmtColumns: DataColumn<Statement>[] = [
    { key: "no", header: "系统结算单号", width: 190, alwaysVisible: true, sortField: "statement_no", sortValue: (s) => s.statement_no, exportValue: (s) => s.statement_no, render: (s) => <span className="mono" style={{ fontWeight: 700, color: "var(--accent)", fontSize: 13 }}>{expanded === s.id ? "▼" : "▶"} {s.statement_no}</span> },
    { key: "dir", header: "方向", width: 90, filterable: true, filterValue: (s) => (s.direction === "receivable" ? "应收" : "应付"), sortField: "direction", sortValue: (s) => s.direction, exportValue: (s) => (s.direction === "receivable" ? "应收" : "应付"), render: (s) => <span className={`tag ${s.direction === "receivable" ? "tag-low" : "tag-high"}`}>{s.direction === "receivable" ? "应收(AR)" : "应付(AP)"}</span> },
    { key: "cp", header: "对手方", width: 150, filterable: true, filterValue: (s) => s.counterparty_name, sortField: "counterparty_name", sortValue: (s) => s.counterparty_name, exportValue: (s) => s.counterparty_name, render: (s) => <span style={{ fontWeight: 600 }}>{s.counterparty_name}</span> },
    { key: "period", header: "账期 / 到期", width: 180, exportValue: (s) => `${s.period_start}~${s.period_end}${s.due_date ? ` 到期${s.due_date}` : ""}`, render: (s) => <span className="small muted mono">{s.period_start}~{s.period_end}{s.due_date ? <><br /><span style={{ color: "var(--amber)" }}>到期 {s.due_date}</span></> : ""}</span> },
    { key: "amt", header: "应结金额", width: 120, align: "right", sortField: "total_amount", sortValue: (s) => num(s.total_amount), exportValue: (s) => num(s.total_amount), render: (s) => <span style={{ fontWeight: 700 }}>{fmtMoney(s.total_amount)}</span> },
    { key: "settled", header: "已核销", width: 110, align: "right", sortField: "settled_amount", sortValue: (s) => num(s.settled_amount), exportValue: (s) => num(s.settled_amount), render: (s) => num(s.settled_amount) > 0 ? <span style={{ color: "var(--green)" }}>{fmtMoney(s.settled_amount)}</span> : <span className="muted">—</span> },
    { key: "out", header: "未结余额", width: 120, align: "right", sortField: "outstanding_anno", sortValue: (s) => num(s.outstanding), exportValue: (s) => num(s.outstanding), render: (s) => num(s.outstanding) > 0.01 ? <span style={{ fontWeight: 700, color: "var(--accent)" }}>{fmtMoney(s.outstanding)}</span> : <span className="tag tag-low">已结清</span> },
    { key: "diff", header: "差异", width: 110, align: "right", sortField: "diff_anno", sortValue: (s) => Math.abs(num(s.diff)), exportValue: (s) => num(s.diff), render: (s) => num(s.diff) !== 0 && num(s.external_total) > 0 ? <span className="mono" style={{ color: "var(--red)", fontWeight: 700 }}>{fmtMoney(s.diff)}</span> : <span className="muted small">无差异</span> },
    { key: "status", header: "状态", width: 100, filterable: true, filterValue: (s) => STATEMENT_STATUS_LABEL[s.status] ?? s.status, sortField: "status", sortValue: (s) => s.status, exportValue: (s) => STATEMENT_STATUS_LABEL[s.status] ?? s.status, render: (s) => statusTag(s) },
    { key: "act", header: "财务操作", width: 110, alwaysVisible: true, render: (s) => (
      <div className="row-actions" onClick={(e) => e.stopPropagation()}>
        {s.status === "draft" ? <button disabled={confirm.isPending} onClick={() => confirm.mutate(s.id)}>确认</button>
          : (s.status === "confirmed" || s.status === "partial") ? <button className="btn-primary" onClick={() => setSettleTarget(s)}>核销</button>
          : <span className="muted small">—</span>}
      </div>
    ) },
  ];

  const renderStmtDetail = () => (
    <div style={{ padding: "16px 20px", background: "var(--panel)", border: "1px solid var(--line-2)", borderRadius: 12, margin: "10px 16px 16px" }}>
      <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 12, alignItems: "center", flexWrap: "wrap", gap: 8 }}>
        <div style={{ fontWeight: 700, fontSize: 14 }}>账单明细审计</div>
        <div className="cluster">
          <span className="muted small">
            {detail.data?.audited_at ? `已审计 · ${fmtDateTime(detail.data.audited_at)}` : "尚未审计"}
            {" · "}共 {detail.data?.lines?.length || 0} 笔明细
          </span>
          {detail.data && (detail.data.status === "confirmed" || detail.data.status === "partial") && (
            <button className="btn-primary" style={{ padding: "3px 12px", fontSize: 11 }} onClick={(e) => { e.stopPropagation(); setSettleTarget(detail.data!); }}>登记核销</button>
          )}
          <button className="btn-ghost" style={{ padding: "3px 10px", fontSize: 11 }} disabled={auditOne.isPending} onClick={(e) => { e.stopPropagation(); if (expanded) auditOne.mutate(expanded); }}>
            {auditOne.isPending ? "审计中…" : "审计本单"}
          </button>
        </div>
      </div>

      {(payments.data?.length ?? 0) > 0 && (
        <div className="pay-trail">
          <span className="muted small">核销流水：</span>
          {(payments.data ?? []).map((p) => (
            <span key={p.id} className="pay-chip">{p.paid_at} · {p.method_label} · <b>{fmtMoney(p.amount)}</b>{p.reference_no ? ` · ${p.reference_no}` : ""}</span>
          ))}
        </div>
      )}

      {detail.isLoading ? (
        <span className="muted small">加载明细…</span>
      ) : (
        <div style={{ maxHeight: 260, overflowY: "auto", border: "1px solid var(--line)", borderRadius: 8 }}>
          <div className="table-wrap">
          <table className="table" style={{ margin: 0, fontSize: 12 }}>
            <thead>
              <tr><th>运单号</th><th>费用科目</th><th>金额</th><th>发生时间</th><th>审计结论</th></tr>
            </thead>
            <tbody>
              {(detail.data?.lines ?? []).map((l) => (
                <tr key={l.id} style={l.is_anomaly ? { background: "var(--red-weak)" } : {}}>
                  <td className="mono link">{l.waybill_no}</td>
                  <td><span className="tag tag-none">{l.expense_item_code}</span></td>
                  <td style={{ fontWeight: 700, color: l.is_anomaly ? "var(--red)" : "inherit" }}>{fmtMoney(l.amount)}</td>
                  <td className="muted mono">{l.occurred_at ? fmtDateTime(l.occurred_at) : "—"}</td>
                  <td>
                    {l.is_anomaly
                      ? <span style={{ color: "var(--red)", fontWeight: 700 }}>超历史均值 {fmtMoney(l.baseline_avg ?? "0")} {l.deviation_pct}%</span>
                      : l.baseline_avg != null
                        ? <span style={{ color: "var(--green)" }}>合规（基线 {fmtMoney(l.baseline_avg)}）</span>
                        : <span className="muted">{detail.data?.audited_at ? "样本不足，无基线" : "待审计"}</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        </div>
      )}
    </div>
  );

  return (
    <div className={`stack${tab === "statements" ? " table-page" : ""}`} style={{ position: "relative" }}>
      <div className="panel" style={{ background: "var(--hero-grad)", color: "var(--hero-ink)", border: "none" }}>
        <div style={{ padding: "20px 24px", display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: 12 }}>
          <div>
            <div style={{ fontSize: 22, fontWeight: "bold" }}>对账中心</div>
            <div style={{ fontSize: 13, color: "rgba(255,255,255,0.6)", marginTop: 6 }}>
              应收应付一体化：总览敞口 → 生成对账单 → 异常审计 → 账龄分析 → 收付款核销闭环。
            </div>
          </div>
          <div className="recon-tabs">
            {([["overview", "对账总览"], ["statements", "对账单台账"], ["aging", "账龄分析"], ["settle", "收付款核销"]] as const).map(([k, label]) => (
              <button key={k} className={tab === k ? "active" : ""} onClick={() => setTab(k)}>{label}{k === "settle" && settleQueue.length > 0 ? <span className="recon-badge">{settleQueue.length}</span> : null}</button>
            ))}
          </div>
        </div>
      </div>

      {tab === "overview" && <OverviewTab />}
      {tab === "aging" && <AgingTab />}

      {tab === "statements" && (
        <div className="panel om-panel" style={{ flex: 1 }}>
          <div className="panel-head" style={{ gap: 8, flexWrap: "wrap" }}>
            <span>对账单台账<span className="ai-pill">{st.total}</span></span>
            <div style={{ flex: 1 }} />
            <div style={{ position: "relative" }}>
              <button className={`btn-ghost${stmtActiveCount > 0 || showStmtFilter ? " on-accent" : ""}`} onClick={(e) => { e.stopPropagation(); setShowStmtFilter((v) => !v); }}>
                <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 5h18l-7 8v5l-4 2v-7z" /></svg>
                  高级筛选{stmtActiveCount > 0 ? ` · ${stmtActiveCount}` : ""}
                </span>
              </button>
              {showStmtFilter && <FilterBuilder fields={STMT_FILTER_FIELDS} model={stmtModel} onChange={setStmtModel} onClose={() => setShowStmtFilter(false)} />}
            </div>
            <button className="btn-ghost" disabled={auditAll.isPending || items.length === 0} onClick={() => auditAll.mutate()}>{auditAll.isPending ? "审计中…" : `批量审计（${items.length}）`}</button>
            <button className={`btn-primary${showGen ? " is-on" : ""}`} onClick={() => setShowGen((v) => !v)}>{showGen ? "收起" : "+ 生成对账单"}</button>
          </div>

          {showGen && (
            <div className="gen-bar">
              <div className="seg-tabs">
                <button className={direction === "receivable" ? "active" : ""} onClick={() => { setDirection("receivable"); setCounterpartyId(""); }}>应收（客户）</button>
                <button className={direction === "payable" ? "active" : ""} onClick={() => { setDirection("payable"); setCounterpartyId(""); }}>应付（承运商）</button>
              </div>
              <label>对手方
                <select value={counterpartyId} onChange={(e) => setCounterpartyId(e.target.value)}>
                  <option value="">选择 {cpType === "customer" ? "客户" : "承运商"}</option>
                  {cps.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
                </select>
              </label>
              <label>账期开始<input type="date" value={start} onChange={(e) => setStart(e.target.value)} /></label>
              <label>账期结束<input type="date" value={end} onChange={(e) => setEnd(e.target.value)} /></label>
              <label>到期日<input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} /></label>
              <label>对方金额<input inputMode="decimal" placeholder="稽核用" value={externalTotal} onChange={(e) => setExternalTotal(e.target.value)} style={{ width: 90 }} /></label>
              <button className="btn-primary" disabled={!counterpartyId || generate.isPending} onClick={() => generate.mutate()}
                title={!counterpartyId ? `请先选择${cpType === "customer" ? "客户" : "承运商"}` : "生成对账单"}>
                {generate.isPending ? "生成中…" : "生成"}
              </button>
              {!counterpartyId && <span className="muted small" style={{ alignSelf: "center", color: "var(--amber)" }}>▸ 请先选择对手方</span>}
            </div>
          )}

          {stmtActiveCount > 0 && (
              <div className="om-chips">
                <span className="muted small">条件（{stmtModel.combinator === "and" ? "全部满足" : "任一满足"}）：</span>
                {stmtModel.conditions.map((c) => {
                  const label = describeCondition(c, STMT_FILTER_FIELDS);
                  if (!label) return null;
                  return <span key={c.id} className="filter-chip">{label}<button onClick={() => setStmtModel((m) => ({ ...m, conditions: m.conditions.filter((x) => x.id !== c.id) }))}>×</button></span>;
                })}
                <button className="linkish small" onClick={() => setStmtModel(EMPTY_MODEL)}>清空条件</button>
              </div>
            )}
            {st.isError ? (
              <StateView kind="error" onRetry={() => st.refetch()} />
            ) : (
              <DataTable<Statement>
                columns={stmtColumns}
                rows={items}
                rowKey={(s) => s.id}
                viewKey="statements"
                exportName="对账单"
                server={st.server}
                fill
                onRowClick={(s) => setExpanded(expanded === s.id ? "" : s.id)}
                expandedKey={expanded}
                renderExpanded={renderStmtDetail}
                emptyState={<StateView kind="empty" scene="recon-empty" />}
                toolbarLeft={<span className="muted small">点行看明细/核销流水 · 点表头排序</span>}
              />
            )}
        </div>
      )}

      {tab === "settle" && (
        <div className="panel" style={{ flex: 1 }}>
          <div className="panel-head" style={{ gap: 8, flexWrap: "wrap" }}>
            <span>待核销队列<span className="ai-pill">{settleQueue.length}</span></span>
            <div style={{ flex: 1 }} />
            <span className="muted small">已确认 / 部分结算且仍有未结余额的对账单</span>
          </div>
          {settleQ.isLoading ? (
            <StateView kind="loading" compact />
          ) : settleQueue.length === 0 ? (
            <StateView kind="empty" title="暂无待核销单据" hint="确认对账单后，会进入此队列等待收付款核销。" />
          ) : (
            <div className="settle-list">
              {settleQueue.map((s) => {
                const rate = num(s.total_amount) > 0 ? num(s.settled_amount) / num(s.total_amount) : 0;
                const overdue = s.due_date && s.due_date < new Date().toISOString().slice(0, 10);
                return (
                  <div className="settle-item" key={s.id}>
                    <div className="settle-item-main">
                      <div className="settle-item-head">
                        <span className={`tag ${s.direction === "receivable" ? "tag-low" : "tag-high"}`}>{s.direction === "receivable" ? "应收" : "应付"}</span>
                        <span className="mono" style={{ fontWeight: 700, color: "var(--accent)" }}><CopyCode value={s.statement_no} /></span>
                        <span style={{ fontWeight: 600 }}>{s.counterparty_name}</span>
                        {statusTag(s)}
                        {overdue && <span className="tag tag-high">已逾期</span>}
                      </div>
                      <div className="settle-item-sub muted small">
                        账期 {s.period_start}~{s.period_end}{s.due_date ? ` · 到期 ${s.due_date}` : ""} · {s.item_count} 笔
                      </div>
                      <div className="settle-progress"><div style={{ width: `${Math.min(rate * 100, 100)}%` }} /></div>
                    </div>
                    <div className="settle-item-amt">
                      <div><span className="muted small">应结</span><b>{fmtMoney(s.total_amount)}</b></div>
                      <div><span className="muted small">已核销</span><b style={{ color: "var(--green)" }}>{fmtMoney(s.settled_amount)}</b></div>
                      <div><span className="muted small">未结</span><b style={{ color: "var(--accent)" }}>{fmtMoney(s.outstanding)}</b></div>
                    </div>
                    <button className="btn-primary" onClick={() => setSettleTarget(s)}>登记{s.direction === "receivable" ? "收款" : "付款"}</button>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}

      {settleTarget && <SettleModal statement={settleTarget} onClose={() => setSettleTarget(null)} onDone={() => { invalidate(); if (expanded) { queryClient.invalidateQueries({ queryKey: ["statement", expanded] }); queryClient.invalidateQueries({ queryKey: ["stmt-payments", expanded] }); } }} />}
    </div>
  );
}
