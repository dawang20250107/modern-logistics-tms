import { useQuery } from "@tanstack/react-query";

import { apiGet } from "../api/client";
import type { FinanceCardData } from "../api/types";

const yuan = (n: number) => `¥${Math.round(n).toLocaleString()}`;

// 单票财务卡：客户报价 / 承运商报价 / 其他费 / 毛利 + 是否可对账
export function FinanceCard({ waybillNo }: { waybillNo: string }) {
  const q = useQuery({
    queryKey: ["finance-card", waybillNo],
    queryFn: () => apiGet<FinanceCardData>(`/waybills/${waybillNo}/finance-card`),
    enabled: Boolean(waybillNo),
  });

  if (q.isLoading) return <div className="muted small">核算中…</div>;
  const c = q.data;
  if (!c) return <div className="muted small">未取到财务卡。</div>;

  const marginColor = c.gross_margin > 0 ? "var(--green)" : c.gross_margin < 0 ? "var(--red)" : "var(--ink)";
  return (
    <div className="reply-card">
      <div className="kv" style={{ padding: 0, gridTemplateColumns: "1fr 1fr", gap: "8px 16px" }}>
        <div><span>客户报价(应收)</span><b className="num">{yuan(c.receivable)}</b></div>
        <div><span>承运商报价(应付)</span><b className="num">{yuan(c.payable)}</b></div>
        {c.other_fee > 0 && <div><span>平台/其他费</span><b className="num">{yuan(c.other_fee)}</b></div>}
        {c.exception_deduction > 0 && <div><span>异常扣款</span><b className="num" style={{ color: "var(--red)" }}>−{yuan(c.exception_deduction)}</b></div>}
        <div><span>毛利</span><b className="num" style={{ color: marginColor }}>{yuan(c.gross_margin)}{c.margin_pct != null ? ` · ${Math.round(c.margin_pct * 100)}%` : ""}</b></div>
      </div>
      <div style={{ marginTop: 10, display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        {c.reconcilable
          ? <span className="tag tag-low">✓ 可对账</span>
          : <span className="tag tag-medium">暂不可对账</span>}
        {c.blockers.map((b, i) => <span key={i} className="tag tag-none">{b}</span>)}
      </div>
    </div>
  );
}
