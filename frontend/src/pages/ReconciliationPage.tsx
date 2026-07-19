import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import type { Carrier, Customer, Paginated, Statement, StatementAuditResult } from "../api/types";
import { STATEMENT_STATUS_LABEL } from "../api/types";
import { DataTable, type DataColumn } from "../components/DataTable";
import { FilterBuilder, applyFilterModel, activeConditionCount, describeCondition, EMPTY_MODEL, type FilterFieldDef, type FilterModel } from "../components/FilterBuilder";
import { StateView } from "../components/StateView";

const STMT_FILTER_FIELDS: FilterFieldDef[] = [
  { key: "no", label: "结算单号", type: "text", accessor: (s) => (s as Statement).statement_no },
  { key: "cp", label: "对手方", type: "text", accessor: (s) => (s as Statement).counterparty_name },
  { key: "dir", label: "方向", type: "enum", options: [{ value: "receivable", label: "应收 AR" }, { value: "payable", label: "应付 AP" }], accessor: (s) => (s as Statement).direction },
  { key: "status", label: "状态", type: "enum", options: Object.entries(STATEMENT_STATUS_LABEL).map(([value, label]) => ({ value, label })), accessor: (s) => (s as Statement).status },
  { key: "amt", label: "应结金额", type: "number", accessor: (s) => Number((s as Statement).total_amount) || 0 },
  { key: "diff", label: "差异额", type: "number", accessor: (s) => Math.abs(Number((s as Statement).diff) || 0) },
];

