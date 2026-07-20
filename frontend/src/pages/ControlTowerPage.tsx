import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet } from "../api/client";
import type { ExpiringCredentials, Paginated, Waybill } from "../api/types";
import { useEventStream } from "../api/useEventStream";
import { hasPerm, useAuth } from "../auth/auth";
import { BusinessMetrics } from "../components/BusinessMetrics";

// 驾驶舱日期联动：驱动经营指标仪表（近 N 天）
const CT_PERIODS: { days: number; label: string }[] = [
  { days: 7, label: "近7天" },
  { days: 30, label: "近30天" },
  { days: 90, label: "近90天" },
  { days: 365, label: "近1年" },
];

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
  const { user } = useAuth();
  const [days, setDays] = useState(30);

  const waybills = useQuery({
    queryKey: ["waybills", "all"],
    queryFn: () => apiGet<Paginated<Waybill>>("/waybills?page_size=100"),
  });

  // 实时事件：到达即刷新看板
  useEventStream(() => queryClient.invalidateQueries({ queryKey: ["waybills"] }));

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
  const pendingReceipt = items.filter((w) => w.receipt_status === "pending");
  const inTransit = items.filter((w) => w.status === "in_transit");
  const w = wb.data;
  const todos = w ? [
    { label: "待确认订单", value: w.cs.my_orders_pending_confirm, to: "/intake", tone: "amber" },
    { label: "订单池待派", value: w.dispatch.pool_count, to: "/dispatch-board", tone: "blue" },
    { label: "我认领的", value: w.dispatch.my_claimed, to: "/dispatch-board", tone: "" },
    { label: "待对账", value: w.finance.draft_statements, to: "/reconciliation", tone: "" },
    { label: "我的异常", value: w.common.my_open_exceptions, to: "/dispatch-board", tone: "red" },
    { label: "证件预警", value: (compliance.data?.summary.expired ?? 0) + (compliance.data?.summary.critical ?? 0), to: "/fleet", tone: "red" },
  ].filter((t) => t.value > 0) : [];

  return (
    <div className="stack ctower">
      {todos.length > 0 && (
        <div className="panel">
          <div className="panel-head">我的待办</div>
          <div className="kpi-row" style={{ padding: 12, gridTemplateColumns: `repeat(${Math.min(todos.length, 6)}, 1fr)` }}>
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
        <Kpi label="待回单" value={pendingReceipt.length} tone="amber" />
      </div>

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
  );
}
