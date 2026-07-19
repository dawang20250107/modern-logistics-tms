import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import {
  Area, AreaChart, Bar, CartesianGrid, Cell, ComposedChart, Legend, Pie, PieChart,
  ResponsiveContainer, Tooltip, XAxis, YAxis,
} from "recharts";

import { apiGet } from "../api/client";
import type { MetricCard } from "../api/types";
import { METRIC_DOMAIN_LABEL } from "../api/types";
import { Sparkline } from "./Sparkline";
import { StateView } from "./StateView";

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
const COLORS = ["#2563eb", "#0ea5e9", "#10b981", "#f59e0b", "#8b5cf6", "#ef4444", "#64748b"];

const PERIODS: { key: string; label: string; days: number }[] = [
  { key: "day", label: "日", days: 7 },
  { key: "month", label: "月", days: 30 },
  { key: "year", label: "年", days: 365 },
];

export function BusinessMetrics() {
  const [period, setPeriod] = useState("month");
  const days = PERIODS.find((p) => p.key === period)?.days ?? 30;

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
              <div className="seg-toggle">
                {PERIODS.map((p) => (
                  <button key={p.key} className={`seg-btn${period === p.key ? " on" : ""}`} onClick={() => setPeriod(p.key)}>{p.label}</button>
                ))}
              </div>
            </div>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={financeMetrics.data.trend} margin={{ top: 10, right: 0, left: -20, bottom: 0 }}>
                <defs>
                  <linearGradient id="colorRevenue" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="#2563eb" stopOpacity={0.3} />
                    <stop offset="95%" stopColor="#2563eb" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="var(--line)" />
                <XAxis dataKey="date" axisLine={false} tickLine={false} tick={{ fontSize: 11, fill: "var(--muted)" }} dy={10} />
                <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 11, fill: "var(--muted)" }} tickFormatter={(val) => `¥${val / 1000}k`} />
                <Tooltip contentStyle={{ borderRadius: 8, border: "none", boxShadow: "0 10px 24px rgba(0,0,0,0.1)", fontSize: 12 }} formatter={(value) => formatRmb(Number(value))} />
                <Legend iconType="circle" wrapperStyle={{ fontSize: 12, paddingTop: 10 }} />
                <Area type="monotone" name="主营收入" dataKey="revenue" fill="url(#colorRevenue)" stroke="#2563eb" strokeWidth={3} />
                <Bar name="外协成本/支出" dataKey="cost" barSize={16} fill="#94a3b8" radius={[4, 4, 0, 0]} />
                <Area type="monotone" name="毛利润" dataKey="profit" fill="none" stroke="#10b981" strokeWidth={2} strokeDasharray="5 5" />
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
                  <Tooltip formatter={(value) => formatRmb(Number(value))} contentStyle={{ borderRadius: 8, border: "none", boxShadow: "0 10px 24px rgba(0,0,0,0.1)", fontSize: 12 }} />
                  <Legend iconType="circle" layout="vertical" verticalAlign="middle" align="right" wrapperStyle={{ fontSize: 12 }} />
                  <Pie data={pieData} cx="40%" cy="50%" innerRadius={65} outerRadius={100} paddingAngle={4} dataKey="value" stroke="none">
                    {pieData.map((entry, index) => <Cell key={`cell-${index}`} fill={COLORS[index % COLORS.length]} />)}
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
