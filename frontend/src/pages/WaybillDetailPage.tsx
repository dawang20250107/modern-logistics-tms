import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { apiGet, apiPost, apiUpload } from "../api/client";
import { hasPerm, useAuth } from "../auth/auth";
import { fmtDateTime, fmtMoney, EMPTY } from "../api/format";
import { toast } from "../api/toast";
import { COD_STATUS_LABEL, OCR_STATUS_LABEL, REIMB_CATEGORY_LABEL, STATUS_LABEL, type Contract, type CostCatalog, type CostSummary, type DriverCollection, type DriverReminder, type ExceptionRecord, type Paginated, type Reimbursement, type ReminderTemplate, type Receipt, type WaybillDetail } from "../api/types";
import { SignaturePad } from "../components/SignaturePad";
import { CopyCode } from "../components/CopyCode";
import { ExceptionCloseLoop } from "../components/ExceptionCloseLoop";
import { StateView } from "../components/StateView";
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
  { status: "draft", label: "草稿", icon: "" },
  { status: "pending_dispatch", label: "待调度", icon: "" },
  { status: "dispatching", label: "派单中", icon: "" },
  { status: "dispatched", label: "已派发", icon: "" },
  { status: "departed", label: "发车", icon: "" },
  { status: "in_transit", label: "在途", icon: "" },
  { status: "arrived", label: "到达", icon: "" },
  { status: "signed", label: "签收", icon: "" },
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

  // 这一页上原先**一个权限判断都没有**：拿只有 waybill.view 的演示客服
  // 打开一张它范围内的运单，看到的按钮和超管一模一样——
  // 「已送达」「发送提醒」「生成费用」「保存」「提交报销」「生成合同」全在。
  // 后端每一条都挂了闸（resolve(w, r, "waybill.manage") 之类），
  // 所以点下去只是弹一句"无权限"；但对操作的人来说，
  // 一屏可点却点不动的按钮就是"系统坏了"。
  //
  // 三档：运单动作要 waybill.manage、动钱的要 finance.manage、
  // AI 分析要 ai.use。「紧急上报」刻意不设限——上报异常只要 waybill.view。
  const { user } = useAuth();
  const canManage = hasPerm(user, "waybill.manage");
  const canFinance = hasPerm(user, "finance.manage");
  const canAI = hasPerm(user, "ai.use");

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
  const hasCosts = ((costs.data?.payables?.length ?? 0) + (costs.data?.receivables?.length ?? 0)) > 0;
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
      toast.success(action === "collect-cod" ? "已确认代收货款" : "已确认回款给客户");
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

  // 报销**列表**属于财务域（ReimbursementsCfg 的 ReadPerm 是 finance.view），
  // 而**提交**报销是运单动作（要 waybill.manage）。两者权限点不同，
  // 于是调度员的处境是：能提交、看不到列表。
  // 原先照查不误——调度员一打开运单详情就吃一个 403，页面上什么都不说，
  // 只是那块永远显示「暂无报销单」，看起来像提交没成功。
  // 现在没有 finance.view 就不查，并在那一块明说这是权限而不是没有数据。
  const canViewFinance = hasPerm(user, "finance.view");
  const reimbursements = useQuery({
    queryKey: ["waybill", no, "reimbursements"],
    queryFn: () => apiGet<Paginated<Reimbursement>>(`/finance/reimbursements?waybill=${detail.data?.id}&page_size=50`),
    enabled: Boolean(detail.data?.id) && canViewFinance,
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

  if (detail.isLoading) return <StateView kind="loading" />;
  if (detail.isError || !detail.data) return <StateView kind="error" title="运单无法打开" hint="运单不存在、无权访问或数据暂时不可用。" error={detail.error} onRetry={() => detail.refetch()} />;
  
  const w = detail.data;
  const c = contract.data ?? null;
  // editable 原先只看运单状态。手工补录费用（add-expense）后端要 waybill.manage，
  // 所以还要看权限——否则只读角色能看到整个录入表单，填完点保存才被拒。
  const editable = canManage && !["settled", "cancelled", "voided"].includes(w.status);
  
  const currentStepIdx = WORKFLOW_STEPS.findIndex(s => s.status === w.status);

  return (
    <div className="stack" style={{ gap: 16 }}>
      {/* 运单头部 */}
      <div className="panel" style={{ overflow: "visible" }}>
        <div className="waybill-hero on-dark" style={{ background: "var(--hero-grad)", color: "var(--hero-ink)", padding: "12px 14px" }}>
          <div className="stack waybill-hero-info" style={{ gap: 6 }}>
            <div className="waybill-hero-title">
              <span className="mono" style={{ fontSize: 19, fontWeight: 700, letterSpacing: "-0.02em" }}><CopyCode value={w.waybill_no} /></span>
              <span className="tag" style={{ background: "var(--hero-line)", color: "var(--hero-ink)", border: "1px solid var(--hero-line)", fontWeight: 500 }}>
                {STATUS_LABEL[w.status] ?? w.status}
              </span>
              {w.receipt_status === "returned" && <span className="tag tag-low">回单已核验</span>}
            </div>
            <div className="waybill-hero-meta" style={{ color: "var(--hero-sub)", fontSize: 13, fontWeight: 400 }}>
              <span>{w.route_name} ({w.origin} → {w.destination})</span>
              <span>{w.customer_name || "散客"}</span>
              <span>{w.vehicle_plate || "自营/待指派"}</span>
            </div>
          </div>

          <div className="stack waybill-hero-actions" style={{ gap: 8 }}>
            <span className={`tag tag-dot tag-${w.risk_level === 'high' ? 'high' : w.risk_level === 'medium' ? 'medium' : 'low'}`}>
              风险 {RISK_LABEL[w.risk_level]}
            </span>
            <div className="row-actions waybill-hero-buttons">
              {canAI && <button className="btn-ghost" disabled={analyze.isPending} onClick={() => analyze.mutate()}>
                风险分析
              </button>}
              {canManage && w.next_statuses.map((s) => (
                <button
                  key={s}
                  className="btn-primary"
                  style={{ background: "var(--panel)", color: "var(--ink)", borderColor: "var(--panel)" }}
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

      <div className="ct-grid detail-split">
        {/* 左侧：在途与运营 */}
        <div className="stack">
          {/* ETA 预测 */}
          {eta.data?.predicted && (
            <div className="panel">
              <div className="panel-head">
                ETA 预测
              </div>
              <div className="kv" style={{ padding: "12px 16px" }}>
                <div><span>预计到达</span><b>{eta.data.estimated_arrival ? fmtDateTime(eta.data.estimated_arrival) : "—"}</b></div>
                <div><span>剩余里程</span><b>{eta.data.remaining_km ?? "—"} 公里</b></div>
                <div><span>当前均速</span><b>{eta.data.avg_speed_kmh ?? "—"} 公里/时</b></div>
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
              <StateView kind="empty" title="暂无轨迹数据" hint="车辆定位上报后将在此显示轨迹。" compact />
            )}
            
            {w.stops && w.stops.length > 0 && (
              <div className="table-wrap"><table className="table" style={{ borderTop: "1px solid var(--line)" }}>
                <thead><tr style={{ background: "var(--panel-2)" }}><th>提/送类型</th><th>地理围栏地址</th><th>计划到达时间</th><th>打卡确认</th><th>围栏操作</th></tr></thead>
                <tbody>
                  {w.stops.map((s) => (
                    <tr key={s.id}>
                      <td style={{ fontWeight: "bold", color: "var(--ink-2)" }}>{s.stop_type_label}</td>
                      <td className="small">{s.address || s.city || EMPTY}</td>
                      <td className="small mono" style={{ color: "var(--brand)" }}>{fmt(s.planned_eta)}</td>
                      <td className="small">
                        {s.actual_arrival_at ? <span style={{ color: "var(--green)", fontWeight: "bold" }}>✓ {fmt(s.actual_arrival_at)}</span> : <span className="muted">未到达</span>}
                      </td>
                      <td>
                        {canManage && !s.actual_arrival_at && (
                          <button className="btn-ghost small" disabled={stopEvent.isPending} onClick={() => stopEvent.mutate({ seq: s.seq, event: "arrived" })}>人工到站</button>
                        )}
                        {canManage && s.actual_arrival_at && !s.actual_depart_at && (
                          <button className="btn-ghost small" disabled={stopEvent.isPending} onClick={() => stopEvent.mutate({ seq: s.seq, event: "departed" })}>发车放行</button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table></div>
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
              <input className="search" style={{ flex: 1, minWidth: 200, background: "var(--panel)" }} placeholder="异常描述（如：高速拥堵预计延误2小时）" value={excDesc} onChange={(e) => setExcDesc(e.target.value)} />
              <button className="btn-danger" disabled={reportExc.isPending || !excDesc.trim()} onClick={() => reportExc.mutate()}>紧急上报</button>
            </div>
            {/* 原先这里是一张只读表，状态那格链到 /dispatch-board——
                而调度台上并没有处理异常的地方，那个链接把人送去一个空手而归的页面。
                异常的后半截（指派 / 处理 / 定责关闭）后端四个端点都是全的，
                界面上一个入口都没有：上报的异常永远停在「待处理」，
                各页那个「⚠ 异常」角标永远不消，赔付也永远进不了账。 */}
            <ExceptionCloseLoop
              items={exceptions.data?.items ?? []}
              onChanged={() => {
                queryClient.invalidateQueries({ queryKey: ["waybill", no, "exceptions"] });
                queryClient.invalidateQueries({ queryKey: ["waybill", no, "costs"] });
              }}
            />
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
                {canManage && <button className="btn-primary" disabled={sendReminder.isPending || !rmContent.trim()} onClick={() => sendReminder.mutate()}>发送提醒</button>}
              </div>
              <textarea className="search" style={{ width: "100%", minHeight: 70 }} placeholder="提醒下发内容（支持多行）" value={rmContent} onChange={(e) => setRmContent(e.target.value)} />
              {(reminders.data?.length ?? 0) > 0 && (
                <div className="table-wrap"><table className="table">
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
                </table></div>
              )}
            </div>
          </div>
        </div>

        {/* 右侧：财务与签收 */}
        <div className="stack">
          {/* AI 建议 */}
          <div className="panel">
            <div className="panel-head">AI 建议</div>
            {w.agent_suggestions.length === 0 ? (
              <StateView kind="empty" title="暂无建议" compact />
            ) : (
              <ul className="suggestions" style={{ padding: "12px 18px" }}>
                {w.agent_suggestions.map((s) => (
                  <li key={s.id} style={{ background: "var(--panel)", borderColor: "rgba(139,92,246,0.2)" }}>
                    <div className="sg-title" style={{ color: "var(--ink)" }}>{s.title}</div>
                    <div className="muted small">{s.body}</div>
                    <div className="sg-actions">
                      <span className={`tag tag-${s.status === "accepted" ? "low" : s.status === "rejected" ? "none" : "medium"}`}>
                        {s.status === "pending" ? "等待审批" : s.status === "accepted" ? "已采纳执行" : "已驳回"}
                      </span>
                      {canManage && s.status === "pending" && (
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
                    到付：司机送达时需向收货人收取运费 {fmtMoney(collection.data.collect_freight)}
                  </div>
                )}
                {Number(detail.data.cod_amount) > 0 && (
                  <div style={{ background: "var(--amber-weak)", border: "1px solid var(--amber-line)", borderRadius: 8, padding: 12 }}>
                    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <div>
                        <div style={{ fontWeight: "bold" }}>代收货款 COD {fmtMoney(detail.data.cod_amount)}</div>
                        <div className="muted small">状态：{COD_STATUS_LABEL[detail.data.cod_status] ?? detail.data.cod_status}
                          {collection.data ? ` · 司机应收合计 ${fmtMoney(collection.data.total_to_collect)}` : ""}</div>
                      </div>
                      <div style={{ display: "flex", gap: 8 }}>
                        {canManage && detail.data.cod_status === "pending" && (
                          <button className="btn-primary" style={{ padding: "4px 12px", fontSize: 12 }} disabled={codAction.isPending} onClick={() => codAction.mutate("collect-cod")}>司机确认代收</button>
                        )}
                        {canManage && detail.data.cod_status === "collected" && (
                          <button className="btn-primary" style={{ padding: "4px 12px", fontSize: 12 }} disabled={codAction.isPending} onClick={() => codAction.mutate("remit-cod")}>财务确认回款</button>
                        )}
                        {detail.data.cod_status === "remitted" && <span className="tag tag-low">已回款客户</span>}
                      </div>
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* 费用台账 */}
          <div className="panel">
            <div className="panel-head">
              费用台账
              {/* 一张新运单的费用台账是空的，要按这颗按钮才按合同价生成。
                  按钮原先一律写「重新生成」——第一次生成时那三个字是错的，
                  而正在找"费用怎么出来"的人也不会认得它。
                  实测走完整条验收链：建单→派单→签收之后费用记录 0 条，
                  对账单归集出来是 ¥0，看起来像对账坏了。 */}
              {canFinance && <button className="btn-ghost" style={{ fontSize: 11, padding: "4px 8px" }}
                      disabled={genCosts.isPending} onClick={() => genCosts.mutate()}
                      title={hasCosts ? "按当前合同价重算这张运单的应收应付" : "按合同价生成这张运单的应收应付"}>
                {genCosts.isPending ? "生成中…" : hasCosts ? "重新生成" : "生成费用"}
              </button>}
            </div>
            {costs.data ? (
              <>
                <div className="money-pair">
                  <div className="money-cell money-cell-ar">
                    <div className="money-label">向客户应收 (AR)</div>
                    <div className="money-value">{fmtMoney(costs.data.receivable_total)}</div>
                  </div>
                  <div className="money-cell money-cell-ap">
                    <div className="money-label">付承运商成本 (AP)</div>
                    <div className="money-value">{fmtMoney(costs.data.payable_total)}</div>
                  </div>
                </div>
                <div className="kv" style={{ paddingTop: 0, paddingBottom: 10 }}>
                  <div><span>账面毛利预估</span><b className={`money-value${Number(costs.data.gross_profit) < 0 ? " neg" : ""}`} style={{ fontSize: 15 }}>{fmtMoney(costs.data.gross_profit)}</b></div>
                  <div><span>毛利率测算</span><b>{(costs.data.gross_margin * 100).toFixed(1)}%</b></div>
                </div>
                
                {(costs.data.payables.length > 0 || costs.data.receivables.length > 0) && (
                  <div className="table-wrap"><table className="table" style={{ fontSize: 12 }}>
                    <thead><tr style={{ background: "var(--panel-2)" }}><th>借贷</th><th>科目名</th><th>落账金额</th><th>业务主体</th></tr></thead>
                    <tbody>
                      {[...costs.data.receivables, ...costs.data.payables].map((e) => (
                        <tr key={e.id}>
                          <td><span className={`tag${e.direction === "receivable" ? " tag-low" : " tag-high"}`}>{e.direction === "receivable" ? "应收" : "应付"}</span></td>
                          <td><strong>{e.item_label}</strong></td>
                          <td className="mono num" style={{ fontWeight: "bold" }}>{fmtMoney(e.amount)}</td>
                          <td className="muted">{e.payee_label}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table></div>
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

          {/* 司机报销。
              和承运合同同一个毛病：submitReimb / reimbAction 两个 mutation
              连同表单状态（bxCat / bxAmount / bxReason）都写好了，
              但没有任何地方把它们渲染出来——后端的提交/审批/驳回/付款四条
              路由一直是全的，只是界面上够不着。
              过路费、油费这些是司机先垫的钱，没有入口就意味着他垫的钱
              进不了系统，只能线下要——而工作流面板上还有一格「报销」在等它。 */}
          <div className="panel">
            <div className="panel-head">司机报销</div>
            <div style={{ padding: "16px 20px", display: "flex", flexDirection: "column", gap: 12 }}>
              {/* 提交报销要 waybill.manage（ReimbursementCreate）。
                  只读角色看得到整张表单、填完点提交才被拒——
                  而报销单要填类别、金额、事由，白填一遍很难受。 */}
              {canManage && <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
                <select className="search" style={{ minWidth: 110 }} value={bxCat} onChange={(e) => setBxCat(e.target.value)}>
                  {Object.entries(REIMB_CATEGORY_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                </select>
                <input className="search" style={{ width: 110 }} inputMode="decimal" placeholder="金额" value={bxAmount} onChange={(e) => setBxAmount(e.target.value)} />
                <input className="search" style={{ flex: 1, minWidth: 160 }} placeholder="事由（选填）" value={bxReason} onChange={(e) => setBxReason(e.target.value)} />
                <button
                  className="btn-primary small"
                  disabled={submitReimb.isPending || !(Number(bxAmount) > 0)}
                  onClick={() => submitReimb.mutate()}
                >
                  {submitReimb.isPending ? "提交中…" : "提交报销"}
                </button>
              </div>}
              {!canViewFinance ? (
                <StateView kind="empty" title="报销记录需要财务查看权限"
                  hint="你提交的报销会进入财务的待审批列表；要在这里看到它们的状态，需要 finance.view 权限点。" compact />
              ) : (reimbursements.data?.items ?? []).length === 0 ? (
                <StateView kind="empty" title="暂无报销单" hint="过路费、油费等司机垫付的费用在此提交与审批。" compact />
              ) : (
                <div className="table-wrap">
                  <table className="table">
                    <thead><tr><th>单号</th><th>类别</th><th>金额</th><th>事由</th><th>提交人</th><th>状态</th><th>操作</th></tr></thead>
                    <tbody>
                      {(reimbursements.data?.items ?? []).map((b) => (
                        <tr key={b.id}>
                          <td className="small">{b.reimb_no}</td>
                          <td>{REIMB_CATEGORY_LABEL[b.category] ?? b.category_label ?? b.category}</td>
                          <td>{fmtMoney(b.amount)}</td>
                          <td className="small">{b.reason || EMPTY}</td>
                          <td className="small">{b.submitted_by_name || EMPTY}</td>
                          <td><span className={`tag tag-${b.status === "paid" ? "low" : b.status === "rejected" ? "high" : "medium"}`}>{b.status_label || b.status}</span></td>
                          <td style={{ display: "flex", gap: 6 }}>
                            {canFinance && b.status === "submitted" && (
                              <>
                                <button className="btn-ghost small" disabled={reimbAction.isPending} onClick={() => reimbAction.mutate({ id: b.id, action: "approve" })}>审批</button>
                                <button className="btn-ghost small" disabled={reimbAction.isPending} onClick={() => reimbAction.mutate({ id: b.id, action: "reject" })}>驳回</button>
                              </>
                            )}
                            {canFinance && b.status === "approved" && (
                              <button className="btn-ghost small" disabled={reimbAction.isPending} onClick={() => reimbAction.mutate({ id: b.id, action: "pay" })}>标记已付</button>
                            )}
                            {!["submitted", "approved"].includes(b.status) && <span className="small muted">{EMPTY}</span>}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </div>

          {/* 承运合同。
              这三个 mutation（genContract / sendContract / confirmContract）
              早就写在这个文件里，但一直没有任何地方把它们渲染出来——
              而后端那条路由此前也只有 GET。两边各缺一半，
              于是「承运合同」这一整段功能从界面上完全够不着：
              工作流面板上「承运合同 未生成」会一直挂着，没有任何东西能改变它。
              合同是承运责任、运费金额、异常责任的书面依据，
              司机跑了一趟没有合同，出事时双方各说各话。 */}
          <div className="panel">
            <div className="panel-head">承运合同</div>
            <div style={{ padding: "16px 20px", display: "flex", flexDirection: "column", gap: 12 }}>
              {c ? (
                <>
                  <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                    <span style={{ fontWeight: "bold", fontSize: 13 }}>{c.contract_no}</span>
                    <span className={`tag tag-${c.confirm_status === "confirmed" ? "low" : c.confirm_status === "rejected" ? "high" : "medium"}`}>
                      {c.status_label}
                    </span>
                    <span className="small muted">承运司机 {c.driver_name || EMPTY}</span>
                    {c.sent_at && <span className="small muted">发送于 {fmtDateTime(c.sent_at)}</span>}
                    {c.confirmed_at && <span className="small muted">确认于 {fmtDateTime(c.confirmed_at)}</span>}
                  </div>
                  {c.driver_reply && <div className="small">司机回复：{c.driver_reply}</div>}
                  <details>
                    <summary className="linkish small">查看合同正文</summary>
                    <pre style={{ whiteSpace: "pre-wrap", fontSize: 12, background: "var(--panel-2)", padding: 12, borderRadius: "var(--radius)", border: "1px solid var(--line)", marginTop: 8 }}>
                      {c.content}
                    </pre>
                  </details>
                  <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                    {canManage && c.confirm_status === "pending" && (
                      <button className="btn-primary small" disabled={sendContract.isPending} onClick={() => sendContract.mutate()}>
                        {sendContract.isPending ? "发送中…" : "发送给司机"}
                      </button>
                    )}
                    {canManage && c.confirm_status === "sent" && (
                      <>
                        <button className="btn-primary small" disabled={confirmContract.isPending} onClick={() => confirmContract.mutate(true)}>司机已确认</button>
                        <button className="btn-ghost small" disabled={confirmContract.isPending} onClick={() => confirmContract.mutate(false)}>司机拒签</button>
                      </>
                    )}
                    {canManage && c.confirm_status !== "confirmed" && (
                      <button className="btn-ghost small" disabled={genContract.isPending} onClick={() => genContract.mutate()}>
                        {genContract.isPending ? "生成中…" : "重新生成"}
                      </button>
                    )}
                    {c.pdf_url && <a className="btn-ghost small" href={c.pdf_url} target="_blank" rel="noreferrer">下载 PDF</a>}
                  </div>
                </>
              ) : (
                <>
                  <StateView kind="empty" title="尚未生成承运合同" hint="合同在派单指派司机时自动生成；派单时未指派司机的运单可在此补出。" compact />
                  {canManage && <div>
                    <button className="btn-primary small" disabled={genContract.isPending || !w.driver_name} onClick={() => genContract.mutate()}>
                      {genContract.isPending ? "生成中…" : "生成合同"}
                    </button>
                    {!w.driver_name && <span className="small muted" style={{ marginLeft: 8 }}>该运单还没有指派司机，先派司机才能出合同。</span>}
                  </div>}
                </>
              )}
            </div>
          </div>

          {/* 电子回单与签收 */}
          <div className="panel">
            <div className="panel-head">电子回单与签收</div>
            <div style={{ padding: "16px 20px", display: "flex", flexDirection: "column", gap: 14 }}>
              {canManage && <div style={{ background: "var(--panel-2)", padding: 12, borderRadius: "var(--radius)", border: "1px dashed var(--line-2)", display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                <span className="section-label" style={{ marginBottom: 0 }}>上传回单照片</span>
                <label className="btn-ghost small file-trigger" style={{ cursor: "pointer" }}>
                  {upload.isPending ? "上传中…" : "选择图片"}
                  <input className="file-input-accessible" type="file" accept="image/*" ref={fileInput} disabled={upload.isPending} onChange={(e) => { const f = e.target.files?.[0]; if (f) upload.mutate(f); e.target.value = ""; }} />
                </label>
              </div>}

              {(receipts.data?.items ?? []).length === 0 ? (
                <StateView kind="empty" title="暂无电子回单" hint="司机上传回单后在此查看。" compact />
              ) : (
                <div className="stack" style={{ gap: 8 }}>
                  {(receipts.data?.items ?? []).map((r) => (
                    <div key={r.id} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: 12, background: "var(--panel)", border: "1px solid var(--line)", borderRadius: 8 }}>
                      <div className="stack" style={{ gap: 4 }}>
                        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                          <span style={{ fontWeight: "bold", fontSize: 13 }}>回单</span>
                          {/* 别把「不是 done」一律说成「提取中」。
                              未配 OCR 引擎时后端落的是 manual（待人工），
                              这个徽标会一直显示"提取中"——一件永远不会完成的事，
                              等于告诉用户"再等等"，而其实该他自己去录。
                              failed 同理，卡在"提取中"就没人会去重传。 */}
                          <span
                            className={`tag tag-${r.ocr_status === "done" ? "low" : r.ocr_status === "failed" ? "high" : "medium"}`}
                            style={{ fontSize: 10 }}
                            title={r.ocr_status === "manual" ? "本部署未接入 OCR 引擎，签收信息需人工录入" : undefined}
                          >
                            OCR {OCR_STATUS_LABEL[r.ocr_status] ?? r.ocr_status}
                          </span>
                        </div>
                        {r.signatory && (
                          <div style={{ fontSize: 12, color: "var(--brand)" }}>
                            <strong>签收人：</strong> {r.signatory}
                          </div>
                        )}
                        {/* 这里原先取的是 file_url——那是"外链回单"用的字段。
                            后台上传的回单存的是 file，file_url 是空串，于是
                            href="" 的链接点开是刷新当前页：回单在，看不了。
                            file_display 是后端算好的"能打开的地址"（落盘的带 /media/
                            前缀，外链的回落到 file_url），两种来源都覆盖。 */}
                        {r.file_display ? (
                          <a href={r.file_display} target="_blank" rel="noreferrer" className="link small">查看原件</a>
                        ) : (
                          <span className="small" style={{ color: "var(--muted)" }}>无原件</span>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
            
            {/* 手写签收 */}
            {(w.status === "in_transit" || w.status === "arrived") && (
              <div style={{ borderTop: "1px solid var(--line)", padding: "16px 20px", background: "var(--panel-2)" }}>
                <div className="muted small" style={{ marginBottom: 8, fontWeight: "bold" }}>现场签收</div>
                <input className="search" style={{ width: "100%", marginBottom: 10 }} placeholder="输入实际提货/签收人姓名" value={signatory} onChange={(e) => setSignatory(e.target.value)} />
                <div style={{ background: "var(--panel)", borderRadius: 8, border: "1px dashed var(--line)", overflow: "hidden" }}>
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
