import { useMutation } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet } from "../api/client";
import { fmtDateTime } from "../api/format";
import { BUSINESS_TYPE_LABEL, ORDER_STATUS_LABEL, STATUS_LABEL } from "../api/types";

interface TrackResult {
  order_no: string;
  status: string;
  business_type: string;
  origin: string;
  destination: string;
  created_at: string;
  milestones: Array<{ event: string; time: string }>;
  shipment: null | {
    waybill_no: string;
    status: string;
    estimated_arrival: string | null;
    receipt_status: string;
    position: null | { lat: number; lng: number; at: string };
  };
}

const MILESTONE_LABEL: Record<string, string> = {
  created: "已下单", confirmed: "已确认", pooled: "待调度", dispatched: "已派车", completed: "已送达",
};

export function TrackingPage() {
  const [orderNo, setOrderNo] = useState("");
  const [phone, setPhone] = useState("");

  const track = useMutation({
    mutationFn: () => apiGet<TrackResult>(`/track?order_no=${encodeURIComponent(orderNo)}&phone=${encodeURIComponent(phone)}`),
  });

  const r = track.data;

  return (
    <div className="public-page tracking-page">
      <div className="public-card tracking-card">
        <div className="login-brand">订单<span className="accent">跟踪</span></div>
        <div className="login-sub">输入订单号与下单手机号，查询物流进度</div>
        <form className="tracking-form" onSubmit={(e) => { e.preventDefault(); if (orderNo && phone && !track.isPending) track.mutate(); }}>
          <label className="field">订单号
            <input placeholder="如 DD20260617000001" value={orderNo} onChange={(e) => setOrderNo(e.target.value)} autoComplete="off" />
          </label>
          <label className="field">下单手机号
            <input placeholder="手机号或后四位" value={phone} onChange={(e) => setPhone(e.target.value)} inputMode="tel" autoComplete="tel" />
          </label>
          <button type="submit" className="btn-primary" disabled={!orderNo || !phone || track.isPending}>
            {track.isPending ? "查询中…" : "查询"}
          </button>
        </form>
        {track.isError && <div className="login-error">未找到匹配订单，请核对订单号与手机号。</div>}

        {r && (
          <div className="stack" style={{ marginTop: 8 }}>
            <div className="kv" style={{ padding: 0 }}>
              <div><span>订单号</span><b className="mono">{r.order_no}</b></div>
              <div><span>状态</span><b>{ORDER_STATUS_LABEL[r.status] ?? r.status}</b></div>
              <div><span>线路</span><b>{r.origin} → {r.destination}</b></div>
              <div><span>类型</span><b>{BUSINESS_TYPE_LABEL[r.business_type] ?? r.business_type}</b></div>
            </div>
            <ul className="timeline">
              {r.milestones.map((m, i) => (
                <li key={i}>
                  <span className="dot" />
                  <div>
                    <div className="tl-type">{MILESTONE_LABEL[m.event] ?? m.event}</div>
                    <div className="muted small">{fmtDateTime(m.time)}</div>
                  </div>
                </li>
              ))}
            </ul>
            {r.shipment && (
              <div className="kv" style={{ padding: 0 }}>
                <div><span>运单</span><b className="mono">{r.shipment.waybill_no}</b></div>
                <div><span>运输状态</span><b>{STATUS_LABEL[r.shipment.status] ?? r.shipment.status}</b></div>
                {r.shipment.estimated_arrival && <div><span>预计到达</span><b>{fmtDateTime(r.shipment.estimated_arrival)}</b></div>}
                {r.shipment.position && <div><span>当前位置</span><b className="mono small">{r.shipment.position.lat.toFixed(3)}, {r.shipment.position.lng.toFixed(3)}</b></div>}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
