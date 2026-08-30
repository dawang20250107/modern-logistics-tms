import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import {
  Area, AreaChart, Bar, CartesianGrid, Cell, ComposedChart, Legend, Pie, PieChart,
  ResponsiveContainer, Tooltip, XAxis, YAxis,
} from "recharts";

import { apiGet } from "../api/client";
import { EMPTY, fmtMoney, fmtNum0, fmtWan } from "../api/format";
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
  // 空 ≠ 零。比率类指标的分母为 0 意味着「这段时间没有可统计的单」，
  // 后端此时回 value=0，前端若照直显示 0.0%，看板上写的就是
  // 「准班率 0%」「运力在线率 0%」——把"没数据"说成了"表现极差"。
  // 这条规则在 api/format.ts 里早就为金额定下了（fmtMoney(null) 返回 —
  // 而不是 ¥0.00），比率没有理由例外。
  if (m.unit === "%" && m.denominator === 0) return EMPTY;
  if (m.unit === "%") return `${(m.value * 100).toFixed(1)}%`;
  if (m.unit === "元") return fmtMoney(m.value);
  return `${fmtNum0(m.value, 0)}${m.unit}`;
}

/** 比率指标的样本量说明。显示 — 的时候必须说清为什么，否则看的人
 *  只会以为是加载失败。 */
