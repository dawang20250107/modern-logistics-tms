import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import {
  Area, AreaChart, Bar, CartesianGrid, Cell, ComposedChart, Legend, Pie, PieChart,
  ResponsiveContainer, Tooltip, XAxis, YAxis,
} from "recharts";

import { apiGet } from "../api/client";
import { readCssVars, THEME_EVENT } from "../api/theme";
import type { MetricCard } from "../api/types";
import { METRIC_DOMAIN_LABEL } from "../api/types";
import { Sparkline } from "./Sparkline";
import { StateView } from "./StateView";

const CHART_VARS = [
  "--chart-revenue", "--chart-cost", "--chart-profit", "--chart-grid", "--chart-tip-bg", "--chart-tip-ink",
  "--chart-1", "--chart-2", "--chart-3", "--chart-4", "--chart-5", "--chart-6", "--chart-7",
];
// 图表色随主题切换重算（recharts 需要真实色值，不吃 CSS 变量透传）
function useChartTokens() {
  const [tok, setTok] = useState<Record<string, string>>(() => readCssVars(CHART_VARS));
  useEffect(() => {
    const on = () => setTok(readCssVars(CHART_VARS));
    window.addEventListener(THEME_EVENT, on);
    return () => window.removeEventListener(THEME_EVENT, on);
  }, []);
  return tok;
}

// 经营指标（原「经营看板」）：并入运输驾驶舱，作为管理者纵览的经营视角。
// 基于实收实付台账自动聚合：营收/成本/毛利趋势 + 成本构成 + 分域 KPI。

type Trends = Record<string, Array<{ date: string; value: number }>>;

function formatValue(m: MetricCard): string {
  if (m.unit === "%") return `${(m.value * 100).toFixed(1)}%`;
  if (m.unit === "元") return `¥${m.value.toLocaleString()}`;
  return `${m.value.toLocaleString()}${m.unit}`;
}

function trendDelta(points?: Array<{ value: number }>): { dir: "up" | "down" | "flat"; label: string } | null {
  if (!points || points.length < 2) return null;
  const first = points[0].value;
  const last = points[points.length - 1].value;
  if (!isFinite(first) || !isFinite(last)) return null;
  const diff = last - first;
  const base = Math.abs(first) > 1e-9 ? Math.abs(first) : 1;
  const pct = (diff / base) * 100;
  if (Math.abs(pct) < 0.5) return { dir: "flat", label: "持平" };
  const dir = diff > 0 ? "up" : "down";
  return { dir, label: `${diff > 0 ? "▲" : "▼"} ${Math.abs(pct).toFixed(1)}%` };
}

const DOMAIN_ORDER = ["ops", "fleet", "order", "finance"];
const TONE: Record<string, string> = { ops: "blue", fleet: "blue", order: "amber", finance: "" };

const PERIODS: { key: string; label: string; days: number }[] = [
  { key: "day", label: "日", days: 7 },
  { key: "month", label: "月", days: 30 },
  { key: "year", label: "年", days: 365 },
];

