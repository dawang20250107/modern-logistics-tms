import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";

import { apiGet } from "../api/client";
import { fmtMoney } from "../api/format";
import type { LineageStatement, OrderLineage } from "../api/types";
import { StateView } from "./StateView";

const STMT_TONE: Record<string, string> = { draft: "tag-none", confirmed: "tag-info", partial: "tag-medium", settled: "tag-low" };

function StmtChip({ s }: { s: LineageStatement }) {
  return (
    <Link className={`lin-stmt ${s.direction === "receivable" ? "lin-ar" : "lin-ap"}`} to="/reconciliation" title={`${s.counterparty_name} · 应结 ${fmtMoney(s.total_amount)} · 已核销 ${fmtMoney(s.settled_amount)} · 未结 ${fmtMoney(s.outstanding)}`}>
      <span className="mono">{s.statement_no}</span>
      <span className={`tag ${STMT_TONE[s.status] ?? "tag-none"}`}>{s.status_label}</span>
      <span className="muted small">未结 {fmtMoney(s.outstanding)}</span>
    </Link>
  );
}

// 单据血缘：一屏看清 订单(DD) → 运单(YD) → 对账单(ST) 全链路与金额、结算进度。
export function DocumentLineage({ orderId }: { orderId: string }) {
  const q = useQuery({ queryKey: ["order", orderId, "lineage"], queryFn: () => apiGet<OrderLineage>(`/orders/${orderId}/lineage`) });

  return (
    <div className="panel">
      <div className="panel-head" style={{ gap: 8 }}>
        <span>单据关系（血缘）</span>
        <span className="muted small">订单 DD → 运单 YD → 对账单 ST</span>
      </div>
      {q.isLoading ? (
        <StateView kind="loading" compact />
      ) : q.isError || !q.data ? (
        <StateView kind="error" onRetry={() => q.refetch()} />
      ) : (
        <div className="lineage">
          {/* 订单根节点 + 汇总 */}
          <div className="lin-order">
            <div className="lin-row">
              <span className="doc-order mono">{q.data.order.order_no}</span>
              <span className="tag tag-info">{q.data.order.status_label}</span>
              <span className="muted small">{q.data.order.customer_name}</span>
              <span style={{ flex: 1 }} />
              <span className="muted small">报价 {fmtMoney(q.data.order.quoted_amount)}</span>
            </div>
            <div className="lin-sum">
              <span>运单 <b>{q.data.summary.waybill_count}</b></span>
              <span>应收 <b style={{ color: "var(--green)" }}>{fmtMoney(q.data.summary.receivable_total)}</b></span>
              <span>应付 <b style={{ color: "var(--amber)" }}>{fmtMoney(q.data.summary.payable_total)}</b></span>
              <span>毛利 <b style={{ color: q.data.summary.gross >= 0 ? "var(--green)" : "var(--red)" }}>{fmtMoney(q.data.summary.gross)}</b></span>
              <span>对账单 <b>{q.data.summary.statement_count}</b></span>
            </div>
          </div>

          {/* 运单分支 */}
          {q.data.waybills.length === 0 ? (
            <div className="lin-empty muted small">尚未派单转运单。该订单确认进池、调度派单后，运单与对账单链路将在此显现。</div>
          ) : (
            <div className="lin-branch">
              {q.data.waybills.map((w) => (
                <div className="lin-wb" key={w.id}>
                  <div className="lin-wb-head">
                    <span className="lin-conn" aria-hidden>└─</span>
                    <Link className="doc-waybill mono" to={`/waybills/${w.waybill_no}`}>{w.waybill_no}</Link>
                    <span className="tag tag-none">{w.status_label}</span>
                    {w.carrier_name && <span className="muted small">承运 {w.carrier_name}</span>}
                    {w.batch_no && <span className="tag tag-info" title="派车批次">批次 {w.batch_no}</span>}
                    <span style={{ flex: 1 }} />
                    <span className="small">应收 <b style={{ color: "var(--green)" }}>{fmtMoney(w.receivable)}</b> · 应付 <b style={{ color: "var(--amber)" }}>{fmtMoney(w.payable)}</b></span>
                  </div>
                  {w.statements.length > 0 ? (
                    <div className="lin-stmts">
                      <span className="lin-conn2" aria-hidden>↳ 对账</span>
                      {w.statements.map((s) => <StmtChip key={s.id} s={s} />)}
                    </div>
                  ) : (
                    <div className="lin-stmts muted small"><span className="lin-conn2" aria-hidden>↳ 对账</span>费用已归集，尚未生成对账单</div>
                  )}
                </div>
              ))}
            </div>
          )}

          {/* 批次归集提示 */}
          {q.data.batches.length > 0 && (
            <div className="lin-batches">
              {q.data.batches.map((b) => (
                <span key={b.batch_no} className="muted small">
                  批次 <span className="mono">{b.batch_no}</span> · {b.carrier_name || "网货平台"} · {b.order_count} 单归集应付 {fmtMoney(b.total_payable)}
                  {b.statement_no ? <> · 已生成 <span className="mono">{b.statement_no}</span></> : " · 未对账"}
                </span>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