export function ReconciliationPage() {
  const queryClient = useQueryClient();
  const [direction, setDirection] = useState<"receivable" | "payable">("receivable");
  const [counterpartyId, setCounterpartyId] = useState("");
  const [start, setStart] = useState("2026-06-01");
  const [end, setEnd] = useState("2026-06-30");
  const [externalTotal, setExternalTotal] = useState("");
  const [expanded, setExpanded] = useState<string>("");
  const [stmtModel, setStmtModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showStmtFilter, setShowStmtFilter] = useState(false);

  const cpType = direction === "receivable" ? "customer" : "carrier";
  const counterparties = useQuery({
    queryKey: ["cp", cpType],
    queryFn: () => apiGet<Paginated<Customer | Carrier>>(`/${cpType === "customer" ? "customers" : "carriers"}?page_size=200`),
  });
  const statements = useQuery({
    queryKey: ["statements"],
    queryFn: () => apiGet<Paginated<Statement>>("/finance/statements?page_size=50"),
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["statements"] });

  const generate = useMutation({
    mutationFn: () =>
      apiPost<Statement>("/finance/statements/generate", {
        direction, counterparty_type: cpType, counterparty_id: counterpartyId,
        period_start: start, period_end: end, external_total: externalTotal || 0,
      }),
    onSuccess: (s) => { toast.success(`对账单已生成：${s.statement_no}`); invalidate(); },
  });
  const confirm = useMutation({
    mutationFn: (id: string) => apiPost(`/finance/statements/${id}/confirm`, {}),
    onSuccess: () => { toast.success("对账单已确认。"); invalidate(); },
  });
  const detail = useQuery({
    queryKey: ["statement", expanded],
    queryFn: () => apiGet<Statement>(`/finance/statements/${expanded}`),
    enabled: Boolean(expanded),
  });

  const cps = counterparties.data?.items ?? [];
  const items = statements.data?.items ?? [];
  const stmtActiveCount = activeConditionCount(stmtModel, STMT_FILTER_FIELDS);
  const filteredStmts = applyFilterModel(items, stmtModel, STMT_FILTER_FIELDS);

  // 对账一体化摘要：应收/应付金额、单据状态、差异张数（由台账实时汇总）
  const num = (v: unknown) => Number(v) || 0;
  const summary = {
    recvAmt: items.filter((s) => s.direction === "receivable").reduce((a, s) => a + num(s.total_amount), 0),
    payAmt: items.filter((s) => s.direction === "payable").reduce((a, s) => a + num(s.total_amount), 0),
    draft: items.filter((s) => s.status === "draft").length,
    confirmed: items.filter((s) => s.status === "confirmed").length,
    withDiff: items.filter((s) => Math.abs(num(s.external_total) - num(s.total_amount)) > 0.01 && num(s.external_total) > 0).length,
  };

  // 对账单异常审计：按费用科目历史均值计算
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
      return results.reduce(
        (acc, r) => ({ lines: acc.lines + r.total_lines, anomalies: acc.anomalies + r.anomaly_count }),
        { lines: 0, anomalies: 0 }
      );
    },
    onSuccess: (r) => {
      toast.success(`批量审计完成：核对 ${items.length} 张账单共 ${r.lines} 笔明细，发现 ${r.anomalies} 处异常。`);
      queryClient.invalidateQueries({ queryKey: ["statement", expanded] });
      invalidate();
    },
  });

  const stmtColumns: DataColumn<Statement>[] = [
    { key: "no", header: "系统结算单号", width: 190, alwaysVisible: true, sortValue: (s) => s.statement_no, exportValue: (s) => s.statement_no, render: (s) => <span className="mono" style={{ fontWeight: 700, color: "var(--accent)", fontSize: 13 }}>{expanded === s.id ? "▼" : "▶"} {s.statement_no}</span> },
    { key: "dir", header: "方向", width: 90, filterable: true, filterValue: (s) => (s.direction === "receivable" ? "应收" : "应付"), sortValue: (s) => s.direction, exportValue: (s) => (s.direction === "receivable" ? "应收" : "应付"), render: (s) => <span className={`tag ${s.direction === "receivable" ? "tag-low" : "tag-high"}`}>{s.direction === "receivable" ? "AR 应收" : "AP 应付"}</span> },
    { key: "cp", header: "对手方", width: 150, filterable: true, filterValue: (s) => s.counterparty_name, sortValue: (s) => s.counterparty_name, exportValue: (s) => s.counterparty_name, render: (s) => <span style={{ fontWeight: 600 }}>{s.counterparty_name}</span> },
    { key: "period", header: "账期", width: 170, exportValue: (s) => `${s.period_start}~${s.period_end}`, render: (s) => <span className="small muted mono">{s.period_start} ~ {s.period_end}</span> },
    { key: "amt", header: "应结金额", width: 120, align: "right", sortValue: (s) => Number(s.total_amount) || 0, exportValue: (s) => Number(s.total_amount) || 0, render: (s) => <span style={{ fontWeight: 700 }}>{fmtMoney(s.total_amount)}</span> },
    { key: "cnt", header: "笔数", width: 70, align: "right", sortValue: (s) => s.item_count, exportValue: (s) => s.item_count, render: (s) => <>{s.item_count} 笔</> },
    { key: "diff", header: "差异", width: 120, align: "right", sortValue: (s) => Math.abs(Number(s.diff) || 0), exportValue: (s) => Number(s.diff) || 0, render: (s) => <span className="mono" style={Number(s.diff) !== 0 ? { color: "var(--red)", fontWeight: 700 } : { color: "var(--muted)" }}>{Number(s.diff) !== 0 ? `差异 ${fmtMoney(s.diff)}` : "无差异"}</span> },
    { key: "status", header: "状态", width: 100, filterable: true, filterValue: (s) => STATEMENT_STATUS_LABEL[s.status] ?? s.status, sortValue: (s) => s.status, exportValue: (s) => STATEMENT_STATUS_LABEL[s.status] ?? s.status, render: (s) => <span className={`tag tag-${s.status === "confirmed" || s.status === "settled" ? "low" : "medium"}`}>{STATEMENT_STATUS_LABEL[s.status] ?? s.status}</span> },
    { key: "act", header: "财务操作", width: 90, alwaysVisible: true, render: (s) => s.status === "draft" ? <div className="row-actions" onClick={(e) => e.stopPropagation()}><button disabled={confirm.isPending} onClick={() => confirm.mutate(s.id)}>确认</button></div> : <span className="muted small">—</span> },
  ];

  const renderStmtDetail = () => (
    <div style={{ padding: "16px 20px", background: "var(--panel)", border: "1px solid var(--line-2)", borderRadius: 12, margin: "10px 16px 16px" }}>
      <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 12, alignItems: "center" }}>
        <div style={{ fontWeight: 700, fontSize: 14 }}>账单明细审计</div>
        <div className="cluster">
          <span className="muted small">
            {detail.data?.audited_at ? `已审计 · ${new Date(detail.data.audited_at).toLocaleString()}` : "尚未审计"}
            {" · "}共 {detail.data?.lines?.length || 0} 笔明细
          </span>
          <button className="btn-ghost" style={{ padding: "3px 10px", fontSize: 11 }} disabled={auditOne.isPending} onClick={(e) => { e.stopPropagation(); if (expanded) auditOne.mutate(expanded); }}>
            {auditOne.isPending ? "审计中…" : "审计本单"}
          </button>
        </div>
      </div>
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
                  <td className="muted mono">{l.occurred_at ? new Date(l.occurred_at).toLocaleString() : "-"}</td>
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
    <div className="stack" style={{ position: "relative" }}>

      <div className="panel" style={{ background: "linear-gradient(135deg, #1b1e25 0%, #16181d 100%)", color: "#fff", border: "none" }}>
        <div style={{ padding: "20px 24px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <div>
            <div style={{ fontSize: 22, fontWeight: "bold", display: "flex", alignItems: "center", gap: 10 }}>
              对账管理
              
            </div>
            <div style={{ fontSize: 13, color: "rgba(255,255,255,0.6)", marginTop: 6 }}>
              管理客户应收与承运商应付对账单，自动排查异常费用。
            </div>
          </div>
          <button
            className="btn-primary"
            style={{ padding: "10px 18px", fontSize: 13, background: "var(--grad-ai)", boxShadow: "0 6px 16px rgba(75,88,240,0.28)" }}
            onClick={() => auditAll.mutate()}
            disabled={auditAll.isPending || items.length === 0}
          >
            {auditAll.isPending ? "审计中…" : `批量审计（${items.length} 张）`}
          </button>
        </div>
      </div>

      <div className="ct-grid" style={{ gridTemplateColumns: "1fr 1fr" }}>
        <div className="panel">
          <div className="panel-head">
            <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
              {direction === "receivable" ? "应收配置" : "应付配置"}
            </span>
          </div>
          <div className="grid-form" style={{ padding: "16px 20px" }}>
            <div className="seg-tabs" style={{ gridColumn: "1 / -1", marginBottom: 4 }}>
              <button className={direction === "receivable" ? "active" : ""} onClick={() => { setDirection("receivable"); setCounterpartyId(""); }}>应收（客户）</button>
              <button className={direction === "payable" ? "active" : ""} onClick={() => { setDirection("payable"); setCounterpartyId(""); }}>应付（承运商）</button>
            </div>
            <label>
              对手方主体
              <select value={counterpartyId} onChange={(e) => setCounterpartyId(e.target.value)} style={{ padding: "8px 10px" }}>
                <option value="">请选择 {cpType === "customer" ? "客户" : "承运商"}</option>
                {cps.map((c) => (
                  <option key={c.id} value={c.id}>{c.name}</option>
                ))}
              </select>
            </label>
            <label>账期开始时间<input type="date" value={start} onChange={(e) => setStart(e.target.value)} /></label>
            <label>账期结束时间<input type="date" value={end} onChange={(e) => setEnd(e.target.value)} /></label>
            <label>对方提交金额 (稽核差异用)
              <input placeholder="0.00" value={externalTotal} onChange={(e) => setExternalTotal(e.target.value)} />
            </label>
            <div style={{ gridColumn: "1 / -1", marginTop: 12 }}>
              <button className="btn-primary" style={{ width: "100%", padding: 12, fontSize: 13 }} disabled={!counterpartyId || generate.isPending} onClick={() => generate.mutate()}>
                {generate.isPending ? "生成中…" : `生成${direction === 'receivable' ? '收款' : '付款'}对账单`}
              </button>
            </div>
          </div>
        </div>

        <div className="panel">
          <div className="panel-head">对账摘要</div>
          <div className="kpi-row" style={{ padding: 16, gridTemplateColumns: "1fr 1fr" }}>
            <div className="kpi kpi-blue"><div className="kpi-top"><span className="kpi-label">应收合计</span></div><div className="kpi-value" style={{ fontSize: 22 }}>{fmtMoney(summary.recvAmt)}</div></div>
            <div className="kpi kpi-red"><div className="kpi-top"><span className="kpi-label">应付合计</span></div><div className="kpi-value" style={{ fontSize: 22 }}>{fmtMoney(summary.payAmt)}</div></div>
          </div>
          <div className="kv">
            <div><span>对账单总数</span><b>{items.length}</b></div>
            <div><span>待确认草稿</span><b style={summary.draft > 0 ? { color: "var(--amber)" } : {}}>{summary.draft}</b></div>
            <div><span>已确认</span><b>{summary.confirmed}</b></div>
            <div><span>存在差异</span><b style={summary.withDiff > 0 ? { color: "var(--red)" } : {}}>{summary.withDiff}</b></div>
          </div>
        </div>
      </div>

      <div className="panel" style={{ flex: 1 }}>
        <div className="panel-head" style={{ gap: 8, flexWrap: "wrap" }}>
          <span>对账单核销台账</span>
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
        </div>
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
        {statements.isLoading ? (
          <StateView kind="loading" compact />
        ) : statements.isError ? (
          <StateView kind="error" onRetry={() => statements.refetch()} />
        ) : filteredStmts.length === 0 ? (
          <StateView kind="empty" scene="recon-empty" />
        ) : (
          <DataTable<Statement>
            columns={stmtColumns}
            rows={filteredStmts}
            rowKey={(s) => s.id}
            viewKey="statements"
            exportName="对账单"
            onRowClick={(s) => setExpanded(expanded === s.id ? "" : s.id)}
            expandedKey={expanded}
            renderExpanded={renderStmtDetail}
            toolbarLeft={<span className="muted small">共 {filteredStmts.length} 张 · 点行看明细 · 表头 ⚟ 筛选/排序 · 「列」增减字段</span>}
          />
        )}
      </div>
    </div>
  );
}
