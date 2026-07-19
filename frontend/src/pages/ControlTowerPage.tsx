import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import type { ExpiringCredentials, Paginated, QueryWaybillResult, Waybill } from "../api/types";
import { useEventStream } from "../api/useEventStream";
import { IconSparkles, IconTerminal, IconSearch } from "../components/Icons";
import { StateView } from "../components/StateView";

const RISK_LABEL: Record<string, string> = { high: "高", medium: "中", low: "低", none: "无" };

const EVT_LABEL: Record<string, string> = {
  risk: "风险", alert: "报警", order_pooled: "进池", order_claimed: "认领",
  order_dispatched: "派单", waybill_status: "运单", waybill_split: "拆单", waybill_merge: "合单",
  receipt_ocr: "回单", agent_suggestions: "智能建议", notification: "通知", order_sla: "时效",
};
function evtText(e: { data?: Record<string, unknown> }): string {
  const d = (e.data ?? {}) as Record<string, string | number>;
  if (d.waybill_no) return `运单 ${d.waybill_no}${d.risk_level ? " 风险变化" : ""}`;
  if (d.order_no) return `订单 ${d.order_no}`;
  return "有新动态";
}

function Kpi({ label, value, tone }: { label: string; value: number; tone?: string }) {
  return (
    <div className={`kpi${tone ? ` kpi-${tone}` : ""}`}>
      <div className="kpi-value">{value}</div>
      <div className="kpi-label">{label}</div>
    </div>
  );
}

