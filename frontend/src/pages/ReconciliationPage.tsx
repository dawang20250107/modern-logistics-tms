import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Fragment, useState } from "react";

import { apiGet, apiPost } from "../api/client";
import type { Carrier, Customer, Paginated, Statement } from "../api/types";
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
    onSuccess: invalidate,
  });
  const confirm = useMutation({
    mutationFn: (id: string) => apiPost(`/finance/statements/${id}/confirm`, {}),
    onSuccess: invalidate,
  });
  const detail = useQuery({
    queryKey: ["statement", expanded],
    queryFn: () => apiGet<Statement>(`/finance/statements/${expanded}`),
    enabled: Boolean(expanded),
  });

  const cps = counterparties.data?.items ?? [];
  const items = statements.data?.items ?? [];

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">生成对账单</div>
        <div className="form-row">
          <select value={direction} onChange={(e) => { setDirection(e.target.value as "receivable" | "payable"); setCounterpartyId(""); }}>
            <option value="receivable">应收（客户）</option>
            <option value="payable">应付（承运商）</option>
          </select>
          <select value={counterpartyId} onChange={(e) => setCounterpartyId(e.target.value)}>
            <option value="">选择{cpType === "customer" ? "客户" : "承运商"}</option>
            {cps.map((c) => (
              <option key={c.id} value={c.id}>{c.name}</option>
            ))}
          </select>
          <input type="date" value={start} onChange={(e) => setStart(e.target.value)} />
          <input type="date" value={end} onChange={(e) => setEnd(e.target.value)} />
          <input placeholder="对方金额(差异稽核,可选)" value={externalTotal} onChange={(e) => setExternalTotal(e.target.value)} />
          <button className="btn-primary" disabled={!counterpartyId || generate.isPending} onClick={() => generate.mutate()}>
            {generate.isPending ? "生成中…" : "生成"}
          </button>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">对账单</div>
        {statements.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : items.length === 0 ? (
          <div className="muted" style={{ padding: 16 }}>暂无对账单</div>
        ) : (
          <table className="table">
            <thead>
              <tr><th>单号</th><th>方向</th><th>对方</th><th>账期</th><th>金额</th><th>笔数</th><th>差异</th><th>状态</th><th>操作</th></tr>
            </thead>
            <tbody>
              {items.map((s) => (
                <Fragment key={s.id}>
                  <tr>
                    <td className="mono">
                      <button className="link" style={{ background: "none", border: "none", cursor: "pointer", padding: 0 }}
                        onClick={() => setExpanded(expanded === s.id ? "" : s.id)}>
                        {s.statement_no}
                      </button>
                    </td>
                    <td>{s.direction === "receivable" ? "应收" : "应付"}</td>
                    <td>{s.counterparty_name}</td>
                    <td className="small">{s.period_start} ~ {s.period_end}</td>
                    <td>¥{s.total_amount}</td>
                    <td>{s.item_count}</td>
                    <td className={Number(s.diff) !== 0 ? "" : "muted"} style={Number(s.diff) !== 0 ? { color: "var(--red)" } : {}}>
                      {Number(s.diff) !== 0 ? `¥${s.diff}` : "—"}
                    </td>
                    <td><span className={`tag tag-${s.status === "confirmed" || s.status === "settled" ? "low" : "medium"}`}>{STATEMENT_STATUS_LABEL[s.status] ?? s.status}</span></td>
                    <td>
                      {s.status === "draft" && (
                        <button className="btn-ghost" disabled={confirm.isPending} onClick={() => confirm.mutate(s.id)}>确认</button>
                      )}
                    </td>
                  </tr>
                  {expanded === s.id && (
                    <tr>
                      <td colSpan={9} style={{ background: "var(--panel-2)" }}>
                        {detail.isLoading ? (
                          <span className="muted small">加载明细…</span>
                        ) : (
                          <table className="table" style={{ margin: 0 }}>
                            <thead><tr><th>运单</th><th>费用项</th><th>金额</th><th>发生时间</th></tr></thead>
                            <tbody>
                              {(detail.data?.lines ?? []).map((l) => (
                                <tr key={l.id}>
                                  <td className="mono">{l.waybill_no}</td>
                                  <td>{l.expense_item_code}</td>
                                  <td>¥{l.amount}</td>
                                  <td className="small">{l.occurred_at ? new Date(l.occurred_at).toLocaleString() : "-"}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        )}
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
