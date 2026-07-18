import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { apiGet, apiPost, apiUpload } from "../api/client";
import { fmtDateTime } from "../api/format";
import { toast } from "../api/toast";
import { COD_STATUS_LABEL, REIMB_CATEGORY_LABEL, STATUS_LABEL, type Contract, type CostCatalog, type CostSummary, type DriverCollection, type DriverReminder, type ExceptionRecord, type Paginated, type Reimbursement, type ReminderTemplate, type Receipt, type WaybillDetail } from "../api/types";
import { SignaturePad } from "../components/SignaturePad";
import { TrajectoryMap, type Trajectory } from "../components/TrajectoryMap";

const fmt = fmtDateTime;
const RISK_LABEL: Record<string, string> = { high: "高", medium: "中", low: "低", none: "无" };
const EXC_TYPE_LABEL: Record<string, string> = {
  transit_delay: "在途超时", route_deviation: "偏航", cargo_damage: "货损货差",
  vehicle_breakdown: "车辆故障", detained: "扣车扣货", customer_complaint: "客户投诉", other: "其他",
};
const EXC_STATUS_LABEL: Record<string, string> = {
  pending_handle: "待处理", handling: "处理中", pending_audit: "待审核", closed: "已关闭", rejected: "已驳回",
};

// 运单状态流转
const WORKFLOW_STEPS = [
  { status: "draft", label: "草稿", icon: "📝" },
  { status: "pending_dispatch", label: "待调度", icon: "" },
  { status: "dispatching", label: "派单中", icon: "" },
  { status: "dispatched", label: "已派发", icon: "" },
  { status: "departed", label: "发车", icon: "" },
  { status: "in_transit", label: "在途", icon: "" },
  { status: "arrived", label: "到达", icon: "" },
  { status: "signed", label: "签收", icon: "✍️" },
  { status: "delivered", label: "回单交接", icon: "" },
  { status: "settled", label: "完结核销", icon: "" }
];

