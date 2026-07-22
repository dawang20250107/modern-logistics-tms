import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet } from "../api/client";
import { fmtMoney } from "../api/format";
import type { CredentialRow, ExpiringCredentials, Order, StatementOverview } from "../api/types";
import { ORDER_STATUS_LABEL, STATUS_LABEL } from "../api/types";
import { useEventStream } from "../api/useEventStream";
import { hasPerm, useAuth } from "../auth/auth";
import { BusinessMetrics } from "../components/BusinessMetrics";
import { StateView } from "../components/StateView";
import {
  IconArrowRight, IconBox, IconMoney, IconReceipt, IconShield, IconTruck, IconWarning,
} from "../components/Icons";

// 驾驶舱日期联动：驱动经营指标仪表（近 N 天）
const CT_PERIODS: { days: number; label: string }[] = [
  { days: 7, label: "近7天" },
  { days: 30, label: "近30天" },
  { days: 90, label: "近90天" },
  { days: 365, label: "近1年" },
];

// 运单生命周期分段（顺序 + 配色），用于「运单状态分布」条
const WB_PIPELINE: { key: string; color: string }[] = [
  { key: "pending_dispatch", color: "var(--muted)" },
  { key: "dispatched", color: "var(--blue)" },
  { key: "loaded", color: "var(--blue)" },
  { key: "departed", color: "var(--accent)" },
  { key: "in_transit", color: "var(--accent-2)" },
  { key: "arrived", color: "var(--accent-cyan)" },
  { key: "signed", color: "var(--green)" },
  { key: "delivered", color: "var(--green)" },
  { key: "settled", color: "var(--green)" },
];
const WB_ACTIVE = ["dispatched", "loaded", "departed", "in_transit", "arrived"];

// 大额金额紧凑显示（¥万/¥亿），驾驶舱磁贴用；带 ¥ 前缀与其它金额风格统一
function fmtWan(v: number): string {
  const n = Math.abs(v);
  if (n >= 1e8) return `¥${(v / 1e8).toFixed(2)}亿`;
  if (n >= 1e4) return `¥${(v / 1e4).toFixed(1)}万`;
  return fmtMoney(v);
}

type WbStats = { by_status: Record<string, number>; total: number };
type Funnel = { by_status: Record<string, number>; by_channel: Record<string, number>; today_created: number; total: number };
type Workbench = {
  common: { unread_notifications: number; my_open_exceptions: number };
  cs: { my_orders_pending_confirm: number; my_orders_today: number; recent_pending: Order[] };
  dispatch: { pool_count: number; my_claimed: number; pool_top: Order[] };
  finance: { draft_statements: number };
};

const SEV_TONE: Record<string, string> = { expired: "high", critical: "medium", warning: "low" };

