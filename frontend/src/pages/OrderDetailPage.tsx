import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import type { Order, OrderEvent } from "../api/types";
import {
  BUSINESS_TYPE_LABEL,
  ORDER_CHANNEL_LABEL,
  ORDER_EVENT_LABEL,
  ORDER_STATUS_LABEL,
  PRIORITY_LABEL,
  SETTLEMENT_LABEL,
  SLA_STATUS_LABEL,
  SOURCE_TYPE_LABEL,
} from "../api/types";

const fmtDt = (s: string | null) => (s ? new Date(s).toLocaleString() : "-");

export function OrderDetailPage() {
  const { id = "" } = useParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const [edit, setEdit] = useState<Record<string, string>>({});

  const order = useQuery({ queryKey: ["order", id], queryFn: () => apiGet<Order>(`/orders/${id}`) });
  const timeline = useQuery({ queryKey: ["order", id, "timeline"], queryFn: () => apiGet<OrderEvent[]>(`/orders/${id}/timeline`) });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["order", id] });

  const act = useMutation({
    mutationFn: (action: string) => apiPost(`/orders/${id}/${action}`, {}),
    onSuccess: invalidate,
  });
  const clone = useMutation({
    mutationFn: () => apiPost<Order>(`/orders/${id}/clone`, {}),
    onSuccess: (o) => { toast.success(`已复制为草稿：${o.order_no}`); navigate(`/orders/${o.id}`); },
  });
  const save = useMutation({
    mutationFn: () => apiPost(`/orders/${id}/edit`, { fields: edit }),
    onSuccess: () => { toast.success("已保存"); setEditing(false); invalidate(); },
  });

  const o = order.data;
  const events = timeline.data ?? [];

  if (order.isLoading || !o) return <div className="muted" style={{ padding: 16 }}>加载中…</div>;

  const startEdit = () => {
    setEdit({
      priority: o.priority, settlement_type: o.settlement_type,
      quoted_amount: o.quoted_amount, cargo_value: o.cargo_value, remark: o.remark || "",
    });
    setEditing(true);
  };
  const editable = !["converted", "completed", "cancelled"].includes(o.status);

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
          {(o.status === "draft" || o.status === "pending_confirm") && <button className="btn-ghost" onClick={() => act.mutate("confirm")}>确认</button>}
          {(o.status === "confirmed" || o.status === "pending_confirm") && <button className="btn-ghost" onClick={() => act.mutate("pool")}>进池</button>}
          {o.status === "pooled" && <Link className="btn-primary" to="/dispatch-board" style={{ textDecoration: "none" }}>去调度台派单</Link>}
          {editable && !editing && <button className="btn-ghost" onClick={startEdit}>编辑</button>}
          {editing && <button className="btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>保存</button>}
          {editing && <button className="btn-ghost" onClick={() => setEditing(false)}>取消编辑</button>}
          <button className="btn-ghost" onClick={() => clone.mutate()}>复制建单</button>
          {(o.waybill_nos ?? []).map((no) => (
            <Link key={no} className="btn-ghost mono" to={`/waybills/${no}`} style={{ textDecoration: "none" }}>运单 {no} →</Link>
          ))}
          {editable && !editing && (
            <button
              className="btn-ghost"
              onClick={async () => {
                if (await confirmAction({ message: `确定取消订单 ${o.order_no}？取消后不可恢复。`, tone: "danger", confirmText: "取消订单" })) {
                  act.mutate("cancel");
                }
              }}
            >取消订单</button>
          )}
        </div>
      </div>

      <div className="wb-grid">
        <div className="stack">
          {/* 商务信息 */}
          <div className="panel">
            <div className="panel-head">商务信息</div>
            <div className="kv">
              {kv("客户", o.customer_name)}
              {kv("客户类型", SOURCE_TYPE_LABEL[o.source_type] ?? o.source_type)}
              {kv("业务类型", BUSINESS_TYPE_LABEL[o.business_type] ?? o.business_type)}
              {kv("优先级", editing
                ? <select value={edit.priority} onChange={(e) => setEdit({ ...edit, priority: e.target.value })}>{Object.entries(PRIORITY_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select>
                : PRIORITY_LABEL[o.priority])}
              {kv("结算方式", editing
                ? <select value={edit.settlement_type} onChange={(e) => setEdit({ ...edit, settlement_type: e.target.value })}>{Object.entries(SETTLEMENT_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select>
                : SETTLEMENT_LABEL[o.settlement_type] ?? o.settlement_type)}
              {kv("报价", editing
                ? <input className="search" style={{ width: 120 }} value={edit.quoted_amount} onChange={(e) => setEdit({ ...edit, quoted_amount: e.target.value })} />
                : (Number(o.quoted_amount) > 0 ? fmtMoney(o.quoted_amount) : "-"))}
              {kv("货值", editing
                ? <input className="search" style={{ width: 120 }} value={edit.cargo_value} onChange={(e) => setEdit({ ...edit, cargo_value: e.target.value })} />
                : (Number(o.cargo_value) > 0 ? fmtMoney(o.cargo_value) : "-"))}
              {kv("认领调度", o.claimed_by_name)}
            </div>
          </div>

          {/* 装卸站点 */}
          <div className="panel">
            <div className="panel-head">装卸站点</div>
            {o.stops.length > 0 ? (
              <ul className="timeline" style={{ padding: 16 }}>
                {o.stops.map((s) => (
                  <li key={s.id}>
                    <span className="dot" />
                    <div>
                      <div className="tl-type">{s.stop_type === "pickup" ? "提货" : "送货"} · {s.city} {s.address}</div>
                      <div className="muted small">
                        {[s.contact_name, s.contact_phone].filter(Boolean).join(" ")}
                        {s.expected_start && ` · ${fmtDt(s.expected_start)}`}
                        {s.cargo_note && ` · ${s.cargo_note}`}
                      </div>
                    </div>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="kv">
                {kv("线路", `${o.origin} → ${o.destination}`)}
                {kv("提货地址", o.pickup_address)}
                {kv("送货地址", o.delivery_address)}
                {kv("发货联系", `${o.contact_name} ${o.contact_phone}`)}
              </div>
            )}
          </div>

          {/* 货物明细 */}
          <div className="panel">
            <div className="panel-head">货物明细 · 合计 {o.cargo_quantity}件 / {o.cargo_weight_ton}吨</div>
            {o.cargo_items.length > 0 ? (
              <table className="table">
                <thead><tr><th>品名</th><th>件数</th><th>吨</th><th>方</th><th>包装</th><th>温区</th></tr></thead>
                <tbody>
                  {o.cargo_items.map((c) => (
                    <tr key={c.id}>
                      <td>{c.name}</td><td>{c.quantity}</td><td>{c.weight_ton}</td><td>{c.volume_cbm}</td>
                      <td>{c.package_type || "-"}</td><td>{c.temperature_range || "-"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div className="kv">
                {kv("货物", o.cargo_desc)}
                {kv("货量", `${o.cargo_weight_ton}吨 / ${o.cargo_quantity}件 / ${o.cargo_volume_cbm}方`)}
                {kv("包装", o.package_type)}
                {kv("危险品", o.is_hazardous ? "是 ⚠" : "否")}
                {kv("温区", o.temperature_range)}
              </div>
            )}
          </div>

          {/* 时效 */}
          <div className="panel">
            <div className="panel-head">时效要求</div>
            <div className="kv">
              {kv("要求提货", fmtDt(o.expected_pickup_at))}
              {kv("要求送达", fmtDt(o.expected_delivery_at))}
              {kv("SLA", SLA_STATUS_LABEL[o.sla_status] ?? o.sla_status)}
              {kv("送达时间", fmtDt(o.delivered_at))}
            </div>
            <div style={{ padding: "0 18px 16px" }}>
              <div className="muted small">备注</div>
              {editing
                ? <textarea className="search" style={{ width: "100%", minHeight: 56 }} value={edit.remark} onChange={(e) => setEdit({ ...edit, remark: e.target.value })} />
                : <div>{o.remark || <span className="muted">-</span>}</div>}
            </div>
            {o.raw_text && (
              <div style={{ padding: "0 18px 16px" }}>
                <div className="muted small">原始消息</div>
                <div className="result-box" style={{ margin: "6px 0 0" }}>{o.raw_text}</div>
              </div>
            )}
          </div>
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
