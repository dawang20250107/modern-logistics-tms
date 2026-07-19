import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, Fragment } from "react";

import { apiGet, apiPost } from "../api/client";
import { fmtDateTime, fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import type { ExceptionEvent, ExceptionRecord, Paginated } from "../api/types";
import { EXC_EVENT_LABEL } from "../api/types";
import { useAuth } from "../auth/auth";
import { StateView } from "./StateView";

const LEVEL_LABEL: Record<string, string> = { low: "低风险", medium: "中风险", high: "高风险" };
const STATUS_LABEL: Record<string, string> = {
  pending_handle: "待处理", handling: "处理中", pending_audit: "待审核", closed: "已关闭", rejected: "已驳回",
};
const EXC_TYPE_LABEL: Record<string, string> = {
  transit_delay: "在途超时", route_deviation: "偏航/路线异常", cargo_damage: "货损货差",
  vehicle_breakdown: "车辆故障", detained: "扣车扣货", customer_complaint: "客户投诉",
  temperature: "冷链温度异常", fuel: "油耗/漏油异常", overspeed: "超速驾驶",
  receipt_pending: "回单待回收", receipt_exception: "回单异常", other: "其他",
};
const SOURCE_LABEL: Record<string, string> = {
  track: "车联网设备", manual: "人工提报", system: "系统规则", ai: "AI 识别",
};

// 异常处置队列（原「异常处置」页的核心）：并入调度工作台，做认领·AI诊断·强制闭环。
export function ExceptionQueue() {
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const [expandedId, setExpandedId] = useState("");
  const [resolvingId, setResolvingId] = useState("");

  const list = useQuery({
    queryKey: ["exceptions"],
    queryFn: () => apiGet<Paginated<ExceptionRecord>>("/exceptions?page_size=100"),
  });
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["exceptions"] });
    queryClient.invalidateQueries({ queryKey: ["exception-timeline"] });
  };

  const act = useMutation({
    mutationFn: (v: { id: string; action: string; body?: Record<string, unknown> }) =>
      apiPost(`/exceptions/${v.id}/${v.action}`, v.body ?? {}),
    onSuccess: (_d, v) => {
      toast.success(v.action === "close" ? "异常处理已闭环" : "已更新处理状态");
      invalidate();
    },
  });
  const aiResolve = useMutation({
    mutationFn: (id: string) => { setResolvingId(id); return apiPost<{ ai_resolution: string }>(`/exceptions/${id}/ai-resolve`, {}); },
    onSuccess: (_d, id) => { toast.success("诊断已完成"); setResolvingId(""); setExpandedId(id); invalidate(); },
    onError: () => { toast.error("AI 服务调用失败"); setResolvingId(""); },
  });

  const timeline = useQuery({
    queryKey: ["exception-timeline", expandedId],
    queryFn: () => apiGet<ExceptionEvent[]>(`/exceptions/${expandedId}/timeline`),
    enabled: Boolean(expandedId),
  });

  const items = list.data?.items ?? [];
  const open = items.filter((i) => i.status !== "closed");

  return (
    <div className="panel">
      <div className="panel-head">
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          异常处置
          <span className="tag tag-medium" style={{ fontSize: 11, fontWeight: "bold" }}>{open.length} 待办</span>
        </div>
      </div>
      {list.isLoading ? (
        <StateView kind="loading" compact />
      ) : items.length === 0 ? (
        <StateView kind="empty" scene="exception-empty" />
      ) : (
        <div className="table-wrap">
          <table className="table" style={{ width: "100%" }}>
            <thead>
              <tr>
                <th style={{ width: 40 }}></th>
                <th>异常类型</th><th>风险等级</th><th>触发来源</th><th>运单号</th>
                <th>跟进人</th><th>理赔金额</th><th>处置状态</th>
                <th style={{ textAlign: "right", paddingRight: 20 }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {items.map((e) => {
                const isExpanded = expandedId === e.id;
                const isHandling = e.status === "handling" || e.status === "pending_handle";
                return (
                  <Fragment key={e.id}>
                    <tr style={{ background: isExpanded ? "var(--panel-2)" : "transparent", cursor: "pointer" }} onClick={() => setExpandedId(isExpanded ? "" : e.id)}>
                      <td style={{ textAlign: "center", color: "var(--brand)" }}>{isExpanded ? "▼" : "▶"}</td>
                      <td style={{ fontWeight: "bold", color: "var(--ink)" }}>{EXC_TYPE_LABEL[e.exception_type] ?? e.exception_type}</td>
                      <td><span className={`tag tag-${e.level === "high" ? "high" : e.level === "medium" ? "medium" : "low"}`} style={{ fontWeight: "bold" }}>{LEVEL_LABEL[e.level]}</span></td>
                      <td><span className="tag tag-none">{SOURCE_LABEL[e.source] ?? e.source}</span></td>
                      <td className="mono" style={{ color: "var(--brand)", fontWeight: "bold" }}>{e.waybill_no || "全局"}</td>
                      <td style={{ color: e.assignee_name ? "var(--ink)" : "var(--muted)" }}>{e.assignee_name || "待认领"}</td>
                      <td className="mono num" style={{ color: Number(e.amount) > 0 ? "var(--red)" : "var(--muted)", fontWeight: "bold" }}>{Number(e.amount) > 0 ? fmtMoney(e.amount) : "暂无"}</td>
                      <td><span className={`tag tag-${e.status === "closed" ? "low" : "medium"}`}>{STATUS_LABEL[e.status] ?? e.status}</span></td>
                      <td style={{ textAlign: "right", paddingRight: 16 }}>
                        <div style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
                          {isHandling && !e.assignee_name && (
                            <button className="btn-ghost" style={{ padding: "4px 10px", fontSize: 11 }} onClick={(ev) => { ev.stopPropagation(); act.mutate({ id: e.id, action: "assign", body: { assignee: user?.id } }); }}>认领处置</button>
                          )}
                          {isHandling && (
                            <button className="btn-primary" style={{ padding: "4px 10px", fontSize: 11, background: "var(--violet)", boxShadow: "0 2px 8px rgba(139,92,246,0.3)" }} onClick={(ev) => { ev.stopPropagation(); aiResolve.mutate(e.id); }} disabled={resolvingId === e.id}>
                              {resolvingId === e.id ? "诊断中…" : "智能诊断"}
                            </button>
                          )}
                          {e.status !== "closed" && (
                            <button className="btn-danger" style={{ padding: "4px 10px", fontSize: 11 }} onClick={(ev) => { ev.stopPropagation(); act.mutate({ id: e.id, action: "close", body: { responsibility_party: "carrier", amount: 0, resolution: "已强制闭环处理" } }); }}>强制闭环</button>
                          )}
                        </div>
                      </td>
                    </tr>
                    {isExpanded && (
                      <tr style={{ background: "var(--panel-2)" }}>
                        <td colSpan={9} style={{ padding: "0 24px 24px" }}>
                          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 16, marginTop: 10 }}>
                            <div style={{ padding: "16px 20px", background: "var(--panel)", border: "1px solid var(--line-strong)", borderRadius: 12 }}>
                              <div style={{ marginBottom: 12, color: "var(--red)", fontWeight: "bold" }}>异常描述</div>
                              <div style={{ background: "var(--red-weak)", padding: 14, borderRadius: 8, fontSize: 13, lineHeight: 1.6, color: "var(--red)", borderLeft: "4px solid var(--red)" }}>{e.description || "暂无描述"}</div>
                              <div className="muted small" style={{ marginTop: 12 }}>创建于：{fmtDateTime(e.created_at)}</div>
                            </div>
                            <div style={{ padding: "16px 20px", background: "var(--panel)", border: "1px solid var(--violet)", borderRadius: 12 }}>
                              <div style={{ marginBottom: 12, color: "var(--violet)", fontWeight: "bold" }}>智能预案</div>
                              {resolvingId === e.id ? (
                                <div className="muted small" style={{ padding: "20px 0", textAlign: "center" }}>正在生成预案…</div>
                              ) : e.resolution ? (
                                <pre style={{ whiteSpace: "pre-wrap", background: "var(--violet-weak)", padding: 14, borderRadius: 8, fontSize: 13, lineHeight: 1.6, color: "var(--ink-2)", margin: 0, border: "1px solid var(--violet-weak)", fontFamily: "var(--font-sans)" }}>{e.resolution}</pre>
                              ) : (
                                <div className="muted small" style={{ padding: "20px 0", textAlign: "center" }}>尚未生成处理方案，点击「智能诊断」生成。</div>
                              )}
                            </div>
                          </div>
                          <div style={{ marginTop: 16, padding: "16px 20px", background: "var(--panel)", border: "1px solid var(--line-strong)", borderRadius: 12 }}>
                            <div style={{ marginBottom: 12, color: "var(--ink-2)", fontWeight: "bold" }}>处置流水</div>
                            {timeline.isLoading ? (
                              <span className="muted small">加载中…</span>
                            ) : (timeline.data?.length ?? 0) === 0 ? (
                              <span className="muted small">暂无留痕</span>
                            ) : (
                              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                                {(timeline.data ?? []).map((ev) => (
                                  <div key={ev.id} style={{ display: "flex", gap: 10, fontSize: 12, alignItems: "baseline" }}>
                                    <span className="tag tag-none" style={{ flexShrink: 0 }}>{EXC_EVENT_LABEL[ev.event_type] ?? ev.event_type}</span>
                                    <span className="muted mono small" style={{ flexShrink: 0 }}>{fmtDateTime(ev.event_time)}</span>
                                    <span className="muted small">{ev.actor_name || "系统"}</span>
                                    {ev.note && <span className="small" style={{ color: "var(--ink-2)", flex: 1 }}>{ev.note.length > 60 ? `${ev.note.slice(0, 60)}…` : ev.note}</span>}
                                  </div>
                                ))}
                              </div>
                            )}
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