export function ControlTowerPage() {
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const [days, setDays] = useState(30);

  const stats = useQuery({ queryKey: ["waybills", "stats"], queryFn: () => apiGet<WbStats>("/waybills/stats"), refetchInterval: 30000 });
  const funnel = useQuery({ queryKey: ["orders", "funnel"], queryFn: () => apiGet<Funnel>("/orders/funnel"), refetchInterval: 30000 });
  const wb = useQuery({ queryKey: ["workbench"], queryFn: () => apiGet<Workbench>("/workbench"), refetchInterval: 30000 });
  const fin = useQuery({ queryKey: ["statement-overview"], queryFn: () => apiGet<StatementOverview>("/finance/statement-overview"), refetchInterval: 60000 });
  const compliance = useQuery({ queryKey: ["compliance-mini"], queryFn: () => apiGet<ExpiringCredentials>("/credentials/expiring?days=30"), refetchInterval: 60000 });

  // 实时事件：到达即刷新看板
  useEventStream(() => {
    queryClient.invalidateQueries({ queryKey: ["waybills"] });
    queryClient.invalidateQueries({ queryKey: ["orders"] });
  });

  const primaryQueries = [stats, funnel, wb];
  if (primaryQueries.some((q) => q.isLoading)) return <StateView kind="loading" />;
  if (primaryQueries.some((q) => q.isError)) {
    return (
      <StateView
        kind="error"
        hint="驾驶舱核心数据暂时无法同步，请重试。"
        onRetry={() => primaryQueries.forEach((q) => q.refetch())}
      />
    );
  }

  const byStatus = stats.data?.by_status ?? {};
  const wbTotal = stats.data?.total ?? 0;
  const inTransit = WB_ACTIVE.reduce((s, k) => s + (byStatus[k] ?? 0), 0);
  const fb = funnel.data?.by_status ?? {};
  const pooled = (fb.pooled ?? 0) + (fb.dispatching ?? 0);
  const ov = fin.data;
  const credSum = compliance.data?.summary;
  const credAlert = (credSum?.expired ?? 0) + (credSum?.critical ?? 0);

  // 顶部指挥磁贴（全部真实服务端聚合，点击直达对应工作台）
  const hero = [
    { key: "today", icon: <IconBox size={17} />, label: "今日建单", value: String(funnel.data?.today_created ?? 0), sub: `订单总量 ${funnel.data?.total ?? 0}`, tone: "", to: "/intake" },
    { key: "transit", icon: <IconTruck size={17} />, label: "在途运单", value: String(inTransit), sub: `运单总量 ${wbTotal}`, tone: "blue", to: "/waybills" },
    { key: "pool", icon: <IconArrowRight size={17} />, label: "池中待派", value: String(pooled), sub: pooled > 0 ? "去调度派单 →" : "暂无待派", tone: "amber", to: "/dispatch-board" },
    { key: "ar", icon: <IconMoney size={17} />, label: "应收敞口", value: fin.isLoading ? "…" : fin.isError ? "—" : fmtWan(ov?.receivable.outstanding ?? 0), sub: fin.isLoading ? "正在同步财务数据" : fin.isError ? "财务数据暂不可用" : `应收单据 ${ov?.receivable.count ?? 0} 张`, tone: "grad", to: "/reconciliation" },
    { key: "overdue", icon: <IconWarning size={17} />, label: "逾期应收", value: fin.isLoading ? "…" : fin.isError ? "—" : fmtWan(ov?.overdue.receivable.amount ?? 0), sub: fin.isLoading ? "正在同步财务数据" : fin.isError ? "财务数据暂不可用" : `${ov?.overdue.receivable.count ?? 0} 张逾期`, tone: (ov?.overdue.receivable.amount ?? 0) > 0 ? "red" : "", to: "/reconciliation" },
    { key: "cred", icon: <IconShield size={17} />, label: "证件预警", value: compliance.isLoading ? "…" : compliance.isError ? "—" : String(credAlert), sub: compliance.isLoading ? "正在同步证件数据" : compliance.isError ? "证件数据暂不可用" : credAlert > 0 ? `${credSum?.expired ?? 0} 过期 · ${credSum?.critical ?? 0} 紧急` : "30 天内无临期", tone: credAlert > 0 ? "red" : "", to: "/fleet" },
  ];

  // 运单状态分布分段
  const segTotal = WB_PIPELINE.reduce((s, p) => s + (byStatus[p.key] ?? 0), 0) || 1;
  const segments = WB_PIPELINE.filter((p) => (byStatus[p.key] ?? 0) > 0);

  // 我的待办（可操作清单）：先计数快捷，再列最近待确认订单直达
  const w = wb.data;
  const todoChips = w ? [
    { label: "待确认", value: w.cs.my_orders_pending_confirm, to: "/intake", tone: "amber" },
    { label: "我认领", value: w.dispatch.my_claimed, to: "/dispatch-board", tone: "blue" },
    { label: "待对账", value: w.finance.draft_statements, to: "/reconciliation", tone: "" },
    { label: "我的异常", value: w.common.my_open_exceptions, to: "/dispatch-board", tone: "red" },
  ].filter((t) => t.value > 0) : [];
  // 首屏待办共用 4 行预算，避免任务卡无限增长把财务敞口推出视口。
  const pendingList = (w?.cs.recent_pending ?? []).slice(0, 3);
  const poolList = (w?.dispatch.pool_top ?? []).slice(0, Math.max(0, 4 - pendingList.length));

  const credRows: CredentialRow[] = [
    ...(compliance.data?.vehicles ?? []),
    ...(compliance.data?.drivers ?? []),
  ].sort((a, b) => a.days_left - b.days_left).slice(0, 5);

  const topAr = (ov?.top_receivable ?? []).slice(0, 4);
  const topArMax = Math.max(1, ...topAr.map((t) => t.outstanding));

  return (
    <div className="stack ctower">
      {/* 指挥磁贴带 */}
      <div className="ct-hero">
        {hero.map((h) => (
          <Link key={h.key} to={h.to} className={`kpi ct-tile${h.tone && h.tone !== "grad" ? ` kpi-${h.tone}` : ""}`}>
            <div className="ct-tile-top">
              <span className="ct-tile-ic">{h.icon}</span>
              <span className="kpi-label">{h.label}</span>
            </div>
            <div className={`kpi-value${h.tone === "grad" ? "" : ""}`}>{h.value}</div>
            <div className="ct-tile-sub">{h.sub}</div>
          </Link>
        ))}
      </div>

      <div className="ct-grid">
        {/* 主列 */}
        <div className="stack" style={{ minWidth: 0 }}>
          {/* 运单状态分布 */}
          <div className="panel">
            <div className="panel-head">
              运单状态分布
              <Link className="link small" to="/waybills">全部运单 →</Link>
            </div>
            <div className="ct-dist">
              {wbTotal === 0 ? (
                <div className="muted small" style={{ padding: "10px 2px" }}>暂无在册运单。</div>
              ) : (
                <>
                  <div className="ct-bar" role="img" aria-label="运单状态分布">
                    {segments.map((p) => {
                      const n = byStatus[p.key] ?? 0;
                      return <span key={p.key} className="ct-bar-seg" style={{ width: `${(n / segTotal) * 100}%`, background: p.color }} title={`${STATUS_LABEL[p.key] ?? p.key} ${n}`} />;
                    })}
                  </div>
                  <div className="ct-legend">
                    {segments.map((p) => (
                      <span key={p.key} className="ct-legend-item">
                        <span className="ct-dot" style={{ background: p.color }} />
                        {STATUS_LABEL[p.key] ?? p.key}
                        <b>{byStatus[p.key] ?? 0}</b>
                      </span>
                    ))}
                  </div>
                </>
              )}
            </div>
          </div>

          {/* 经营指标（有权限才显示） */}
          {hasPerm(user, "analytics.view") && (
            <>
              <div className="section-label ct-metrics-head">
                经营指标
                <div className="seg-toggle ct-period">
                  {CT_PERIODS.map((p) => (
                    <button key={p.days} className={`seg-btn${days === p.days ? " on" : ""}`} onClick={() => setDays(p.days)}>{p.label}</button>
                  ))}
                </div>
              </div>
              <BusinessMetrics days={days} />
            </>
          )}
        </div>

        {/* 侧列 */}
        <div className="stack ct-side">
          {/* 我的待办 */}
          <div className="panel">
            <div className="panel-head">我的待办</div>
            <div className="ct-side-body">
              {todoChips.length > 0 && (
                <div className="ct-chips">
                  {todoChips.map((t) => (
                    <Link key={t.label} to={t.to} className={`ct-chip${t.tone ? ` ct-chip-${t.tone}` : ""}`}>
                      {t.label}<b>{t.value}</b>
                    </Link>
                  ))}
                </div>
              )}
              {pendingList.length > 0 && <div className="ct-sub-label">待我确认订单</div>}
              {pendingList.map((o) => (
                <Link key={o.id} to={`/orders/${o.id}`} className="ct-row">
                  <span className="mono small ct-row-no">{o.order_no}</span>
                  <span className="ct-row-main">{o.customer_name || "散客"} · {o.origin || "?"}→{o.destination || "?"}</span>
                  <IconArrowRight size={13} className="ct-row-go" />
                </Link>
              ))}
              {poolList.length > 0 && <div className="ct-sub-label">池中待派（优先）</div>}
              {poolList.map((o) => (
                <Link key={o.id} to="/dispatch-board" className="ct-row">
                  <span className="mono small ct-row-no">{o.order_no}</span>
                  <span className="ct-row-main">{o.customer_name || "散客"} · {ORDER_STATUS_LABEL[o.status] ?? o.status}</span>
                  <IconArrowRight size={13} className="ct-row-go" />
                </Link>
              ))}
              {todoChips.length === 0 && pendingList.length === 0 && poolList.length === 0 && (
                <div className="muted small" style={{ padding: "6px 2px" }}>当前没有待办，保持在线。</div>
              )}
            </div>
          </div>

          {/* 财务敞口 */}
          <div className="panel">
            <div className="panel-head">
              <span style={{ display: "inline-flex", alignItems: "center", gap: 7 }}><IconReceipt size={15} />财务敞口</span>
              <Link className="link small" to="/reconciliation">对账中心 →</Link>
            </div>
            <div className="ct-side-body">
              {fin.isLoading ? <StateView kind="loading" compact /> : fin.isError ? (
                <StateView kind="error" hint="财务敞口暂时无法同步。" compact onRetry={() => fin.refetch()} />
              ) : <><div className="ct-expo">
                <div><span>应收未结</span><b className="num-grad">{fmtWan(ov?.receivable.outstanding ?? 0)}</b></div>
                <div><span>应付未结</span><b>{fmtWan(ov?.payable.outstanding ?? 0)}</b></div>
                <div><span>净头寸</span><b style={{ color: (ov?.net_position ?? 0) >= 0 ? "var(--green)" : "var(--red)" }}>{fmtWan(ov?.net_position ?? 0)}</b></div>
                <div><span>逾期应收</span><b style={{ color: (ov?.overdue.receivable.amount ?? 0) > 0 ? "var(--red)" : undefined }}>{fmtWan(ov?.overdue.receivable.amount ?? 0)}</b></div>
              </div>
              {topAr.length > 0 && <div className="ct-sub-label">应收 Top 对手方</div>}
              {topAr.map((t) => (
                <div key={t.counterparty_id} className="ct-rank">
                  <span className="ct-rank-name">{t.counterparty_name}</span>
                  <span className="ct-rank-bar"><span style={{ width: `${(t.outstanding / topArMax) * 100}%` }} /></span>
                  <span className="ct-rank-val num">{fmtWan(t.outstanding)}</span>
                </div>
              ))}</>}
            </div>
          </div>

          {/* 证件预警 */}
          <div className="panel">
            <div className="panel-head">
              <span style={{ display: "inline-flex", alignItems: "center", gap: 7 }}><IconShield size={15} />证件合规预警</span>
              <Link className="link small" to="/fleet">证件库 →</Link>
            </div>
            <div className="ct-side-body">
              {compliance.isLoading ? <StateView kind="loading" compact /> : compliance.isError ? (
                <StateView kind="error" hint="证件状态暂时无法同步。" compact onRetry={() => compliance.refetch()} />
              ) : credRows.length === 0 ? (
                <div className="muted small" style={{ padding: "6px 2px" }}>30 天内无临期/过期证件。</div>
              ) : (
                credRows.map((r, i) => (
                  <div key={i} className="ct-cred">
                    <span className={`tag tag-${SEV_TONE[r.severity]}`}>{r.days_left < 0 ? `逾期${-r.days_left}天` : `${r.days_left}天`}</span>
                    <span className="ct-cred-main">{r.subject} · {r.credential}</span>
                    <span className="muted small">{r.expiry}</span>
                  </div>
                ))
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
