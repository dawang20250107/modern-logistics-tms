import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Fragment, useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import type { Carrier, Customer, Paginated, Statement, StatementAuditResult } from "../api/types";
import { STATEMENT_STATUS_LABEL } from "../api/types";

export function ReconciliationPage() {
  const queryClient = useQueryClient();
  const [direction, setDirection] = useState<"receivable" | "payable">("receivable");
  const [counterpartyId, setCounterpartyId] = useState("");
  const [start, setStart] = useState("2026-06-01");
  const [end, setEnd] = useState("2026-06-30");
  const [externalTotal, setExternalTotal] = useState("");
  const [expanded, setExpanded] = useState<string>("");

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

  return (
    <div className="stack" style={{ position: "relative" }}>

      <div className="panel" style={{ background: "linear-gradient(135deg, #1b1e25 0%, #16181d 100%)", color: "#fff", border: "none" }}>
        <div style={{ padding: "20px 24px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <div>
            <div style={{ fontSize: 22, fontWeight: "bold", display: "flex", alignItems: "center", gap: 10 }}>
              对账管理
              
            </div>
            <div style={{ fontSize: 13, color: "#94a3b8", marginTop: 6 }}>
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
        <div className="panel-head">对账单核销台账</div>
        {statements.isLoading ? (
          <div className="muted" style={{ padding: 24, textAlign: "center" }}>加载中…</div>
        ) : items.length === 0 ? (
          <div className="muted" style={{ padding: 24, textAlign: "center" }}>暂无对账单</div>
        ) : (
          <table className="table" style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ background: "var(--line)" }}>
                <th style={{ padding: "10px 12px" }}>系统结算单号</th>
                <th>账单方向</th>
                <th>对手方</th>
                <th>账期</th>
                <th>应结金额</th>
                <th>交易笔数</th>
                <th>差异</th>
                <th>状态</th>
                <th>财务操作</th>
              </tr>
            </thead>
            <tbody>
              {items.map((s) => (
                <Fragment key={s.id}>
                  <tr style={{ cursor: "pointer" }} onClick={() => setExpanded(expanded === s.id ? "" : s.id)}>
                    <td className="mono" style={{ fontWeight: "bold", color: "var(--brand)", fontSize: 13 }}>
                      {expanded === s.id ? "▼" : "▶"} {s.statement_no}
                    </td>
                    <td>
                      <span className="tag" style={{ background: s.direction === "receivable" ? "rgba(16,185,129,0.1)" : "rgba(239,68,68,0.1)", color: s.direction === "receivable" ? "var(--green)" : "var(--red)" }}>
                        {s.direction === "receivable" ? "AR 应收" : "AP 应付"}
                      </span>
                    </td>
                    <td style={{ fontWeight: 600, color: "var(--ink)" }}>{s.counterparty_name}</td>
                    <td className="small muted mono">{s.period_start} ~ {s.period_end}</td>
                    <td style={{ fontWeight: 800, fontSize: 14 }}>{fmtMoney(s.total_amount)}</td>
                    <td>{s.item_count} 笔</td>
                    <td className="mono" style={Number(s.diff) !== 0 ? { color: "var(--red)", fontWeight: "bold" } : { color: "var(--muted)" }}>
                      {Number(s.diff) !== 0 ? `差异 ${fmtMoney(s.diff)}` : "无差异"}
                    </td>
                    <td>
                      <span className={`tag tag-${s.status === "confirmed" || s.status === "settled" ? "low" : "medium"}`}>
                        {STATEMENT_STATUS_LABEL[s.status] ?? s.status}
                      </span>
                    </td>
                    <td>
                      {s.status === "draft" && (
                        <button className="btn-primary" style={{ padding: "4px 10px", fontSize: 11 }} disabled={confirm.isPending} onClick={(e) => { e.stopPropagation(); confirm.mutate(s.id); }}>
                          确认
                        </button>
                      )}
                    </td>
                  </tr>
                  
                  {/* 明细审计区 */}
                  {expanded === s.id && (
                    <tr style={{ background: "rgba(0,0,0,0.015)" }}>
                      <td colSpan={9} style={{ padding: "0 24px 24px" }}>
                        <div style={{ padding: "16px 20px", background: "#fff", border: "1px solid var(--line-strong)", borderRadius: 12, boxShadow: "0 4px 12px rgba(0,0,0,0.05)", marginTop: 10 }}>
                          <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 12, alignItems: "center" }}>
                            <div style={{ fontWeight: "bold", fontSize: 14 }}>
                              账单明细审计
                            </div>
                            <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                              <span className="muted small">
                                {detail.data?.audited_at
                                  ? `已审计 · ${new Date(detail.data.audited_at).toLocaleString()}`
                                  : "尚未审计"}
                                {" · "}共 {detail.data?.lines?.length || 0} 笔明细
                              </span>
                              <button
                                className="btn-ghost"
                                style={{ padding: "3px 10px", fontSize: 11 }}
                                disabled={auditOne.isPending}
                                onClick={(e) => { e.stopPropagation(); auditOne.mutate(s.id); }}
                              >
                                {auditOne.isPending ? "审计中…" : "审计本单"}
                              </button>
                            </div>
                          </div>

                          {detail.isLoading ? (
                            <span className="muted small">加载明细…</span>
                          ) : (
                            <div style={{ maxHeight: 260, overflowY: "auto", border: "1px solid var(--line)", borderRadius: 8 }}>
                              <table className="table" style={{ margin: 0, fontSize: 12 }}>
                                <thead>
                                  <tr style={{ background: "var(--panel-2)" }}>
                                    <th>运单号</th>
                                    <th>费用科目</th>
                                    <th>金额</th>
                                    <th>发生时间</th>
                                    <th>审计结论</th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {(detail.data?.lines ?? []).map((l) => (
                                    <tr key={l.id} style={l.is_anomaly ? { background: "var(--red-weak)" } : {}}>
                                      <td className="mono link" style={{ cursor: "pointer" }}>{l.waybill_no}</td>
                                      <td>
                                        <span className="tag" style={{ background: "rgba(0,0,0,0.04)" }}>{l.expense_item_code}</span>
                                      </td>
                                      <td style={{ fontWeight: "bold", color: l.is_anomaly ? "var(--red)" : "inherit" }}>
                                        {fmtMoney(l.amount)}
                                      </td>
                                      <td className="muted mono">{l.occurred_at ? new Date(l.occurred_at).toLocaleString() : "-"}</td>
                                      <td>
                                        {l.is_anomaly ? (
                                          <span style={{ color: "var(--red)", fontWeight: "bold", display: "flex", alignItems: "center", gap: 4 }}>
                                            超历史均值 ¥{fmtMoney(l.baseline_avg ?? "0")} {l.deviation_pct}%
                                          </span>
                                        ) : l.baseline_avg != null ? (
                                          <span style={{ color: "#27ae60", display: "flex", alignItems: "center", gap: 4 }}>
                                            合规（基线 ¥{fmtMoney(l.baseline_avg)}）
                                          </span>
                                        ) : (
                                          <span className="muted">{detail.data?.audited_at ? "样本不足，无基线" : "待审计"}</span>
                                        )}
                                      </td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          )}
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
