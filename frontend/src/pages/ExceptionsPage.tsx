import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, Fragment } from "react";

import { apiGet, apiPost } from "../api/client";
import { toast } from "../api/toast";
import type { ExceptionEvent, ExceptionRecord, Paginated } from "../api/types";
import { EXC_EVENT_LABEL } from "../api/types";
import { useAuth } from "../auth/auth";
import { IconSparkles, IconAlert } from "../components/Icons";

const LEVEL_LABEL: Record<string, string> = { low: "低风险", medium: "中风险", high: "高风险" };
const STATUS_LABEL: Record<string, string> = {
  pending_handle: "待处理",
  handling: "处理中",
  pending_audit: "待审核",
  closed: "已关闭",
  rejected: "已驳回",
};
const EXC_TYPE_LABEL: Record<string, string> = {
  transit_delay: "在途超时", route_deviation: "偏航/路线异常", cargo_damage: "货损货差",
  vehicle_breakdown: "车辆故障", detained: "扣车扣货", customer_complaint: "客户投诉", 
  temperature: "冷链温度异常", fuel: "油耗/漏油异常", overspeed: "超速驾驶", other: "其他",
};

export function ExceptionsPage() {
  const queryClient = useQueryClient();
  const { user } = useAuth();
  
  const [type, setType] = useState("transit_delay");
  const [desc, setDesc] = useState("");
  const [level, setLevel] = useState("medium");
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

  const create = useMutation({
    mutationFn: () => apiPost("/exceptions", { exception_type: type, description: desc, level }),
    onSuccess: () => {
      setDesc("");
      toast.success("人工异常提报成功");
      invalidate();
    },
  });

  const act = useMutation({
    mutationFn: (v: { id: string; action: string; body?: Record<string, unknown> }) =>
      apiPost(`/exceptions/${v.id}/${v.action}`, v.body ?? {}),
    onSuccess: (_d, v) => {
      toast.success(v.action === "close" ? "异常处理已闭环" : "已更新处理状态");
      invalidate();
    },
  });

  // 自动诊断与预案生成
  const aiResolve = useMutation({
    mutationFn: (id: string) => {
      setResolvingId(id);
      return apiPost<{ ai_resolution: string }>(`/exceptions/${id}/ai-resolve`, {});
    },
    onSuccess: (data, id) => {
      toast.success("诊断已完成");
      setResolvingId("");
      setExpandedId(id);
      invalidate();
    },
    onError: () => {
      toast.error("AI 服务调用失败");
      setResolvingId("");
    }
  });

  const timeline = useQuery({
    queryKey: ["exception-timeline", expandedId],
    queryFn: () => apiGet<ExceptionEvent[]>(`/exceptions/${expandedId}/timeline`),
    enabled: Boolean(expandedId),
  });

  const items = list.data?.items ?? [];

  return (
    <div className="stack" style={{ position: "relative" }}>
      
      {/* 头部 */}
      <div className="panel" style={{ background: "linear-gradient(135deg, #1b1e25 0%, #16181d 100%)", color: "#fff", border: "none" }}>
        <div style={{ padding: "20px 24px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <div>
            <div style={{ fontSize: 22, fontWeight: "bold", display: "flex", alignItems: "center", gap: 10 }}>
              异常处置
            </div>
            <div style={{ fontSize: 13, color: "#94a3b8", marginTop: 6 }}>
              管理设备报警生成的在途异常，或手动提报异常。
            </div>
          </div>
          <div className="form-row" style={{ gap: 10, background: "rgba(255,255,255,0.05)", padding: "10px 14px", borderRadius: 8, border: "1px solid rgba(255,255,255,0.1)" }}>
            <span style={{ fontSize: 12, fontWeight: "bold" }}>手动提报：</span>
            <select value={type} onChange={(e) => setType(e.target.value)} style={{ padding: "6px 8px", width: 140 }}>
              {Object.entries(EXC_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
            <select value={level} onChange={(e) => setLevel(e.target.value)} style={{ padding: "6px 8px" }}>
              <option value="high">高风险</option><option value="medium">中风险</option><option value="low">低风险</option>
            </select>
            <input className="search" placeholder="异常状况描述..." value={desc} onChange={(e) => setDesc(e.target.value)} style={{ width: 220 }} />
            <button className="btn-primary" disabled={create.isPending || !desc.trim()} onClick={() => create.mutate()} style={{ background: "var(--red)" }}>
              + 立案
            </button>
          </div>
        </div>
      </div>

      <div className="panel" style={{ flex: 1 }}>
        <div className="panel-head">
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            未结异常
            <span className="tag tag-medium" style={{ fontSize: 11, fontWeight: "bold" }}>{items.filter(i => i.status !== "closed").length} 待办</span>
          </div>
        </div>
        {list.isLoading ? (
          <div style={{ padding: "24px 18px", display: "flex", flexDirection: "column", gap: 12 }}>
            <div className="skeleton" style={{ width: "100%", height: 32 }}></div>
            <div className="skeleton" style={{ width: "100%", height: 32, opacity: 0.8 }}></div>
            <div className="skeleton" style={{ width: "100%", height: 32, opacity: 0.6 }}></div>
          </div>
        ) : items.length === 0 ? (
          <div className="empty-state">
            <div className="empty-icon">✓</div>
            <div className="empty-title">无活动异常</div>
            <div className="empty-hint muted small">暂无在途异常。</div>
          </div>
        ) : (
          <table className="table" style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ background: "var(--line)" }}>
                <th style={{ padding: "10px 12px", width: 40 }}></th>
                <th>异常类型</th>
                <th>风险等级</th>
                <th>触发来源</th>
                <th>运单号</th>
                <th>跟进人</th>
                <th>理赔金额</th>
                <th>处置状态</th>
                <th style={{ textAlign: "right", paddingRight: 20 }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {items.map((e) => {
                const isExpanded = expandedId === e.id;
                const isHandling = e.status === "handling" || e.status === "pending_handle";
                
                return (
                  <Fragment key={e.id}>
                    <tr style={{ background: isExpanded ? "rgba(0,0,0,0.015)" : "transparent", cursor: "pointer", transition: "all 0.2s" }} onClick={() => setExpandedId(isExpanded ? "" : e.id)}>
                      <td style={{ textAlign: "center", color: "var(--brand)", fontSize: 14 }}>
                        {isExpanded ? "▼" : "▶"}
                      </td>
                      <td style={{ fontWeight: "bold", color: "var(--ink)" }}>{EXC_TYPE_LABEL[e.exception_type] ?? e.exception_type}</td>
                      <td>
                        <span className={`tag tag-${e.level === "high" ? "high" : e.level === "medium" ? "medium" : "low"}`} style={{ fontWeight: "bold", padding: "4px 8px", fontSize: 11 }}>
                          {LEVEL_LABEL[e.level]}
                        </span>
                      </td>
                      <td>
                        <span className="tag" style={{ background: "rgba(0,0,0,0.04)" }}>{e.source === "track" ? "车联网设备" : e.source}</span>
                      </td>
                      <td className="mono" style={{ color: "var(--brand)", fontWeight: "bold" }}>{e.waybill_no || "全局"}</td>
                      <td style={{ color: e.assignee_name ? "var(--ink)" : "var(--muted)" }}>{e.assignee_name || "待认领"}</td>
                      <td className="mono" style={{ color: Number(e.amount) > 0 ? "var(--red)" : "var(--muted)", fontWeight: "bold" }}>
                        {Number(e.amount) > 0 ? `¥${e.amount}` : "暂无"}
                      </td>
                      <td>
                        <span className={`tag tag-${e.status === "closed" ? "low" : "medium"}`}>
                          {STATUS_LABEL[e.status] ?? e.status}
                        </span>
                      </td>
                      <td style={{ textAlign: "right", paddingRight: 16 }}>
                        <div style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
                          {isHandling && !e.assignee_name && (
                            <button className="btn-ghost" style={{ padding: "4px 10px", fontSize: 11 }} onClick={(ev) => { ev.stopPropagation(); act.mutate({ id: e.id, action: "assign", body: { assignee: user?.id } }); }}>
                              认领处置
                            </button>
                          )}
                          {isHandling && (
                            <button 
                              className="btn-primary" 
                              style={{ padding: "4px 10px", fontSize: 11, background: "var(--violet)", boxShadow: "0 2px 8px rgba(139,92,246,0.3)" }} 
                              onClick={(ev) => { ev.stopPropagation(); aiResolve.mutate(e.id); }}
                              disabled={resolvingId === e.id}
                            >
                              {resolvingId === e.id ? "诊断中…" : "智能诊断"}
                            </button>
                          )}
                          {e.status !== "closed" && (
                            <button className="btn-danger" style={{ padding: "4px 10px", fontSize: 11 }} onClick={(ev) => { ev.stopPropagation(); act.mutate({ id: e.id, action: "close", body: { responsibility_party: "carrier", amount: 0, resolution: "已强制闭环处理" } }); }}>
                              强制闭环
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                    
                    {/* 异常详情 */}
                    {isExpanded && (
                      <tr style={{ background: "rgba(0,0,0,0.015)" }}>
                        <td colSpan={9} style={{ padding: "0 24px 24px" }}>
                          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 16, marginTop: 10 }}>
                            
                            {/* 左侧：传感器描述记录 */}
                            <div style={{ padding: "16px 20px", background: "#fff", border: "1px solid var(--line-strong)", borderRadius: 12, boxShadow: "0 4px 12px rgba(0,0,0,0.02)" }}>
                              <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12, color: "var(--red)", fontWeight: "bold" }}>
                                异常描述
                              </div>
                              <div style={{ background: "var(--red-weak)", padding: 14, borderRadius: 8, fontSize: 13, lineHeight: 1.6, color: "var(--red)", borderLeft: "4px solid var(--red)" }}>
                                {e.description || "暂无描述"}
                              </div>
                              <div className="muted small" style={{ marginTop: 12 }}>
                                创建于：{new Date(e.created_at).toLocaleString()}
                              </div>
                            </div>

                            {/* 右侧：AI 预案与执行流水 */}
                            <div style={{ padding: "16px 20px", background: "#fff", border: "1px solid var(--violet)", borderRadius: 12, boxShadow: "0 4px 12px rgba(139,92,246,0.05)" }}>
                              <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12, color: "var(--violet)", fontWeight: "bold" }}>
                                智能预案
                              </div>
                              
                              {resolvingId === e.id ? (
                                <div className="muted small" style={{ padding: "20px 0", textAlign: "center" }}>
                                  正在生成预案…
                                </div>
                              ) : e.resolution ? (
                                <pre style={{ 
                                  whiteSpace: "pre-wrap", background: "rgba(139,92,246,0.03)", padding: 14, borderRadius: 8, 
                                  fontSize: 13, lineHeight: 1.6, color: "var(--ink-2)", margin: 0, border: "1px solid rgba(139,92,246,0.15)",
                                  fontFamily: "var(--font-sans)"
                                }}>
                                  {e.resolution}
                                </pre>
                              ) : (
                                <div className="muted small" style={{ padding: "20px 0", textAlign: "center" }}>
                                  尚未生成处理方案，点击「智能诊断」按钮生成。
                                </div>
                              )}
                            </div>

                          </div>

                          {/* 处置流水 */}
                          <div style={{ marginTop: 16, padding: "16px 20px", background: "#fff", border: "1px solid var(--line-strong)", borderRadius: 12 }}>
                            <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12, color: "var(--ink-2)", fontWeight: "bold" }}>
                              处置流水
                            </div>
                            {timeline.isLoading ? (
                              <span className="muted small">加载中…</span>
                            ) : (timeline.data?.length ?? 0) === 0 ? (
                              <span className="muted small">暂无留痕</span>
                            ) : (
                              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                                {(timeline.data ?? []).map((ev) => (
                                  <div key={ev.id} style={{ display: "flex", gap: 10, fontSize: 12, alignItems: "baseline" }}>
                                    <span className="tag" style={{ background: "rgba(0,0,0,0.04)", flexShrink: 0 }}>{EXC_EVENT_LABEL[ev.event_type] ?? ev.event_type}</span>
                                    <span className="muted mono small" style={{ flexShrink: 0 }}>{new Date(ev.event_time).toLocaleString()}</span>
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
        )}
      </div>
    </div>
  );
}