export function WaybillDetailPage() {
  const { no = "" } = useParams();
  const queryClient = useQueryClient();

  const detail = useQuery({
    queryKey: ["waybill", no],
    queryFn: () => apiGet<WaybillDetail>(`/waybills/${no}`),
  });
  const costs = useQuery({
    queryKey: ["waybill", no, "costs"],
    queryFn: () => apiGet<CostSummary>(`/waybills/${no}/costs`),
  });
  const traj = useQuery({
    queryKey: ["waybill", no, "trajectory"],
    queryFn: () => apiGet<Trajectory>(`/telematics/waybills/${no}/trajectory`),
  });
  const eta = useQuery({
    queryKey: ["waybill", no, "eta"],
    queryFn: () => apiGet<{ predicted: boolean; estimated_arrival: string | null; planned_arrival: string | null; eta_drift_minutes: number; remaining_km: number | null; avg_speed_kmh: number | null }>(`/waybills/${no}/eta`),
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["waybill", no] });

  const transition = useMutation({
    mutationFn: (to: string) => apiPost<WaybillDetail>(`/waybills/${no}/transition`, { to_status: to }),
    onSuccess: invalidate,
  });
  const analyze = useMutation({
    mutationFn: () =>
      apiPost("/agent/tools/execute", {
        tool_name: "logistics.eta_risk_analysis",
        arguments: { waybill_no: no },
      }),
    onSuccess: invalidate,
  });
  const genCosts = useMutation({
    mutationFn: () => apiPost(`/waybills/${no}/generate-costs`, {}),
    onSuccess: invalidate,
  });
  const confirm = useMutation({
    mutationFn: (vars: { id: string; status: string }) =>
      apiPost(`/ai/suggestions/${vars.id}/confirm`, { status: vars.status }),
    onSuccess: invalidate,
  });

  const [signatory, setSignatory] = useState("");
  const [signature, setSignature] = useState("");
  const sign = useMutation({
    mutationFn: () => apiPost(`/waybills/${no}/sign`, { signatory, signature, sign_source: "driver" }),
    onSuccess: () => { setSignatory(""); setSignature(""); invalidate(); },
  });

  const collection = useQuery({
    queryKey: ["waybill", no, "collection"],
    queryFn: () => apiGet<DriverCollection>(`/waybills/${no}/collection`),
    enabled: Boolean(detail.data),
  });
  const codAction = useMutation({
    mutationFn: (action: "collect-cod" | "remit-cod") => apiPost(`/waybills/${no}/${action}`, {}),
    onSuccess: (_d, action) => {
      toast.success(action === "collect-cod" ? "已确认代收货款" : "已确认回款给货主");
      invalidate();
      queryClient.invalidateQueries({ queryKey: ["waybill", no, "collection"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const fileInput = useRef<HTMLInputElement>(null);
  const receipts = useQuery({
    queryKey: ["waybill", no, "receipts"],
    queryFn: () => apiGet<Paginated<Receipt>>(`/receipts?waybill=${detail.data?.id}`),
    enabled: Boolean(detail.data?.id),
  });
  const upload = useMutation({
    mutationFn: (file: File) => {
      const fd = new FormData();
      fd.append("waybill", detail.data!.id);
      fd.append("file", file);
      return apiUpload<Receipt>("/receipts", fd);
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["waybill", no, "receipts"] }),
  });

  const [excType, setExcType] = useState("transit_delay");
  const [excLevel, setExcLevel] = useState("medium");
  const [excDesc, setExcDesc] = useState("");
  const exceptions = useQuery({
    queryKey: ["waybill", no, "exceptions"],
    queryFn: () => apiGet<Paginated<ExceptionRecord>>(`/exceptions?waybill=${detail.data?.id}&page_size=50`),
    enabled: Boolean(detail.data?.id),
  });
  const reportExc = useMutation({
    mutationFn: () => apiPost("/exceptions", {
      waybill: detail.data!.id, exception_type: excType, level: excLevel, description: excDesc, source: "manual",
    }),
    onSuccess: () => {
      setExcDesc("");
      toast.success("异常已上报，进入处理队列");
      queryClient.invalidateQueries({ queryKey: ["waybill", no, "exceptions"] });
    },
  });

  const stopEvent = useMutation({
    mutationFn: (v: { seq: number; event: "arrived" | "departed" }) =>
      apiPost(`/waybills/${no}/stop-event`, v),
    onSuccess: (_d, v) => { toast.success(v.event === "arrived" ? "已记录到达" : "已记录离开"); invalidate(); },
  });

  const contract = useQuery({
    queryKey: ["waybill", no, "contract"],
    queryFn: () => apiGet<Contract | null>(`/waybills/${no}/contract`),
  });
  const invalidateContract = () => queryClient.invalidateQueries({ queryKey: ["waybill", no, "contract"] });
  const genContract = useMutation({
    mutationFn: () => apiPost(`/waybills/${no}/contract`, {}),
    onSuccess: () => { toast.success("合同已生成（含PDF）"); invalidateContract(); },
  });
  const sendContract = useMutation({
    mutationFn: () => apiPost(`/waybills/${no}/contract/send`, {}),
    onSuccess: () => { toast.success("合同已发送给司机"); invalidateContract(); },
  });
  const confirmContract = useMutation({
    mutationFn: (accepted: boolean) => apiPost(`/waybills/${no}/contract/confirm`, { accepted, reply: accepted ? "同意承运" : "拒签" }),
    onSuccess: () => { toast.success("已更新合同确认状态"); invalidateContract(); },
  });

  const reminderTpls = useQuery({ queryKey: ["reminder-templates"], queryFn: () => apiGet<Paginated<ReminderTemplate>>("/reminder-templates?is_active=true&page_size=100") });
  const reminders = useQuery({
    queryKey: ["waybill", no, "reminders"],
    queryFn: () => apiGet<DriverReminder[]>(`/waybills/${no}/reminders`),
  });
  const [rmTpl, setRmTpl] = useState("");
  const [rmContent, setRmContent] = useState("");
  const [rmAck, setRmAck] = useState(true);
  const sendReminder = useMutation({
    mutationFn: () => apiPost(`/waybills/${no}/reminders`, {
      template: rmTpl || undefined, content: rmContent || undefined, ack_required: rmAck,
    }),
    onSuccess: () => {
      setRmContent(""); setRmTpl("");
      toast.success("提醒已发送");
      queryClient.invalidateQueries({ queryKey: ["waybill", no, "reminders"] });
    },
  });

  const reimbursements = useQuery({
    queryKey: ["waybill", no, "reimbursements"],
    queryFn: () => apiGet<Paginated<Reimbursement>>(`/finance/reimbursements?waybill=${detail.data?.id}&page_size=50`),
    enabled: Boolean(detail.data?.id),
  });
  const invalidateReimb = () => queryClient.invalidateQueries({ queryKey: ["waybill", no, "reimbursements"] });
  const [bxCat, setBxCat] = useState("toll");
  const [bxAmount, setBxAmount] = useState("");
  const [bxReason, setBxReason] = useState("");
  const submitReimb = useMutation({
    mutationFn: () => apiPost("/finance/reimbursements", { waybill_no: no, category: bxCat, amount: bxAmount, reason: bxReason }),
    onSuccess: () => { setBxAmount(""); setBxReason(""); toast.success("报销已提交"); invalidateReimb(); },
  });
  const reimbAction = useMutation({
    mutationFn: (v: { id: string; action: string }) => apiPost(`/finance/reimbursements/${v.id}/${v.action}`, {}),
    onSuccess: (_d, v) => { toast.success(v.action === "approve" ? "已审批，生成应付与付款申请" : v.action === "pay" ? "已付款" : "已驳回"); invalidateReimb(); },
  });

  const catalog = useQuery({ queryKey: ["cost-catalog"], queryFn: () => apiGet<CostCatalog>("/waybills/cost-catalog") });
  const [exDir, setExDir] = useState<"payable" | "receivable">("payable");
  const [exItem, setExItem] = useState("TRANSPORT_COST");
  const [exAmount, setExAmount] = useState("");
  const [exPayeeType, setExPayeeType] = useState("carrier");
  const [exPayeeRef, setExPayeeRef] = useState("");
  const addExpense = useMutation({
    mutationFn: () => apiPost(`/waybills/${no}/add-expense`, {
      direction: exDir, expense_item_code: exItem, amount: exAmount,
      payee_type: exPayeeType, payee_ref: exPayeeRef,
    }),
    onSuccess: () => {
      setExAmount(""); setExPayeeRef("");
      toast.success("已新增费用明细");
      queryClient.invalidateQueries({ queryKey: ["waybill", no, "costs"] });
    },
  });

  if (detail.isLoading) return <div className="muted" style={{ padding: 40, textAlign: "center" }}>加载中…</div>;
  if (detail.isError || !detail.data) return <div className="muted" style={{ padding: 40, textAlign: "center" }}>运单不存在或无权访问。</div>;
  
  const w = detail.data;
  const editable = !["settled", "cancelled", "voided"].includes(w.status);
  
  const currentStepIdx = WORKFLOW_STEPS.findIndex(s => s.status === w.status);

  return (
    <div className="stack" style={{ gap: 16 }}>
      {/* 运单头部 */}
      <div className="panel" style={{ overflow: "visible" }}>
        <div style={{ background: "#09090b", color: "#f4f4f5", padding: "20px 24px", display: "flex", justifyContent: "space-between", alignItems: "flex-start", borderTopLeftRadius: "var(--radius)", borderTopRightRadius: "var(--radius)" }}>
          <div className="stack" style={{ gap: 6 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
              <span className="mono" style={{ fontSize: 24, fontWeight: 700, letterSpacing: "-0.02em" }}>{w.waybill_no}</span>
              <span className="tag" style={{ background: "rgba(255,255,255,0.1)", color: "#e4e4e7", border: "1px solid rgba(255,255,255,0.2)", fontWeight: "500" }}>
              </span>
              {w.receipt_status === "returned" && <span className="tag tag-low">POD Verified</span>}
            </div>
            <div style={{ color: "#a1a1aa", fontSize: 13, display: "flex", gap: 16, fontWeight: "400" }}>
              <span>{w.route_name} ({w.origin} → {w.destination})</span>
              <span>{w.customer_name || "Unknown"}</span>
              <span>{w.vehicle_plate || "Self-Fleet"}</span>
            </div>
          </div>
          
          <div className="stack" style={{ alignItems: "flex-end", gap: 8 }}>
            <span className={`tag tag-${w.risk_level === 'high' ? 'high' : w.risk_level === 'medium' ? 'medium' : 'low'}`} style={{ fontSize: 12, padding: "4px 10px" }}>
              Risk: {RISK_LABEL[w.risk_level]}
            </span>
            <div className="row-actions">
              <button className="btn-ghost" style={{ color: "#fff", border: "1px solid rgba(255,255,255,0.2)", background: "transparent" }} disabled={analyze.isPending} onClick={() => analyze.mutate()}>
风险分析              </button>
              {w.next_statuses.map((s) => (
                <button
                  key={s}
                  className="btn-primary"
                  style={{ background: "#fff", color: "#09090b", borderColor: "#fff" }}
                  disabled={transition.isPending}
                  onClick={() => transition.mutate(s)}
                >
                  {STATUS_LABEL[s] ?? s}
                </button>
              ))}
            </div>
          </div>
        </div>

        {/* 状态流转 */}
        <div style={{ padding: "16px 24px", background: "var(--panel)" }}>
          <div className="wf-track">
            {WORKFLOW_STEPS.map((step, i) => {
              const isDone = i < currentStepIdx;
              const isCurrent = i === currentStepIdx;
              return (
                <div key={step.status} className={`wf-step ${isDone ? "done" : ""} ${isCurrent ? "current" : ""}`}>
                  <div className="wf-dot">{isDone ? "✓" : isCurrent ? "●" : ""}</div>
                  <div className="wf-name">{step.label}</div>
                  <div className="wf-detail">{step.icon}</div>
                </div>
              );
            })}
          </div>
        </div>
      </div>

      <div className="ct-grid" style={{ gridTemplateColumns: "1.4fr 1fr" }}>
        {/* 左侧：在途与运营 */}
        <div className="stack">
          {/* ETA 预测 */}
          {eta.data?.predicted && (
            <div className="panel">
              <div className="panel-head" style={{ borderLeft: "4px solid var(--brand)" }}>
                ETA 预测
              </div>
              <div className="kv" style={{ padding: "12px 16px" }}>
                <div><span>预计到达</span><b>{eta.data.estimated_arrival ? new Date(eta.data.estimated_arrival).toLocaleString() : "-"}</b></div>
                <div><span>剩余里程</span><b>{eta.data.remaining_km ?? "-"} km</b></div>
                <div><span>当前均速</span><b>{eta.data.avg_speed_kmh ?? "-"} km/h</b></div>
                <div>
                  <span>相对计划</span>
                  <b style={{ color: eta.data.eta_drift_minutes > 0 ? "var(--red)" : "var(--green)" }}>
                    {eta.data.eta_drift_minutes > 0 ? `晚 ${eta.data.eta_drift_minutes} 分` : eta.data.eta_drift_minutes < 0 ? `早 ${-eta.data.eta_drift_minutes} 分` : "准点"}
                  </b>
                </div>
              </div>
            </div>
          )}

          <div className="panel">
            <div className="panel-head">在途轨迹</div>
            {traj.isLoading ? (
              <div className="muted" style={{ padding: 24, textAlign: "center" }}>加载轨迹数据…</div>
            ) : traj.data ? (
              <TrajectoryMap traj={traj.data} />
            ) : (
              <div className="muted small" style={{ padding: 24, textAlign: "center" }}>暂无轨迹数据。</div>
            )}
            
            {w.stops && w.stops.length > 0 && (
              <table className="table" style={{ borderTop: "1px solid var(--line)" }}>
                <thead><tr style={{ background: "var(--panel-2)" }}><th>提/送类型</th><th>地理围栏地址</th><th>计划ETA</th><th>打卡确认</th><th>围栏操作</th></tr></thead>
                <tbody>
                  {w.stops.map((s) => (
                    <tr key={s.id}>
                      <td style={{ fontWeight: "bold", color: "var(--ink-2)" }}>{s.stop_type_label}</td>
                      <td className="small">{s.address || s.city || "-"}</td>
                      <td className="small mono" style={{ color: "var(--brand)" }}>{fmt(s.planned_eta)}</td>
                      <td className="small">
                        {s.actual_arrival_at ? <span style={{ color: "var(--green)", fontWeight: "bold" }}>✓ {fmt(s.actual_arrival_at)}</span> : <span className="muted">未到达</span>}
                      </td>
                      <td>
                        {!s.actual_arrival_at && (
                          <button className="btn-primary" style={{ padding: "4px 10px", fontSize: 11 }} disabled={stopEvent.isPending} onClick={() => stopEvent.mutate({ seq: s.seq, event: "arrived" })}>人工到站</button>
                        )}
                        {s.actual_arrival_at && !s.actual_depart_at && (
                          <button className="btn-ghost" style={{ padding: "4px 10px", fontSize: 11 }} disabled={stopEvent.isPending} onClick={() => stopEvent.mutate({ seq: s.seq, event: "departed" })}>发车放行</button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          {/* 异常上报 */}
          <div className="panel">
            <div className="panel-head">异常处置</div>
            <div className="form-row" style={{ flexWrap: "wrap", gap: 10, background: "rgba(239, 68, 68, 0.04)" }}>
              <select value={excType} onChange={(e) => setExcType(e.target.value)}>
                {Object.entries(EXC_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
              <select value={excLevel} onChange={(e) => setExcLevel(e.target.value)}>
                <option value="low">低</option><option value="medium">中</option><option value="high">高</option>
              </select>
              <input className="search" style={{ flex: 1, minWidth: 200, background: "#fff" }} placeholder="异常描述（如：高速拥堵预计延误2小时）" value={excDesc} onChange={(e) => setExcDesc(e.target.value)} />
              <button className="btn-danger" disabled={reportExc.isPending || !excDesc.trim()} onClick={() => reportExc.mutate()}>紧急上报</button>
            </div>
            {(exceptions.data?.items?.length ?? 0) > 0 && (
              <table className="table">
                <thead><tr><th>类型</th><th>级别</th><th>描述</th><th>状态</th></tr></thead>
                <tbody>
                  {(exceptions.data?.items ?? []).map((ex) => (
                    <tr key={ex.id}>
                      <td>{EXC_TYPE_LABEL[ex.exception_type] ?? ex.exception_type}</td>
                      <td><span className={`tag tag-${ex.level === "high" ? "high" : ex.level === "low" ? "low" : "medium"}`}>{RISK_LABEL[ex.level] ?? ex.level}</span></td>
                      <td className="small">{ex.description || "-"}</td>
                      <td><Link className="link" to="/exceptions">{EXC_STATUS_LABEL[ex.status] ?? ex.status}</Link></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          {/* 司机提醒 */}
          <div className="panel">
            <div className="panel-head">司机提醒</div>
            <div style={{ padding: "16px 20px" }} className="stack">
              <div className="form-row" style={{ gap: 10, flexWrap: "wrap", padding: 0 }}>
                <select value={rmTpl} onChange={(e) => { setRmTpl(e.target.value); const t = (reminderTpls.data?.items ?? []).find((x) => x.id === e.target.value); if (t) setRmContent(t.content); }}>
                  <option value="">选择模板…</option>
                  {(reminderTpls.data?.items ?? []).map((t) => <option key={t.id} value={t.id}>{t.category ? `[${t.category}] ` : ""}{t.name}</option>)}
                </select>
                <label className="small" style={{ display: "flex", alignItems: "center", gap: 6, fontWeight: "bold" }}>
                  <input type="checkbox" checked={rmAck} onChange={(e) => setRmAck(e.target.checked)} />需确认阅读
                </label>
                <span style={{ flex: 1 }} />
                <button className="btn-primary" disabled={sendReminder.isPending || !rmContent.trim()} onClick={() => sendReminder.mutate()}>发送提醒</button>
              </div>
              <textarea className="search" style={{ width: "100%", minHeight: 70 }} placeholder="提醒下发内容（支持多行）" value={rmContent} onChange={(e) => setRmContent(e.target.value)} />
              {(reminders.data?.length ?? 0) > 0 && (
                <table className="table">
                  <thead><tr style={{ background: "var(--panel-2)" }}><th>标题</th><th>需确认</th><th>发送时间</th><th>状态</th></tr></thead>
                  <tbody>
                    {(reminders.data ?? []).map((r) => (
                      <tr key={r.id}>
                        <td className="small"><strong>{r.title}</strong></td>
                        <td className="small">{r.ack_required ? "是" : "否"}</td>
                        <td className="small mono muted">{fmt(r.sent_at)}</td>
                        <td><span className={`tag${r.status === "acknowledged" ? " tag-low" : " tag-high"}`}>{r.status === "acknowledged" ? `已确认 ${fmt(r.acknowledged_at)}` : "未读"}</span></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        </div>

        {/* 右侧：财务与签收 */}
        <div className="stack">
          {/* AI 建议 */}
          <div className="panel" style={{ border: "1px solid var(--violet)" }}>
            <div className="panel-head" style={{ background: "rgba(139,92,246,0.06)", color: "var(--violet)", borderBottomColor: "var(--violet)" }}>
              AI 建议            </div>
            {w.agent_suggestions.length === 0 ? (
              <div className="muted small" style={{ padding: 24, textAlign: "center" }}>暂无建议</div>
            ) : (
              <ul className="suggestions" style={{ padding: "12px 18px" }}>
                {w.agent_suggestions.map((s) => (
                  <li key={s.id} style={{ background: "#fff", borderColor: "rgba(139,92,246,0.2)" }}>
                    <div className="sg-title" style={{ color: "var(--ink)" }}>{s.title}</div>
                    <div className="muted small">{s.body}</div>
                    <div className="sg-actions">
                      <span className={`tag tag-${s.status === "accepted" ? "low" : s.status === "rejected" ? "none" : "medium"}`}>
                        {s.status === "pending" ? "等待审批" : s.status === "accepted" ? "已采纳执行" : "已驳回"}
                      </span>
                      {s.status === "pending" && (
                        <>
                          <button className="btn-primary" style={{ padding: "3px 10px", fontSize: 11 }} onClick={() => confirm.mutate({ id: s.id, status: "accepted" })}>
                            采纳建议
                          </button>
                          <button className="btn-ghost" style={{ padding: "3px 10px", fontSize: 11 }} onClick={() => confirm.mutate({ id: s.id, status: "rejected" })}>
                            忽略
                          </button>
                        </>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {/* 运费付款方式与代收货款 */}
          {detail.data && (
            <div className="panel">
              <div className="panel-head">运费付款与代收货款</div>
              <div style={{ padding: "14px 20px", display: "flex", flexDirection: "column", gap: 10 }}>
                <div style={{ display: "flex", gap: 20, flexWrap: "wrap" }}>
                  <div className="small"><span className="muted">付款方式：</span><b>{detail.data.freight_term_label}</b></div>
                  <div className="small"><span className="muted">承担方：</span><b>{detail.data.freight_payer_label}</b></div>
                </div>
                {collection.data && collection.data.freight_term === "collect" && collection.data.collect_freight > 0 && (
                  <div className="small" style={{ color: "var(--amber)" }}>
                    到付：司机送达时需向收货人收取运费 ¥{collection.data.collect_freight.toLocaleString()}
                  </div>
                )}
                {Number(detail.data.cod_amount) > 0 && (
                  <div style={{ background: "rgba(245,158,11,0.06)", border: "1px solid rgba(245,158,11,0.25)", borderRadius: 8, padding: 12 }}>
                    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <div>
                        <div style={{ fontWeight: "bold" }}>代收货款 COD ¥{Number(detail.data.cod_amount).toLocaleString()}</div>
                        <div className="muted small">状态：{COD_STATUS_LABEL[detail.data.cod_status] ?? detail.data.cod_status}
                          {collection.data ? ` · 司机应收合计 ¥${collection.data.total_to_collect.toLocaleString()}` : ""}</div>
                      </div>
                      <div style={{ display: "flex", gap: 8 }}>
                        {detail.data.cod_status === "pending" && (
                          <button className="btn-primary" style={{ padding: "4px 12px", fontSize: 12 }} disabled={codAction.isPending} onClick={() => codAction.mutate("collect-cod")}>司机确认代收</button>
                        )}
                        {detail.data.cod_status === "collected" && (
                          <button className="btn-primary" style={{ padding: "4px 12px", fontSize: 12 }} disabled={codAction.isPending} onClick={() => codAction.mutate("remit-cod")}>财务确认回款</button>
                        )}
                        {detail.data.cod_status === "remitted" && <span className="tag tag-low">已回款货主</span>}
                      </div>
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* 费用台账 */}
          <div className="panel">
            <div className="panel-head" style={{ borderLeft: "4px solid var(--brand)" }}>
              费用台账              <button className="btn-ghost" style={{ fontSize: 11, padding: "4px 8px" }} disabled={genCosts.isPending} onClick={() => genCosts.mutate()}>重新生成</button>
            </div>
            {costs.data ? (
              <>
                <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12, padding: "16px 20px" }}>
                  <div style={{ background: "rgba(39,174,96,0.06)", border: "1px solid rgba(39,174,96,0.2)", borderRadius: 8, padding: 14 }}>
                    <div className="muted small" style={{ fontWeight: "bold" }}>向客户应收 (AR)</div>
                    <div style={{ fontSize: 24, fontWeight: 800, color: "var(--green)", marginTop: 4 }}>¥{costs.data.receivable_total.toFixed(2)}</div>
                  </div>
                  <div style={{ background: "rgba(231,76,60,0.06)", border: "1px solid rgba(231,76,60,0.2)", borderRadius: 8, padding: 14 }}>
                    <div className="muted small" style={{ fontWeight: "bold" }}>付承运商成本 (AP)</div>
                    <div style={{ fontSize: 24, fontWeight: 800, color: "var(--red)", marginTop: 4 }}>¥{costs.data.payable_total.toFixed(2)}</div>
                  </div>
                </div>
                <div className="kv" style={{ paddingTop: 0, paddingBottom: 10 }}>
                  <div><span>账面毛利预估</span><b style={{ fontSize: 16 }}>¥{costs.data.gross_profit.toFixed(2)}</b></div>
                  <div><span>毛利率测算</span><b>{(costs.data.gross_margin * 100).toFixed(1)}%</b></div>
                </div>
                
                {(costs.data.payables.length > 0 || costs.data.receivables.length > 0) && (
                  <table className="table" style={{ fontSize: 12 }}>
                    <thead><tr style={{ background: "var(--panel-2)" }}><th>借贷</th><th>科目名</th><th>落账金额</th><th>业务主体</th></tr></thead>
                    <tbody>
                      {[...costs.data.receivables, ...costs.data.payables].map((e) => (
                        <tr key={e.id}>
                          <td><span className={`tag${e.direction === "receivable" ? " tag-low" : " tag-high"}`}>{e.direction === "receivable" ? "应收" : "应付"}</span></td>
                          <td><strong>{e.item_label}</strong></td>
                          <td className="mono" style={{ fontWeight: "bold" }}>¥{e.amount.toFixed(2)}</td>
                          <td className="muted">{e.payee_label}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
                
                {/* 增加费用明细录入入口 */}
                {editable && (
                  <div style={{ padding: "16px 20px", borderTop: "1px dashed var(--line)" }}>
                    <div className="muted small" style={{ marginBottom: 10, fontWeight: "bold" }}>+ 录入补收/补扣费用</div>
                    <div className="form-row" style={{ gap: 8, padding: 0 }}>
                      <select value={exDir} onChange={(e) => { const d = e.target.value as "payable" | "receivable"; setExDir(d); setExItem(d === "payable" ? "TRANSPORT_COST" : "TRANSPORT_INCOME"); setExPayeeType(d === "payable" ? "carrier" : "customer"); }}>
                        <option value="payable">录应付成本</option><option value="receivable">录应收加价</option>
                      </select>
                      <select value={exItem} onChange={(e) => setExItem(e.target.value)}>
                        {Object.entries(exDir === "payable" ? (catalog.data?.cost_items ?? {}) : (catalog.data?.income_items ?? {})).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                      </select>
                      <input className="search" style={{ width: 100 }} placeholder="¥ 金额" value={exAmount} onChange={(e) => setExAmount(e.target.value)} />
                      <select value={exPayeeType} onChange={(e) => setExPayeeType(e.target.value)}>
                        {Object.entries(catalog.data?.payees ?? {}).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                      </select>
                      <input className="search" style={{ width: 110 }} placeholder="收/付款方" value={exPayeeRef} onChange={(e) => setExPayeeRef(e.target.value)} />
                      <button className="btn-ghost" disabled={addExpense.isPending || !exAmount} onClick={() => addExpense.mutate()}>保存</button>
                    </div>
                  </div>
                )}
              </>
            ) : (
              <div className="muted small" style={{ padding: 24, textAlign: "center" }}>加载费用数据…</div>
            )}
          </div>

          {/* 电子回单与签收 */}
          <div className="panel">
            <div className="panel-head">电子回单与签收</div>
            <div style={{ padding: "16px 20px", display: "flex", flexDirection: "column", gap: 14 }}>
              <div style={{ background: "rgba(0,0,0,0.02)", padding: 16, borderRadius: 8, border: "1px dashed var(--line-strong)" }}>
                <span className="muted small" style={{ display: "block", marginBottom: 8, fontWeight: "bold" }}>上传回单照片</span>
                <input type="file" ref={fileInput} onChange={(e) => { const f = e.target.files?.[0]; if (f) upload.mutate(f); }} />
                {upload.isPending && <span className="muted small" style={{ color: "var(--brand)" }}> 上传中…</span>}
              </div>

              {(receipts.data?.items ?? []).length === 0 ? (
                <div className="muted small" style={{ textAlign: "center", padding: "10px 0" }}>暂无电子回单</div>
              ) : (
                <div className="stack" style={{ gap: 8 }}>
                  {(receipts.data?.items ?? []).map((r) => (
                    <div key={r.id} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: 12, background: "#fff", border: "1px solid var(--line)", borderRadius: 8 }}>
                      <div className="stack" style={{ gap: 4 }}>
                        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                          <span style={{ fontWeight: "bold", fontSize: 13 }}>回单</span>
                          <span className={`tag tag-${r.ocr_status === "done" ? "low" : "medium"}`} style={{ fontSize: 10 }}>OCR {r.ocr_status === "done" ? "识别完成" : "提取中"}</span>
                        </div>
                        {r.signatory && (
                          <div style={{ fontSize: 12, color: "var(--brand)" }}>
                            <strong>签收人：</strong> {r.signatory}
                          </div>
                        )}
                        <a href={r.file_url} target="_blank" className="link small">查看原件</a>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
            
            {/* 手写签收 */}
            {(w.status === "in_transit" || w.status === "arrived") && (
              <div style={{ borderTop: "1px solid var(--line)", padding: "16px 20px", background: "rgba(0,0,0,0.01)" }}>
                <div className="muted small" style={{ marginBottom: 8, fontWeight: "bold" }}>现场签收</div>
                <input className="search" style={{ width: "100%", marginBottom: 10 }} placeholder="输入实际提货/签收人姓名" value={signatory} onChange={(e) => setSignatory(e.target.value)} />
                <div style={{ background: "#fff", borderRadius: 8, border: "1px dashed var(--line)", overflow: "hidden" }}>
                  <SignaturePad onChange={setSignature} />
                </div>
                <button className="btn-primary" style={{ width: "100%", marginTop: 12 }} disabled={!signatory || sign.isPending} onClick={() => sign.mutate()}>
                  {sign.isPending ? "落库中…" : "提交并完结运单"}
                </button>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
