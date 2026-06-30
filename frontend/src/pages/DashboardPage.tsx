import { useQuery } from "@tanstack/react-query";
import {
  Area,
  AreaChart,
  Bar,
  CartesianGrid,
  Cell,
  ComposedChart,
  Legend,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { apiGet } from "../api/client";
import type { MetricCard } from "../api/types";
import { METRIC_DOMAIN_LABEL } from "../api/types";
import { Sparkline } from "../components/Sparkline";

type Trends = Record<string, Array<{ date: string; value: number }>>;

function formatValue(m: MetricCard): string {
  if (m.unit === "%") return `${(m.value * 100).toFixed(1)}%`;
  if (m.unit === "元") return `¥${m.value.toLocaleString()}`;
  return `${m.value.toLocaleString()}${m.unit}`;
}

const DOMAIN_ORDER = ["ops", "fleet", "order", "finance"];
const TONE: Record<string, string> = { ops: "blue", fleet: "blue", order: "amber", finance: "" };

// ECharts/Recharts 配色方案
const COLORS = ["#2563eb", "#0ea5e9", "#10b981", "#f59e0b", "#8b5cf6", "#ef4444", "#64748b"];

export function DashboardPage() {
  const dash = useQuery({
    queryKey: ["analytics", "dashboard"],
    queryFn: () => apiGet<{ metrics: MetricCard[]; trends?: Trends }>("/analytics/dashboard?trends=true"),
    refetchInterval: 30000,
  });

  const financeMetrics = useQuery({
    queryKey: ["finance", "dashboard-metrics"],
    queryFn: () => apiGet<any>("/finance/dashboard-metrics?days=14"),
  });

  const metrics = dash.data?.metrics ?? [];
  const trends = dash.data?.trends ?? {};
  const grouped = DOMAIN_ORDER.map((d) => ({ domain: d, items: metrics.filter((m) => m.domain === d) })).filter(
    (g) => g.items.length > 0,
  );

  const formatRmb = (val: number) => `¥${val.toLocaleString()}`;

  const pieData = financeMetrics.data?.fleet_costs
    ? [
        { name: "燃油费", value: financeMetrics.data.fleet_costs.fuel },
        { name: "路桥费", value: financeMetrics.data.fleet_costs.toll },
        { name: "维保费", value: financeMetrics.data.fleet_costs.maintenance },
        { name: "三方承运", value: financeMetrics.data.fleet_costs.carrier_fee },
        { name: "其他杂费", value: financeMetrics.data.fleet_costs.other },
      ].filter((d) => d.value > 0)
    : [];

  return (
    <div className="stack">
      <div className="panel" style={{ background: "linear-gradient(135deg, #1e293b 0%, #0f172a 100%)", color: "#fff", border: "none" }}>
        <div style={{ padding: "20px 24px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <div>
            <div style={{ fontSize: 22, fontWeight: "bold", display: "flex", alignItems: "center", gap: 10 }}>
              经营看盘 · 高级可视化大屏
              <span className="tag" style={{ background: "rgba(37,99,235,0.2)", border: "1px solid rgba(37,99,235,0.4)", color: "#93c5fd" }}>BI · ECharts</span>
            </div>
            <div style={{ fontSize: 13, color: "#94a3b8", marginTop: 6 }}>
              基于实收实付台账自动生成；全域统一数据元供底层 AI 直接调用测算。
            </div>
          </div>
        </div>
      </div>

      {/* === 财务可视化图表矩阵 === */}
      {financeMetrics.data && (
        <div style={{ display: "grid", gridTemplateColumns: "2fr 1fr", gap: 16 }}>
          {/* 左侧：营业额与利润趋势组合图 */}
          <div className="panel" style={{ padding: 18, height: 380, display: "flex", flexDirection: "column" }}>
            <div className="section-label" style={{ marginBottom: 16 }}>
              📈 营业额与利润趋势 ({financeMetrics.data.period})
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
                <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 11, fill: "var(--muted)" }} tickFormatter={(val) => `¥${val/1000}k`} />
                <Tooltip 
                  contentStyle={{ borderRadius: 8, border: "none", boxShadow: "0 10px 24px rgba(0,0,0,0.1)", fontSize: 12 }} 
                  formatter={(value) => formatRmb(Number(value))} 
                />
                <Legend iconType="circle" wrapperStyle={{ fontSize: 12, paddingTop: 10 }} />
                <Area type="monotone" name="主营收入" dataKey="revenue" fill="url(#colorRevenue)" stroke="#2563eb" strokeWidth={3} />
                <Bar name="外协成本/支出" dataKey="cost" barSize={16} fill="#94a3b8" radius={[4, 4, 0, 0]} />
                <Area type="monotone" name="毛利润" dataKey="profit" fill="none" stroke="#10b981" strokeWidth={2} strokeDasharray="5 5" />
              </ComposedChart>
            </ResponsiveContainer>
          </div>

          {/* 右侧：车队成本构成 Donut 图 */}
          <div className="panel" style={{ padding: 18, height: 380, display: "flex", flexDirection: "column" }}>
            <div className="section-label">
              🍩 车队运营成本构成占比
            </div>
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Tooltip 
                  formatter={(value) => formatRmb(Number(value))}
                  contentStyle={{ borderRadius: 8, border: "none", boxShadow: "0 10px 24px rgba(0,0,0,0.1)", fontSize: 12 }} 
                />
                <Legend iconType="circle" layout="vertical" verticalAlign="middle" align="right" wrapperStyle={{ fontSize: 12 }} />
                <Pie
                  data={pieData}
                  cx="40%"
                  cy="50%"
                  innerRadius={65}
                  outerRadius={100}
                  paddingAngle={4}
                  dataKey="value"
                  stroke="none"
                >
                  {pieData.map((entry, index) => (
                    <Cell key={`cell-${index}`} fill={COLORS[index % COLORS.length]} />
                  ))}
                </Pie>
              </PieChart>
            </ResponsiveContainer>
          </div>
        </div>
      )}

      {/* 原有 KPI 核心看板 */}
      {dash.isLoading ? (
        <div className="muted" style={{ padding: 16 }}>加载中…</div>
      ) : (
        grouped.map((g) => (
          <div key={g.domain} className="panel">
            <div className="panel-head">{METRIC_DOMAIN_LABEL[g.domain] ?? g.domain}</div>
            <div className="kpi-row" style={{ padding: 16, gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))" }}>
              {g.items.map((m) => (
                <div key={m.code} className={`kpi${TONE[g.domain] ? ` kpi-${TONE[g.domain]}` : ""}`}>
                  <div className="kpi-value">{formatValue(m)}</div>
                  <div className="kpi-label">{m.name}</div>
                  {trends[m.code] && trends[m.code].length > 1 && (
                    <div style={{ marginTop: 12 }}>
                      <Sparkline values={trends[m.code].map((p) => p.value)} />
                    </div>
                  )}
                  {m.breakdown && m.breakdown.length > 0 && (
                    <div className="small muted" style={{ marginTop: 12, display: "flex", flexWrap: "wrap", gap: 6 }}>
                      {m.breakdown.slice(0, 4).map((b) => (
                        <span key={b.key} style={{ background: "var(--bg)", padding: "2px 6px", borderRadius: 4 }}>
                          {b.key}: <b>{b.value}</b>
                        </span>
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
