import { useQuery } from "@tanstack/react-query";

import { apiGet } from "../api/client";

interface Funnel {
  by_status: Record<string, number>;
  by_channel: Record<string, number>;
  today_created: number;
  total: number;
}

// 订单流转四段：草稿 → 已建单(进运单池) → 已调派 → 剩余未调派，
// 让客服一眼看到自己经手订单卡在哪一段。
const STAGES: { key: string; label: string; sub: string; tone: string; pick: (f: Funnel) => number }[] = [
  { key: "draft", label: "订单草稿", sub: "待确认建单", tone: "", pick: (f) => (f.by_status.draft || 0) + (f.by_status.pending_confirm || 0) },
  { key: "pooled", label: "已建单", sub: "进入运单池", tone: "blue", pick: (f) => (f.by_status.confirmed || 0) + (f.by_status.pooled || 0) + (f.by_status.dispatching || 0) },
  { key: "dispatched", label: "已调派", sub: "已转运单派车", tone: "green", pick: (f) => (f.by_status.converted || 0) + (f.by_status.completed || 0) },
  { key: "waiting", label: "剩余未调派", sub: "池中待派", tone: "amber", pick: (f) => (f.by_status.pooled || 0) + (f.by_status.dispatching || 0) },
];

export function OrderLifecycle() {
  const funnel = useQuery({
    queryKey: ["orders", "funnel"],
    queryFn: () => apiGet<Funnel>("/orders/funnel"),
    refetchInterval: 30000,
  });
  const f = funnel.data ?? { by_status: {}, by_channel: {}, today_created: 0, total: 0 };

  return (
    <div className="panel">
      <div className="panel-head">
        <span>订单流转</span>
        <span className="ai-pill">今日新建 {f.today_created}</span>
      </div>
      <div className="lc4">
        {STAGES.map((s, i) => {
          const n = s.pick(f);
          return (
            <div className="lc4-item" key={s.key}>
              <div className={`lc4-card${s.tone ? ` lc4-${s.tone}` : ""}${n > 0 ? " on" : ""}`}>
                <div className="lc4-count">{n}</div>
                <div className="lc4-label">{s.label}</div>
                <div className="lc4-sub">{s.sub}</div>
              </div>
              {i < STAGES.length - 1 && <span className="lc4-arrow">→</span>}
            </div>
          );
        })}
      </div>
    </div>
  );
}
