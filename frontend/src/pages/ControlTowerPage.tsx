import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import type { ExpiringCredentials, Paginated, QueryWaybillResult, Waybill } from "../api/types";
import { useEventStream } from "../api/useEventStream";
import { IconSparkles, IconTerminal, IconSearch } from "../components/Icons";

const RISK_LABEL: Record<string, string> = { high: "高", medium: "中", low: "低", none: "无" };

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
          <div className="panel-head">运输态势</div>
          <div className="situation">
            {items.length === 0 ? (
              <div className="muted small" style={{ padding: 16 }}>暂无运单</div>
            ) : (
              items.map((w) => (
                <Link key={w.id} to={`/waybills/${w.waybill_no}`} className={`route-chip rc-${w.risk_level}`}>
                  <span className="rc-no mono">{w.waybill_no}</span>
                  <span className="rc-route">{w.origin} → {w.destination}</span>
                </Link>
              ))
            )}
          </div>
        </div>

        <div className="panel">
          <div className="panel-head">实时事件流 (SSE)</div>
          {events.length === 0 ? (
            <div className="muted small" style={{ padding: 16 }}>已连接，等待事件…</div>
          ) : (
            <ul className="event-feed">
              {events.map((e, i) => (
                <li key={`${e.t}-${i}`}>
                  <span className={`evt evt-${e.type}`}>{e.type}</span>
                  <span className="mono small">{JSON.stringify(e.data)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      {/* AI 全局指令舱入口 */}
      <div 
        className="panel" 
        style={{ 
          background: "linear-gradient(135deg, rgba(37,99,235,0.03) 0%, rgba(37,99,235,0.08) 100%)", 
          border: "1px solid rgba(37,99,235,0.15)",
          cursor: "pointer",
          transition: "all 0.2s"
        }}
        onClick={() => {
          const e = new KeyboardEvent("keydown", { ctrlKey: true, key: "k" });
          window.dispatchEvent(e);
        }}
        onMouseEnter={(e) => e.currentTarget.style.background = "rgba(37,99,235,0.06)"}
        onMouseLeave={(e) => e.currentTarget.style.background = "linear-gradient(135deg, rgba(37,99,235,0.03) 0%, rgba(37,99,235,0.08) 100%)"}
      >
        <div style={{ padding: "16px 20px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <div style={{ fontSize: 14, fontWeight: "600", color: "var(--brand)", display: "flex", alignItems: "center", gap: 8 }}>
              <IconSparkles size={20} className="icon-offset" /> 智能协同调度引擎已就绪
            </div>
            <div className="muted small" style={{ color: "var(--ink-2)" }}>
              你可以随时使用自然语言唤起调度指令。例如：“帮我分析无锡方向拼单”、“查询苏B异常”、“测算单票利润”。
            </div>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <span className="muted small" style={{ display: "flex", alignItems: "center", gap: 6 }}><IconTerminal size={14} className="icon-offset" /> 快捷指令唤起</span>
            <span style={{ background: "var(--panel)", color: "var(--ink)", padding: "4px 8px", borderRadius: 4, fontWeight: "500", fontFamily: "var(--font-mono)", fontSize: 12, border: "1px solid var(--line)" }}>
              Ctrl K
            </span>
          </div>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">时空预警与异常队列</div>
        {waybills.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>指标数据拉取中…</div>
        ) : risky.length === 0 ? (
          <div className="muted" style={{ padding: 16 }}>全网车辆运转正常，暂无活跃的异常风险。</div>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>系统运单号</th>
                <th>共线线路</th>
                <th>风险等级</th>
                <th>ETA 偏移时效(分)</th>
                <th>回单回收状态</th>
              </tr>
            </thead>
            <tbody>
              {risky.map((w) => (
                <tr key={w.id}>
                  <td>
                    <Link className="link mono interactive-text" title="点击查看数字孪生档案" to={`/waybills/${w.waybill_no}`}>{w.waybill_no}</Link>
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
