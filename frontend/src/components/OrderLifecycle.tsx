import { useQuery } from "@tanstack/react-query";

import { apiGet } from "../api/client";

interface Funnel {
  by_status: Record<string, number>;
  by_channel: Record<string, number>;
  today_created: number;
  total: number;
}

// 订单来源渠道（从哪来）
const CHANNELS: [string, string][] = [
  ["cs", "客服代下"], ["self", "客户自助"], ["wechat_group", "微信群"],
  ["miniprogram", "小程序"], ["api", "开放 API"],
];

// 订单去向（到哪去）—— 生命周期各环节
const STAGES: { key: string; label: string; pick: (f: Funnel) => number }[] = [
  { key: "pending", label: "待确认", pick: (f) => (f.by_status.draft || 0) + (f.by_status.pending_confirm || 0) },
  { key: "confirmed", label: "已确认", pick: (f) => f.by_status.confirmed || 0 },
  { key: "pooled", label: "订单池", pick: (f) => f.by_status.pooled || 0 },
  { key: "dispatching", label: "调度中", pick: (f) => f.by_status.dispatching || 0 },
  { key: "converted", label: "已转运单", pick: (f) => f.by_status.converted || 0 },
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
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
          订单流转 · 从哪来 → 到哪去
        </span>
        <span className="ai-pill">今日新建 {f.today_created}</span>
      </div>
      <div className="lc-body">
        <div className="lc-source">
          <div className="lc-eyebrow">订单来源</div>
          {CHANNELS.map(([k, label]) => (
            <div className="lc-chan" key={k}>
              <span>{label}</span>
              <b className="mono">{f.by_channel[k] || 0}</b>
            </div>
          ))}
        </div>
        <div className="lc-arrow">→</div>
        <div className="lc-pipe">
          {STAGES.map((s, i) => {
            const n = s.pick(f);
            return (
              <div className="lc-stagewrap" key={s.key}>
                <div className={`lc-stage${n > 0 ? " on" : ""}`}>
                  <div className="lc-count">{n}</div>
                  <div className="lc-label">{s.label}</div>
                </div>
                {i < STAGES.length - 1 && <span className="lc-sep">→</span>}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
