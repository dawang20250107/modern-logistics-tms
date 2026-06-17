import { useQuery } from "@tanstack/react-query";

import { apiGet } from "../api/client";
import type { MetricCard } from "../api/types";
import { METRIC_DOMAIN_LABEL } from "../api/types";

function formatValue(m: MetricCard): string {
  if (m.unit === "%") return `${(m.value * 100).toFixed(1)}%`;
  if (m.unit === "元") return `¥${m.value.toLocaleString()}`;
  return `${m.value.toLocaleString()}${m.unit}`;
}

const DOMAIN_ORDER = ["ops", "fleet", "order", "finance"];
const TONE: Record<string, string> = { ops: "blue", fleet: "blue", order: "amber", finance: "" };

export function DashboardPage() {
  const dash = useQuery({
    queryKey: ["analytics", "dashboard"],
    queryFn: () => apiGet<{ metrics: MetricCard[] }>("/analytics/dashboard"),
    refetchInterval: 30000,
  });

  const metrics = dash.data?.metrics ?? [];
  const grouped = DOMAIN_ORDER.map((d) => ({ domain: d, items: metrics.filter((m) => m.domain === d) })).filter(
    (g) => g.items.length > 0,
  );

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">
          经营看板 · 指标中台
          <span className="ai-pill">统一口径</span>
        </div>
        <div className="muted small" style={{ padding: "0 18px 14px" }}>
          全域统一指标，30 秒自动刷新；同源数据供 AI Agent 直接调用做经营分析。
        </div>
      </div>

      {dash.isLoading ? (
        <div className="muted" style={{ padding: 16 }}>加载中…</div>
      ) : (
        grouped.map((g) => (
          <div key={g.domain} className="panel">
            <div className="panel-head">{METRIC_DOMAIN_LABEL[g.domain] ?? g.domain}</div>
            <div className="kpi-row" style={{ padding: 16, gridTemplateColumns: "repeat(4, 1fr)" }}>
              {g.items.map((m) => (
                <div key={m.code} className={`kpi${TONE[g.domain] ? ` kpi-${TONE[g.domain]}` : ""}`}>
                  <div className="kpi-value">{formatValue(m)}</div>
                  <div className="kpi-label">{m.name}</div>
                  {m.breakdown && m.breakdown.length > 0 && (
                    <div className="small muted" style={{ marginTop: 8, display: "flex", flexWrap: "wrap", gap: 8 }}>
                      {m.breakdown.slice(0, 4).map((b) => (
                        <span key={b.key}>{b.key} {b.value}</span>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        ))
      )}
    </div>
  );
}