function sampleNote(m: MetricCard): string | undefined {
  if (m.unit !== "%" || m.denominator === undefined) return undefined;
  if (m.denominator === 0) return "本期无可统计样本";
  return `样本 ${m.numerator ?? 0} / ${m.denominator}`;
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
  const formatRmb = (val: number) => fmtMoney(val);
  const pieData: Array<{ name: string; value: number }> = financeMetrics.data?.cost_composition ?? [];

  // 稀疏度决定图形：营收是离散的成单事件，不是连续的水位。
  // 三十天里只有两天有数时，面积图会把两根尖峰之间连成一条穿过零的斜坡，
  // 看着像"业务在缓慢下滑"——实际那些天什么都没发生。这种时候柱状图才诚实：
  // 有柱子就是有单，没柱子就是没单，中间不编故事。
  const trendPoints: Array<{ date: string; revenue: number; cost: number; profit: number }> =
    financeMetrics.data?.trend ?? [];
  const busyDays = trendPoints.filter((p) => p.revenue || p.cost);
  const sparse = trendPoints.length >= 7 && busyDays.length / trendPoints.length < 0.3;
  // 稀疏时只画有业务的那几天。三十个日期槽里画两根柱子，剩下 28 个空槽既不说明
  // "那几天为零"（本来就没单），又把两根柱子压成头发丝。横轴换成实际发生的日子，
  // 标题里写清一共几天——信息一点没少，可读性天差地别。
  const chartData = sparse ? busyDays : trendPoints;

  return (
    <div className="stack">
      {financeMetrics.data && (
        <div className="bm-charts">
          <div className="panel bm-chart">
            <div className="cluster-between" style={{ marginBottom: 10 }}>
              <div className="section-label" style={{ margin: 0 }}>
                营业额与利润 ({financeMetrics.data.period})
                {sparse && (
                  <span className="muted small" style={{ marginLeft: 8, fontWeight: 400 }}>
                    · 近 {trendPoints.length} 天中 {busyDays.length} 天有业务
                  </span>
                )}
              </div>
              {!controlled && (
                <div className="seg-toggle">
                  {PERIODS.map((p) => (
                    <button key={p.key} className={`seg-btn${period === p.key ? " on" : ""}`} onClick={() => setPeriod(p.key)}>{p.label}</button>
                  ))}
                </div>
              )}
            </div>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={chartData} margin={{ top: 10, right: 8, left: -20, bottom: 0 }}>
                <defs>
                  <linearGradient id="colorRevenue" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={c["--chart-revenue"]} stopOpacity={0.35} />
                    <stop offset="95%" stopColor={c["--chart-2"]} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} stroke={c["--chart-grid"]} />
                <XAxis dataKey="date" axisLine={false} tickLine={false} tick={{ fontSize: 11, fill: "var(--muted)" }} dy={10} />
                <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 11, fill: "var(--muted)" }} tickFormatter={(val) => fmtWan(Number(val), true)} />
                <Tooltip contentStyle={{ borderRadius: 4, border: "none", background: c["--chart-tip-bg"], color: c["--chart-tip-ink"], boxShadow: "var(--chart-tip-shadow)", fontSize: 12 }} formatter={(value) => formatRmb(Number(value))} />
                <Legend iconType="circle" wrapperStyle={{ fontSize: 12, paddingTop: 10 }} />
                {sparse ? (
                  <>
                    <Bar name="主营收入" dataKey="revenue" maxBarSize={44} fill={c["--chart-revenue"]} radius={0} />
                    <Bar name="外协成本/支出" dataKey="cost" maxBarSize={44} fill={c["--chart-cost"]} radius={0} />
                    <Bar name="毛利润" dataKey="profit" maxBarSize={44} fill={c["--chart-profit"]} radius={0} />
                  </>
                ) : (
                  <>
                    <Area type="monotone" name="主营收入" dataKey="revenue" fill="url(#colorRevenue)" stroke={c["--chart-revenue"]} strokeWidth={3} />
                    <Bar name="外协成本/支出" dataKey="cost" barSize={16} fill={c["--chart-cost"]} radius={0} />
                    <Area type="monotone" name="毛利润" dataKey="profit" fill="none" stroke={c["--chart-profit"]} strokeWidth={2} strokeDasharray="5 5" />
                  </>
                )}
              </ComposedChart>
            </ResponsiveContainer>
          </div>
          <div className="panel bm-chart">
            <div className="section-label">车队运营成本构成占比</div>
            {pieData.length === 0 ? (
              <div className="muted" style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", textAlign: "center" }}>
                {/* period 后端给的就是「近 N 天」，自带「近」字。
                    前面再拼一个就成了「近 近 30 天 内暂无…」——这行字在驾驶舱首屏，
                    是新客户第一眼会看到的地方。同一个 period 在上面标题里
                    （营业额与利润 (近 30 天)）用法是对的，只有这一处多加了。 */}
                {financeMetrics.data?.period ?? "本期"}内暂无应付成本记录
              </div>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
                  <Tooltip formatter={(value) => formatRmb(Number(value))} contentStyle={{ borderRadius: 4, border: "none", background: c["--chart-tip-bg"], color: c["--chart-tip-ink"], boxShadow: "var(--chart-tip-shadow)", fontSize: 12 }} />
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
      ) : dash.isError ? (
        <StateView kind="error" hint="经营指标暂时无法加载。" error={dash.error} onRetry={() => dash.refetch()} compact />
      ) : (
        grouped.map((g) => (
          <div key={g.domain} className="panel">
            <div className="panel-head">{METRIC_DOMAIN_LABEL[g.domain] ?? g.domain}</div>
            {/* 试过给格子封宽度上限（3 个指标铺满 1007px 时每格 335px，内容只占左边 140px）。
                量下来不划算：auto-fit 的列数是按**上限**算的，封 240px 得 4 列、封 260px 只剩 3 列，
                两种都会让原本一行放得下的 4 个指标折行，面板反而高 24–40px。
                真正让读数散架的是涨跌幅被 space-between 甩到格子右边，那个已在 .kpi-top 修掉；
                格子偏宽只是留白多，不影响读。留白比折行便宜，所以宽度维持 1fr。 */}
            <div className="kpi-row" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(168px, 1fr))" }}>
              {g.items.map((m) => {
                const delta = trendDelta(trends[m.code]);
                return (
                  <div key={m.code} className={`kpi${TONE[g.domain] ? ` kpi-${TONE[g.domain]}` : ""}`}>
                    <div className="kpi-top">
                      <span className="kpi-label">{m.name}</span>
                      {delta && <span className={`kpi-delta ${delta.dir}`}>{delta.label}</span>}
                    </div>
                    <div className="kpi-value" title={sampleNote(m)}>{formatValue(m)}</div>
                    {m.denominator === 0 && <div className="kpi-note">本期无可统计样本</div>}
                    {trends[m.code] && trends[m.code].length > 1 && (
                      <div className="kpi-spark"><Sparkline values={trends[m.code].map((p) => p.value)} /></div>
                    )}
                    {m.breakdown && m.breakdown.length > 0 && (
                      <div className="kpi-foot" style={{ flexWrap: "wrap" }}>
                        {m.breakdown.slice(0, 4).map((b) => (
                          <span key={b.key} className="kpi-foot-item">
                            {b.label ?? b.key} <b>{b.value}</b>
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
