import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { apiDelete, apiGet, apiPost, apiUpload } from "../api/client";
import { confirmAction } from "../api/confirm";
import { fmtDateTime, fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import { DocumentLineage } from "../components/DocumentLineage";
import { CopyCode } from "../components/CopyCode";
import { StateView } from "../components/StateView";
import { StatusTag } from "../components/StatusTag";
import type { Order, OrderEvent, OrderWorkflow } from "../api/types";
import {
  APPROVAL_STATUS_LABEL,
  ATTACHMENT_KIND_LABEL,
  BUSINESS_TYPE_LABEL,
  ORDER_CHANNEL_LABEL,
  ORDER_EVENT_LABEL,
  ORDER_STATUS_LABEL,
  PRIORITY_LABEL,
  SETTLEMENT_LABEL,
  SLA_STATUS_LABEL,
  SOURCE_TYPE_LABEL,
} from "../api/types";

const fmtDt = (s: string | null) => fmtDateTime(s);

export function OrderDetailPage() {
  const { id = "" } = useParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const [edit, setEdit] = useState<Record<string, string>>({});

  const order = useQuery({ queryKey: ["order", id], queryFn: () => apiGet<Order>(`/orders/${id}`) });
  const workflow = useQuery({ queryKey: ["order", id, "workflow"], queryFn: () => apiGet<OrderWorkflow>(`/orders/${id}/workflow`) });
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
  const approval = useMutation({
    mutationFn: (v: { action: "approve" | "reject"; remark: string }) => apiPost(`/orders/${id}/${v.action}`, { remark: v.remark }),
    onSuccess: (_d, v) => { toast.success(v.action === "approve" ? "已审批通过" : "已驳回"); invalidate(); },
  });
  const [attKind, setAttKind] = useState("contract");
  const [splitMode, setSplitMode] = useState(false);
  const [groupOf, setGroupOf] = useState<Record<string, number>>({});
  const split = useMutation({
    mutationFn: () => {
      const groups: Record<number, string[]> = {};
      (order.data?.cargo_items ?? []).forEach((c) => {
        const g = groupOf[c.id ?? ""] ?? 1;
        (groups[g] ??= []).push(c.id ?? "");
      });
      const payload = Object.values(groups).filter((ids) => ids.length).map((ids) => ({ cargo_item_ids: ids }));
      return apiPost(`/orders/${id}/split`, { groups: payload });
    },
    onSuccess: () => { toast.success("已拆单，原单作废，子订单已生成"); setSplitMode(false); navigate("/intake"); },
  });
  const upload = useMutation({
    mutationFn: (file: File) => {
      const fd = new FormData();
      fd.append("file", file);
      fd.append("kind", attKind);
      return apiUpload(`/orders/${id}/attachments`, fd);
    },
    onSuccess: () => { toast.success("附件已上传"); invalidate(); },
  });
  const delAtt = useMutation({
    mutationFn: (attId: string) => apiDelete(`/orders/${id}/attachments/${attId}`),
    onSuccess: () => { toast.success("已删除附件"); invalidate(); },
  });

  const o = order.data;
  const events = timeline.data ?? [];

  if (order.isLoading || !o) return <StateView kind="loading" />;

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
      {workflow.data && (
        <div className="panel">
          <div className="panel-head">工作流全流程</div>
          <div className="wf-track">
            {workflow.data.stages.map((s) => (
              <div key={s.key} className={`wf-step${s.done ? " done" : ""}${s.key === workflow.data!.current ? " current" : ""}`}>
                <span className="wf-dot">{s.done ? "✓" : ""}</span>
                <span className="wf-name">{s.name}</span>
                {s.detail && <span className="wf-detail">{s.detail}</span>}
              </div>
            ))}
          </div>
        </div>
      )}
      <div className="panel">
        <div className="wb-head">
          <div>
            <div className="muted small">订单</div>
            <div className="wb-no mono"><CopyCode value={o.order_no} /></div>
            <div className="muted small">{ORDER_CHANNEL_LABEL[o.channel]} · {SOURCE_TYPE_LABEL[o.source_type] ?? o.source_type} · 建单 {o.created_by_name || "-"}</div>
          </div>
          <div className="wb-status">
            <StatusTag kind="order" value={o.status} />
            {o.sla_status && o.sla_status !== "pending" && (
              <StatusTag kind="sla" value={o.sla_status} />
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

      <DocumentLineage orderId={id} />

      {o.approval_status !== "none" && (
        <div className="panel" style={{ borderLeft: `4px solid ${o.approval_status === "rejected" ? "var(--red)" : o.approval_status === "approved" ? "var(--green)" : "var(--amber)"}` }}>
          <div className="form-actions" style={{ borderBottom: "none" }}>
            <span className={`tag tag-${o.approval_status === "approved" ? "low" : o.approval_status === "rejected" ? "high" : "medium"}`}>
              审批：{APPROVAL_STATUS_LABEL[o.approval_status]}
            </span>
            <span className="muted small">高价值订单需主管审批后方可进池派单</span>
            {o.approval_remark && <span className="muted small">· {o.approval_remark}</span>}
            <span style={{ flex: 1 }} />
            {o.approval_status === "pending" && (
              <>
                <button className="btn-primary" disabled={approval.isPending} onClick={() => approval.mutate({ action: "approve", remark: "" })}>审批通过</button>
                <button className="btn-danger" disabled={approval.isPending} onClick={async () => {
                  const ok = await confirmAction({ message: `确定驳回订单 ${o.order_no}？`, tone: "danger", confirmText: "驳回" });
                  if (ok) approval.mutate({ action: "reject", remark: "" });
                }}>驳回</button>
              </>
            )}
          </div>
        </div>
      )}

      <div className="wb-grid">
        <div className="stack">
          {/* 商务信息 */}
          <div className="panel">
            <div className="panel-head">商务信息</div>
            <div className="kv">
              {kv("客户", o.customer_name)}
              {kv("客户分类", SOURCE_TYPE_LABEL[o.source_type] ?? o.source_type)}
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
                {kv("提货地址", o.pickup_address ? <CopyCode value={o.pickup_address} /> : "—")}
                {kv("送货地址", o.delivery_address ? <CopyCode value={o.delivery_address} /> : "—")}
                {kv("发货联系", <span>{o.contact_name} {o.contact_phone ? <CopyCode value={o.contact_phone} /> : ""}</span>)}
              </div>
            )}
          </div>

          {/* 货物明细 */}
          <div className="panel">
            <div className="panel-head">
              货物明细 · 合计 {o.cargo_quantity}件 / {o.cargo_weight_ton}吨
              {editable && o.cargo_items.length >= 2 && !splitMode && (
                <button className="btn-ghost" onClick={() => { setSplitMode(true); setGroupOf(Object.fromEntries(o.cargo_items.map((c) => [c.id ?? "", 1]))); }}>拆单</button>
              )}
            </div>
            {o.cargo_items.length > 0 ? (
              <div className="table-wrap">
              <table className="table">
                <thead><tr><th>品名</th><th>件数</th><th>吨</th><th>方</th><th>包装</th><th>温区</th>{splitMode && <th>拆分组</th>}</tr></thead>
                <tbody>
                  {o.cargo_items.map((c) => (
                    <tr key={c.id}>
                      <td>{c.name}</td><td>{c.quantity}</td><td>{c.weight_ton}</td><td>{c.volume_cbm}</td>
                      <td>{c.package_type || "-"}</td><td>{c.temperature_range || "-"}</td>
                      {splitMode && (
                        <td>
                          <select value={groupOf[c.id ?? ""] ?? 1} onChange={(e) => setGroupOf((m) => ({ ...m, [c.id ?? ""]: Number(e.target.value) }))}>
                            {[1, 2, 3, 4].map((g) => <option key={g} value={g}>第 {g} 单</option>)}
                          </select>
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
              </div>
            ) : (
              <div className="kv">
                {kv("货物", o.cargo_desc)}
                {kv("货量", `${o.cargo_weight_ton}吨 / ${o.cargo_quantity}件 / ${o.cargo_volume_cbm}方`)}
                {kv("包装", o.package_type)}
                {kv("危险品", o.is_hazardous ? "是" : "否")}
                {kv("温区", o.temperature_range)}
              </div>
            )}
            {splitMode && (() => {
              const groupCount = new Set(o.cargo_items.map((c) => groupOf[c.id ?? ""] ?? 1)).size;
              return (
                <div className="form-actions">
                  <span className="muted small">将按所选拆成 {groupCount} 张子订单（原单作废）</span>
                  <span style={{ flex: 1 }} />
                  <button className="btn-primary" disabled={groupCount < 2 || split.isPending} onClick={() => split.mutate()}>确认拆单</button>
                  <button className="btn-ghost" onClick={() => setSplitMode(false)}>取消</button>
                </div>
              );
            })()}
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
            {o.ai_conversation_id && (
              <div style={{ padding: "0 18px 12px" }}>
                <div className="muted small">AI会话ID</div>
                <div className="mono small">{o.ai_conversation_id}</div>
              </div>
            )}
            {o.raw_text && (
              <div style={{ padding: "0 18px 16px" }}>
                <div className="muted small">原始消息</div>
                <div className="result-box" style={{ margin: "6px 0 0" }}>{o.raw_text}</div>
              </div>
            )}
          </div>

          {/* 附件 */}
          <div className="panel">
            <div className="panel-head">附件（合同 / 委托书 / 货物照片）</div>
            <div className="form-actions" style={{ borderBottom: "1px solid var(--line)" }}>
              <select value={attKind} onChange={(e) => setAttKind(e.target.value)}>
                {Object.entries(ATTACHMENT_KIND_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
              <label className="btn-ghost" style={{ cursor: "pointer" }}>
                {upload.isPending ? "上传中…" : "选择文件上传"}
                <input type="file" hidden disabled={upload.isPending} onChange={(e) => { const f = e.target.files?.[0]; if (f) upload.mutate(f); e.target.value = ""; }} />
              </label>
            </div>
            {o.attachments.length === 0 ? (
              <StateView kind="empty" title="暂无附件" hint="上传合同 / 磅单 / 回单等文件后在此查看。" compact />
            ) : (
              <div className="table-wrap">
              <table className="table">
                <thead><tr><th>类型</th><th>名称</th><th>上传人</th><th>时间</th><th>操作</th></tr></thead>
                <tbody>
                  {o.attachments.map((a) => (
                    <tr key={a.id}>
                      <td><span className="tag tag-info">{ATTACHMENT_KIND_LABEL[a.kind] ?? a.kind}</span></td>
                      <td>{a.file_display ? <a className="link" href={a.file_display} target="_blank" rel="noreferrer">{a.name || "查看"}</a> : (a.name || "-")}</td>
                      <td className="small">{a.uploaded_by_name || "-"}</td>
                      <td className="small">{fmtDateTime(a.created_at)}</td>
                      <td><button className="btn-ghost" disabled={delAtt.isPending} onClick={() => delAtt.mutate(a.id)}>删除</button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
              </div>
            )}
          </div>
        </div>

        <div className="panel">
          <div className="panel-head">全生命周期</div>
          {timeline.isLoading ? (
            <StateView kind="loading" compact />
          ) : (
            <ul className="timeline">
              {events.map((e) => (
                <li key={e.id}>
                  <span className="dot" />
                  <div>
                    <div className="tl-type">{ORDER_EVENT_LABEL[e.event_type] ?? e.event_type}</div>
                    <div className="muted small">
                      {fmtDateTime(e.event_time)} · {e.actor_name || e.source}
                      {e.to_status && ` · → ${ORDER_STATUS_LABEL[e.to_status] ?? e.to_status}`}
                    </div>
                    {(() => {
                      const changes = (e.payload?.changes ?? []) as { field: string; label: string; from: unknown; to: unknown }[];
                      const cols = (e.payload?.changed_collections ?? []) as string[];
                      if (changes.length === 0 && cols.length === 0) return null;
                      return (
                        <div className="small" style={{ marginTop: 4 }}>
                          {changes.map((c) => (
                            <div key={c.field}>
                              <b>{c.label}</b>：<span className="muted">{String(c.from ?? "—")}</span> → {String(c.to ?? "—")}
                            </div>
                          ))}
                          {cols.length > 0 && <div className="muted">更新了 {cols.join("、")}</div>}
                        </div>
                      );
                    })()}
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