export function BusinessMetrics({ days: externalDays }: { days?: number } = {}) {
  const [period, setPeriod] = useState("month");
  const c = useChartTokens();
  const PIE = [c["--chart-1"], c["--chart-2"], c["--chart-3"], c["--chart-4"], c["--chart-5"], c["--chart-6"], c["--chart-7"]];
  const controlled = externalDays != null;
  const days = externalDays ?? (PERIODS.find((p) => p.key === period)?.days ?? 30);

  const dash = useQuery({
    queryKey: ["analytics", "dashboard"],
    queryFn: () => apiGet<{ metrics: MetricCard[]; trends?: Trends }>("/analytics/dashboard?trends=true"),
    refetchInterval: 30000,
  });
  const financeMetrics = useQuery({
    queryKey: ["finance", "dashboard-metrics", days],
    queryFn: () => apiGet<any>(`/finance/dashboard-metrics?days=${days}`),
  });

  const metrics = dash.data?.metrics ?? [];
  const trends = dash.data?.trends ?? {};
  const grouped = DOMAIN_ORDER
    .map((d) => ({ domain: d, items: metrics.filter((m) => m.domain === d) }))
    .filter((g) => g.items.length > 0);
  const formatRmb = (val: number) => `¥${val.toLocaleString()}`;
  const pieData: Array<{ name: string; value: number }> = financeMetrics.data?.cost_composition ?? [];

  return (
    <div className="stack">
      {financeMetrics.data && (
        <div style={{ display: "grid", gridTemplateColumns: "2fr 1fr", gap: 16 }} className="bm-charts">
          <div className="panel" style={{ padding: 18, height: 380, display: "flex", flexDirection: "column" }}>
            <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 16 }}>
              <div className="section-label" style={{ margin: 0 }}>营业额与利润趋势 ({financeMetrics.data.period})</div>
              {!controlled && (
                <div className="seg-toggle">
                  {PERIODS.map((p) => (
                    <button key={p.key} className={`seg-btn${period === p.key ? " on" : ""}`} onClick={() => setPeriod(p.key)}>{p.label}</button>
                  ))}
                </div>
              )}
            </div>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={financeMetrics.data.trend} margin={{ top: 10, right: 0, left: -20, bottom: 0 }}>
                <defs>
                  <linearGradient id="colorRevenue" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={c["--chart-revenue"]} stopOpacity={0.35} />
                    <stop offset="95%" stopColor={c["--chart-2"]} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} stroke={c["--chart-grid"]} />
                <XAxis dataKey="date" axisLine={false} tickLine={false} tick={{ fontSize: 11, fill: "var(--muted)" }} dy={10} />
                <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 11, fill: "var(--muted)" }} tickFormatter={(val) => `¥${val / 1000}k`} />
                <Tooltip contentStyle={{ borderRadius: 10, border: "none", background: c["--chart-tip-bg"], color: c["--chart-tip-ink"], boxShadow: "var(--chart-tip-shadow)", fontSize: 12 }} formatter={(value) => formatRmb(Number(value))} />
                <Legend iconType="circle" wrapperStyle={{ fontSize: 12, paddingTop: 10 }} />
                <Area type="monotone" name="主营收入" dataKey="revenue" fill="url(#colorRevenue)" stroke={c["--chart-revenue"]} strokeWidth={3} />
                <Bar name="外协成本/支出" dataKey="cost" barSize={16} fill={c["--chart-cost"]} radius={[4, 4, 0, 0]} />
                <Area type="monotone" name="毛利润" dataKey="profit" fill="none" stroke={c["--chart-profit"]} strokeWidth={2} strokeDasharray="5 5" />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
          <div className="panel" style={{ padding: 18, height: 380, display: "flex", flexDirection: "column" }}>
            <div className="section-label">车队运营成本构成占比</div>
            {pieData.length === 0 ? (
              <div className="muted" style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", textAlign: "center" }}>
                近 {financeMetrics.data?.period ?? ""} 内暂无应付成本记录
              </div>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
                  <Tooltip formatter={(value) => formatRmb(Number(value))} contentStyle={{ borderRadius: 10, border: "none", background: c["--chart-tip-bg"], color: c["--chart-tip-ink"], boxShadow: "var(--chart-tip-shadow)", fontSize: 12 }} />
                  <Legend iconType="circle" layout="vertical" verticalAlign="middle" align="right" wrapperStyle={{ fontSize: 12 }} />
                  <Pie data={pieData} cx="40%" cy="50%" innerRadius={65} outerRadius={100} paddingAngle={4} dataKey="value" stroke="none">
                    {pieData.map((entry, index) => <Cell key={`cell-${index}`} fill={PIE[index % PIE.length]} />)}
                  </Pie>
                </PieChart>
              </ResponsiveContainer>
            )}
          </div>
        </div>
      )}

      {dash.isLoading ? (
        <StateView kind="loading" compact />
      ) : (
        grouped.map((g) => (
          <div key={g.domain} className="panel">
            <div className="panel-head">{METRIC_DOMAIN_LABEL[g.domain] ?? g.domain}</div>
            <div className="kpi-row" style={{ padding: 16, gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))" }}>
              {g.items.map((m) => {
                const delta = trendDelta(trends[m.code]);
                return (
                  <div key={m.code} className={`kpi${TONE[g.domain] ? ` kpi-${TONE[g.domain]}` : ""}`}>
                    <div className="kpi-top">
                      <span className="kpi-label">{m.name}</span>
                      {delta && <span className={`kpi-delta ${delta.dir}`}>{delta.label}</span>}
                    </div>
                    <div className="kpi-value">{formatValue(m)}</div>
                    {trends[m.code] && trends[m.code].length > 1 && (
                      <div className="kpi-spark"><Sparkline values={trends[m.code].map((p) => p.value)} /></div>
                    )}
                    {m.breakdown && m.breakdown.length > 0 && (
                      <div className="kpi-foot" style={{ flexWrap: "wrap" }}>
                        {m.breakdown.slice(0, 4).map((b) => (
                          <span key={b.key} style={{ background: "var(--panel-3)", padding: "2px 7px", borderRadius: 4 }}>
                            {b.key}: <b style={{ color: "var(--ink-2)" }}>{b.value}</b>
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        ))
      )}
    </div>
  );
}
