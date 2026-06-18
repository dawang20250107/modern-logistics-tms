import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link, useParams } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import type { Order, OrderEvent } from "../api/types";
import {
  BUSINESS_TYPE_LABEL,
  ORDER_CHANNEL_LABEL,
  ORDER_EVENT_LABEL,
  ORDER_STATUS_LABEL,
  PRIORITY_LABEL,
  SLA_STATUS_LABEL,
} from "../api/types";

const SOURCE_TYPE_LABEL: Record<string, string> = { individual: "个人", enterprise: "企业", government: "政府" };

export function OrderDetailPage() {
  const { id = "" } = useParams();
  const queryClient = useQueryClient();

  const order = useQuery({ queryKey: ["order", id], queryFn: () => apiGet<Order>(`/orders/${id}`) });
  const timeline = useQuery({ queryKey: ["order", id, "timeline"], queryFn: () => apiGet<OrderEvent[]>(`/orders/${id}/timeline`) });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["order", id] });

  const act = useMutation({
    mutationFn: (action: string) => apiPost(`/orders/${id}/${action}`, {}),
    onSuccess: invalidate,
  });

  const o = order.data;
  const events = timeline.data ?? [];

  if (order.isLoading || !o) return <div className="muted" style={{ padding: 16 }}>加载中…</div>;

  const kv = (label: string, value: ReactNode) => (
    <div><span>{label}</span><b>{value || "-"}</b></div>
  );

  return (
    <div className="stack">
      <div className="panel">
        <div className="wb-head">
          <div>
            <div className="muted small">订单</div>
            <div className="wb-no mono">{o.order_no}</div>
            <div className="muted small">{ORDER_CHANNEL_LABEL[o.channel]} · {SOURCE_TYPE_LABEL[o.source_type] ?? o.source_type} · 建单 {o.created_by_name || "-"}</div>
          </div>
          <div className="wb-status">
            <span className="status-pill">{ORDER_STATUS_LABEL[o.status] ?? o.status}</span>
            {o.sla_status && o.sla_status !== "pending" && (
              <span className={`tag tag-sla_${o.sla_status}`}>{SLA_STATUS_LABEL[o.sla_status]}</span>
            )}
          </div>
        </div>
        <div className="wb-actions">
          {o.status === "pending_confirm" && <button className="btn-ghost" onClick={() => act.mutate("confirm")}>确认</button>}
          {(o.status === "confirmed" || o.status === "pending_confirm") && <button className="btn-ghost" onClick={() => act.mutate("pool")}>进池</button>}
          {o.status === "pooled" && <Link className="btn-primary" to="/dispatch-board" style={{ textDecoration: "none" }}>去调度台派单</Link>}
          {!["converted", "completed", "cancelled"].includes(o.status) && <button className="btn-ghost" onClick={() => act.mutate("cancel")}>取消</button>}
        </div>
      </div>

      <div className="wb-grid">
        <div className="panel">
          <div className="panel-head">订单信息</div>
          <div className="kv">
            {kv("客户", o.customer_name)}
            {kv("业务类型", BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type)}
            {kv("优先级", PRIORITY_LABEL[o.priority])}
            {kv("线路", `${o.origin} → ${o.destination}`)}
            {kv("货物", o.cargo_desc)}
            {kv("货量", `${o.cargo_weight_ton}吨 / ${o.cargo_quantity}件`)}
            {kv("货值", o.cargo_value !== "0.00" ? `¥${o.cargo_value}` : "")}
            {kv("危险品", o.is_hazardous ? "是" : "否")}
            {kv("温区", o.temperature_range)}
            {kv("发货联系", `${o.contact_name} ${o.contact_phone}`)}
            {kv("认领调度", o.claimed_by_name)}
          </div>
          {o.raw_text && (
            <div style={{ padding: "0 18px 16px" }}>
              <div className="muted small">原始消息</div>
              <div className="result-box" style={{ margin: "6px 0 0" }}>{o.raw_text}</div>
            </div>
          )}
        </div>

        <div className="panel">
          <div className="panel-head">全生命周期</div>
          {timeline.isLoading ? (
            <div className="muted" style={{ padding: 16 }}>加载中…</div>
          ) : (
            <ul className="timeline">
              {events.map((e) => (
                <li key={e.id}>
                  <span className="dot" />
                  <div>
                    <div className="tl-type">{ORDER_EVENT_LABEL[e.event_type] ?? e.event_type}</div>
                    <div className="muted small">
                      {new Date(e.event_time).toLocaleString()} · {e.actor_name || e.source}
                      {e.to_status && ` · → ${ORDER_STATUS_LABEL[e.to_status] ?? e.to_status}`}
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