export function ControlTowerPage() {
  const queryClient = useQueryClient();
  const [question, setQuestion] = useState("");
  const [answer, setAnswer] = useState("");

  const waybills = useQuery({
    queryKey: ["waybills", "all"],
    queryFn: () => apiGet<Paginated<Waybill>>("/waybills?page_size=100"),
  });

  // 实时事件：到达即刷新看板
  const events = useEventStream(() => queryClient.invalidateQueries({ queryKey: ["waybills"] }));

  const ask = useMutation({
    mutationFn: (q: string) => apiPost<QueryWaybillResult>("/ai/query-waybill", { query: q }),
    onSuccess: (data) => setAnswer(data.answer),
  });

  const wb = useQuery({
    queryKey: ["workbench"],
    queryFn: () => apiGet<{
      common: { unread_notifications: number; my_open_exceptions: number };
      cs: { my_orders_pending_confirm: number; my_orders_today: number };
      dispatch: { pool_count: number; my_claimed: number };
      finance: { draft_statements: number };
    }>("/workbench"),
    refetchInterval: 30000,
  });

  const compliance = useQuery({
    queryKey: ["compliance-mini"],
    queryFn: () => apiGet<ExpiringCredentials>("/credentials/expiring?days=30"),
    refetchInterval: 60000,
  });

  const items = waybills.data?.items ?? [];
  const risky = items.filter((w) => w.risk_level === "high" || w.risk_level === "medium");
  const pendingReceipt = items.filter((w) => w.receipt_status === "pending");
  const inTransit = items.filter((w) => w.status === "in_transit");
  const w = wb.data;
  const todos = w ? [
    { label: "待确认订单", value: w.cs.my_orders_pending_confirm, to: "/intake", tone: "amber" },
    { label: "订单池待派", value: w.dispatch.pool_count, to: "/dispatch-board", tone: "blue" },
    { label: "我认领的", value: w.dispatch.my_claimed, to: "/dispatch-board", tone: "" },
    { label: "待对账", value: w.finance.draft_statements, to: "/reconciliation", tone: "" },
    { label: "我的异常", value: w.common.my_open_exceptions, to: "/exceptions", tone: "red" },
    { label: "证件预警", value: (compliance.data?.summary.expired ?? 0) + (compliance.data?.summary.critical ?? 0), to: "/fleet", tone: "red" },
  ].filter((t) => t.value > 0) : [];

  return (
    <div className="stack">
      {todos.length > 0 && (
        <div className="panel">
          <div className="panel-head">我的待办</div>
          <div className="kpi-row" style={{ padding: 16, gridTemplateColumns: `repeat(${Math.min(todos.length, 6)}, 1fr)` }}>
            {todos.map((t) => (
              <Link key={t.label} to={t.to} className={`kpi${t.tone ? ` kpi-${t.tone}` : ""}`} style={{ textDecoration: "none", color: "inherit" }}>
                <div className="kpi-value">{t.value}</div>
                <div className="kpi-label">{t.label} →</div>
              </Link>
            ))}
          </div>
        </div>
      )}
      <div className="kpi-row">
        <Kpi label="运单总数" value={items.length} />
        <Kpi label="运输中" value={inTransit.length} tone="blue" />
        <Kpi label="风险运单" value={risky.length} tone="red" />
        <Kpi label="待回单" value={pendingReceipt.length} tone="amber" />
      </div>

      <div className="ct-grid">
        <div className="panel">
          <div className="panel-head">
            在途运单
            <Link to="/monitor" className="link small">在途监控 →</Link>
          </div>
          {inTransit.length === 0 ? (
            <StateView kind="empty" title="暂无在途运单" />
          ) : (
            <table className="table">
              <thead><tr><th>运单号</th><th>线路</th><th>风险</th><th className="num">ETA 偏移</th></tr></thead>
              <tbody>
                {inTransit.slice(0, 10).map((wb2) => (
                  <tr key={wb2.id}>
                    <td><Link className="link mono" to={`/waybills/${wb2.waybill_no}`}>{wb2.waybill_no}</Link></td>
                    <td>{wb2.origin} → {wb2.destination}</td>
                    <td><span className={`tag tag-${wb2.risk_level}`}>{RISK_LABEL[wb2.risk_level]}</span></td>
                    <td className="num" style={{ color: wb2.eta_drift_minutes > 0 ? "var(--red)" : "var(--muted)" }}>
                      {wb2.eta_drift_minutes > 0 ? `+${wb2.eta_drift_minutes}` : wb2.eta_drift_minutes} 分
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <div className="panel">
          <div className="panel-head">最近动态</div>
          {events.length === 0 ? (
            <div className="muted small" style={{ padding: 16 }}>已连接，暂无新动态。</div>
          ) : (
            <ul className="event-feed">
              {events.slice(0, 12).map((e, i) => (
                <li key={`${e.t}-${i}`}>
                  <span className={`evt evt-${e.type}`}>{EVT_LABEL[e.type] ?? "动态"}</span>
                  <span className="small">{evtText(e)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      
      <button
        type="button"
        className="panel"
        aria-label="打开 AI 助手命令面板（快捷键 Ctrl K）"
        style={{
          display: "block", width: "100%", textAlign: "left", font: "inherit", color: "inherit",
          background: "var(--accent-weak)",
          border: "1px solid var(--accent-weak-2)",
          cursor: "pointer",
          transition: "all 0.2s"
        }}
        onClick={() => {
          const e = new KeyboardEvent("keydown", { ctrlKey: true, key: "k" });
          window.dispatchEvent(e);
        }}
        onMouseEnter={(e) => e.currentTarget.style.background = "var(--accent-weak-2)"}
        onMouseLeave={(e) => e.currentTarget.style.background = "var(--accent-weak)"}
      >
        <div style={{ padding: "16px 20px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <div style={{ fontSize: 14, fontWeight: "600", color: "var(--brand)", display: "flex", alignItems: "center", gap: 8 }}>
              <IconSparkles size={20} className="icon-offset" /> AI 助手
            </div>
            <div className="muted small" style={{ color: "var(--ink-2)" }}>
              使用自然语言查询运单、拼单或利润测算。
            </div>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <span className="muted small" style={{ display: "flex", alignItems: "center", gap: 6 }}><IconTerminal size={14} className="icon-offset" /> 快捷键</span>
            <span style={{ background: "var(--panel)", color: "var(--ink)", padding: "4px 8px", borderRadius: 4, fontWeight: "500", fontFamily: "var(--font-mono)", fontSize: 12, border: "1px solid var(--line)" }}>
              Ctrl K
            </span>
          </div>
        </div>
      </button>

      <div className="panel">
        <div className="panel-head">风险与异常</div>
        {waybills.isLoading ? (
          <StateView kind="loading" compact />
        ) : risky.length === 0 ? (
          <StateView kind="empty" title="暂无风险运单" hint="有高/中风险运单时会在这里预警。" />
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>运单号</th>
                <th>线路</th>
                <th>风险等级</th>
                <th>ETA 偏移(分钟)</th>
                <th>回单状态</th>
              </tr>
            </thead>
            <tbody>
              {risky.map((w) => (
                <tr key={w.id}>
                  <td>
                    <Link className="link mono interactive-text" to={`/waybills/${w.waybill_no}`}>{w.waybill_no}</Link>
                  </td>
                  <td><span title={`起讫：${w.origin} → ${w.destination}`}>{w.route_name}</span></td>
                  <td>
                    <span className={`tag tag-${w.risk_level}`}>{RISK_LABEL[w.risk_level]}</span>
                  </td>
                  <td className="mono" style={{ color: "var(--red)" }}>+{w.eta_drift_minutes} 分钟</td>
                  <td>{w.receipt_status === "returned" ? "已回收" : w.receipt_status === "pending" ? "待回收" : w.receipt_status}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
