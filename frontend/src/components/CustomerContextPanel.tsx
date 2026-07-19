import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";

import { apiGet } from "../api/client";
import type { CustomerContext } from "../api/types";

function money(n: number): string {
  return `¥${Math.round(n).toLocaleString()}`;
}

// 客服工作台：选中客户即带出上下文（账期/授信/常用线路地址/未完成·异常·回单未返）
export function CustomerContextPanel({ customerId }: { customerId: string }) {
  const q = useQuery({
    queryKey: ["customer-context", customerId],
    queryFn: () => apiGet<CustomerContext>(`/customers/${customerId}/context`),
    enabled: Boolean(customerId),
  });

  if (!customerId) {
    return (
      <div className="panel" style={{ padding: 20 }}>
        <div className="muted small">选择签约客户后，这里显示客户上下文：账期授信、常用线路与地址、未完成/异常/回单未返订单，辅助快速接单与查单回复。</div>
      </div>
    );
  }
  if (q.isLoading) return <div className="panel muted" style={{ padding: 16 }}>加载客户上下文…</div>;
  const c = q.data;
  if (!c) return <div className="panel" style={{ padding: 16 }}>未取到客户上下文。</div>;

  const cr = c.credit;
  return (
    <div className="panel" style={{ position: "sticky", top: 12 }}>
      <div className="panel-head">
        <span style={{ fontSize: 15, fontWeight: 650 }}>{c.name}</span>
        <span className="muted small">{c.profile.settlement_type === "monthly" ? "月结" : c.profile.settlement_type || "—"} · 账期 {c.profile.credit_days ?? "—"} 天</span>
      </div>

      {/* 关键计数 */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(3,1fr)", gap: 8, padding: "12px 16px" }}>
        <div className="stat-mini"><div className="stat-mini-n">{c.counts.open}</div><div className="stat-mini-l">未完成</div></div>
        <div className="stat-mini"><div className="stat-mini-n" style={{ color: c.counts.exceptions ? "var(--red)" : undefined }}>{c.counts.exceptions}</div><div className="stat-mini-l">异常</div></div>
        <div className="stat-mini"><div className="stat-mini-n" style={{ color: c.counts.receipt_pending ? "var(--amber)" : undefined }}>{c.counts.receipt_pending}</div><div className="stat-mini-l">回单未返</div></div>
      </div>

      {/* 授信 */}
      {cr.limit > 0 && (
        <div style={{ padding: "0 16px 12px" }}>
          <div className="muted small" style={{ marginBottom: 4, display: "flex", justifyContent: "space-between" }}>
            <span>授信占用</span>
            <span style={{ color: cr.over_limit ? "var(--red)" : "var(--ink-2)" }}>{money(cr.outstanding)} / {money(cr.limit)}</span>
          </div>
          <div style={{ height: 6, borderRadius: 4, background: "var(--panel-3)", overflow: "hidden" }}>
            <div style={{ width: `${Math.min((cr.used_pct ?? 0) * 100, 100)}%`, height: "100%", background: cr.over_limit ? "var(--red)" : "var(--accent)" }} />
          </div>
          {cr.over_limit && <div className="tag tag-high" style={{ marginTop: 6 }}>已超授信额度，需财务确认后放单</div>}
        </div>
      )}

      {/* 常用线路 */}
      {c.common_routes.length > 0 && (
        <div style={{ padding: "0 16px 12px" }}>
          <div className="section-label" style={{ marginBottom: 6 }}>常用线路</div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
            {c.common_routes.map((r) => <span key={r} className="tag tag-info">{r}</span>)}
          </div>
        </div>
      )}

      {/* 常用收货地址 */}
      {c.common_deliveries.length > 0 && (
        <div style={{ padding: "0 16px 12px" }}>
          <div className="section-label" style={{ marginBottom: 6 }}>常用收货地址</div>
          <div className="stack" style={{ gap: 4 }}>
            {c.common_deliveries.slice(0, 3).map((a, i) => (
              <div key={i} className="small" style={{ color: "var(--ink-2)" }}>
                {a.address} <span className="muted">· {a.contact_name} {a.contact_phone}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* 未完成订单 */}
      {c.open_orders.length > 0 && (
        <div style={{ padding: "0 16px 14px" }}>
          <div className="section-label" style={{ marginBottom: 6 }}>未完成订单</div>
          <table className="table" style={{ fontSize: 12.5 }}>
            <tbody>
              {c.open_orders.slice(0, 6).map((o) => (
                <tr key={o.order_no}>
                  <td><Link className="link mono" to={`/orders/${o.order_no}`}>{o.order_no}</Link></td>
                  <td className="small">{o.route}</td>
                  <td><span className="tag tag-info">{o.status_label}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
