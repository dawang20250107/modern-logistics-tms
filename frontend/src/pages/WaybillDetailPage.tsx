import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { apiGet, apiPost, apiUpload } from "../api/client";
import { toast } from "../api/toast";
import { REIMB_CATEGORY_LABEL, STATUS_LABEL, type Contract, type CostCatalog, type CostSummary, type DriverReminder, type ExceptionRecord, type Paginated, type Reimbursement, type ReminderTemplate, type Receipt, type WaybillDetail } from "../api/types";
import { SignaturePad } from "../components/SignaturePad";
import { TrajectoryMap, type Trajectory } from "../components/TrajectoryMap";

const fmt = (s: string | null) => (s ? new Date(s).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }) : "—");
const RISK_LABEL: Record<string, string> = { high: "高", medium: "中", low: "低", none: "无" };
const EXC_TYPE_LABEL: Record<string, string> = {
  transit_delay: "在途超时", route_deviation: "偏航", cargo_damage: "货损货差",
  vehicle_breakdown: "车辆故障", detained: "扣车扣货", customer_complaint: "客户投诉", other: "其他",
};
const EXC_STATUS_LABEL: Record<string, string> = {
  pending_handle: "待处理", handling: "处理中", pending_audit: "待审核", closed: "已关闭", rejected: "已驳回",
};

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
      toast.success("提醒已下发至司机端");
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

  if (detail.isLoading) return <div className="muted">加载中…</div>;
  if (detail.isError || !detail.data) return <div className="muted">运单不存在或无权访问。</div>;
  const w = detail.data;
  const editable = !["settled", "cancelled", "voided"].includes(w.status);

  return (
    <div className="stack">
      <div className="panel">
        <div className="wb-head">
          <div>
            <Link to="/waybills" className="muted small">← 返回台账</Link>
            <div className="wb-no mono">{w.waybill_no}</div>
            <div className="muted">{w.route_name}（{w.origin} → {w.destination}）</div>
          </div>
          <div className="wb-status">
            <span className={`tag tag-${w.risk_level}`}>风险 {RISK_LABEL[w.risk_level]}</span>
            <span className="status-pill">{STATUS_LABEL[w.status] ?? w.status}</span>
          </div>
        </div>
        <div className="wb-actions">
          {w.next_statuses.length === 0 ? (
            <span className="muted small">无可用流转</span>
          ) : (
            w.next_statuses.map((s) => (
              <button
                key={s}
                className="btn-primary"
                disabled={transition.isPending}
                onClick={() => transition.mutate(s)}
              >
                → {STATUS_LABEL[s] ?? s}
              </button>
            ))
          )}
          <button className="btn-ghost" disabled={analyze.isPending} onClick={() => analyze.mutate()}>
            AI 风险分析
          </button>
          <button className="btn-ghost" disabled={genCosts.isPending} onClick={() => genCosts.mutate()}>
            生成应收应付
          </button>
        </div>
      </div>

      {w.stops && w.stops.length > 0 && (
        <div className="panel">
          <div className="panel-head">点位时效 · 计划 vs 实际（GPS 围栏自动盖戳）</div>
          <table className="table">
            <thead><tr><th>序</th><th>类型</th><th>地址</th><th>计划ETA</th><th>实际到达</th><th>实际离开</th><th>来源</th><th>状态</th><th></th></tr></thead>
            <tbody>
              {w.stops.map((s) => (
                <tr key={s.id}>
                  <td>{s.seq}</td>
                  <td>{s.stop_type_label}</td>
                  <td className="small">{s.address || s.city || "-"}</td>
                  <td className="small">{fmt(s.planned_eta)}</td>
                  <td className="small">{s.actual_arrival_at ? <b>{fmt(s.actual_arrival_at)}</b> : "—"}</td>
                  <td className="small">{fmt(s.actual_depart_at)}</td>
                  <td className="small">{s.arrival_source === "gps" ? "GPS围栏" : s.arrival_source === "manual" ? "手动" : "-"}</td>
                  <td><span className={`tag${s.status === "arrived" ? " tag-high" : s.status === "departed" ? " tag-low" : ""}`}>{s.status_label}</span></td>
                  <td>
                    {!s.actual_arrival_at && (
                      <button className="btn-ghost" disabled={stopEvent.isPending} onClick={() => stopEvent.mutate({ seq: s.seq, event: "arrived" })}>到达</button>
                    )}
                    {s.actual_arrival_at && !s.actual_depart_at && (
                      <button className="btn-ghost" disabled={stopEvent.isPending} onClick={() => stopEvent.mutate({ seq: s.seq, event: "departed" })}>离开</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="panel">
        <div className="panel-head">轨迹回放</div>
        {traj.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载轨迹…</div>
        ) : traj.data ? (
          <TrajectoryMap traj={traj.data} />
        ) : (
          <div className="muted small" style={{ padding: 16 }}>暂无轨迹数据。</div>
        )}
      </div>

      {(w.status === "in_transit" || w.status === "arrived") && (
        <div className="panel">
          <div className="panel-head">签收回传 · e-POD</div>
          <div style={{ padding: 16 }} className="stack">
            <input className="search" placeholder="签收人姓名" value={signatory} onChange={(e) => setSignatory(e.target.value)} style={{ maxWidth: 240 }} />
            <div>
              <div className="muted small" style={{ marginBottom: 6 }}>电子签名（手写）</div>
              <SignaturePad onChange={setSignature} />
            </div>
            <button className="btn-primary" style={{ width: 160 }} disabled={!signatory || sign.isPending} onClick={() => sign.mutate()}>
              {sign.isPending ? "提交中…" : "确认签收"}
            </button>
          </div>
        </div>
      )}

      <div className="panel">
        <div className="panel-head">内部报销 · 下游付款</div>
        <div style={{ padding: 16 }} className="stack">
          <div className="form-row" style={{ gap: 8, flexWrap: "wrap", padding: 0 }}>
            <select value={bxCat} onChange={(e) => setBxCat(e.target.value)}>
              {Object.entries(REIMB_CATEGORY_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
            <input className="search" style={{ width: 90 }} placeholder="金额" value={bxAmount} onChange={(e) => setBxAmount(e.target.value)} />
            <input className="search" style={{ flex: 1, minWidth: 140 }} placeholder="事由" value={bxReason} onChange={(e) => setBxReason(e.target.value)} />
            <button className="btn-primary" disabled={submitReimb.isPending || !bxAmount} onClick={() => submitReimb.mutate()}>提交报销</button>
          </div>
          {(reimbursements.data?.items?.length ?? 0) > 0 && (
            <table className="table">
              <thead><tr><th>单号</th><th>类别</th><th>金额</th><th>事由</th><th>状态</th><th></th></tr></thead>
              <tbody>
                {(reimbursements.data?.items ?? []).map((b) => (
                  <tr key={b.id}>
                    <td className="mono small">{b.reimb_no}</td>
                    <td className="small">{b.category_label}</td>
                    <td className="small">¥{b.amount}</td>
                    <td className="small">{b.reason || "-"}</td>
                    <td><span className={`tag${b.status === "paid" ? " tag-low" : b.status === "rejected" ? " tag-high" : ""}`}>{b.status_label}</span></td>
                    <td className="small">
                      {b.status === "submitted" && (
                        <>
                          <button className="link" onClick={() => reimbAction.mutate({ id: b.id, action: "approve" })}>审批</button>
                          {" · "}
                          <button className="link" onClick={() => reimbAction.mutate({ id: b.id, action: "reject" })}>驳回</button>
                        </>
                      )}
                      {b.status === "approved" && <button className="link" onClick={() => reimbAction.mutate({ id: b.id, action: "pay" })}>付款</button>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">作业提醒 · 富文本回复库（司机端强制确认）</div>
        <div style={{ padding: 16 }} className="stack">
          <div className="form-row" style={{ gap: 8, flexWrap: "wrap", padding: 0 }}>
            <select value={rmTpl} onChange={(e) => { setRmTpl(e.target.value); const t = (reminderTpls.data?.items ?? []).find((x) => x.id === e.target.value); if (t) setRmContent(t.content); }}>
              <option value="">选择模板…</option>
              {(reminderTpls.data?.items ?? []).map((t) => <option key={t.id} value={t.id}>{t.category ? `[${t.category}] ` : ""}{t.name}</option>)}
            </select>
            <label className="small" style={{ display: "flex", alignItems: "center", gap: 4 }}>
              <input type="checkbox" checked={rmAck} onChange={(e) => setRmAck(e.target.checked)} />强制确认
            </label>
            <span style={{ flex: 1 }} />
            <button className="btn-primary" disabled={sendReminder.isPending || !rmContent.trim()} onClick={() => sendReminder.mutate()}>下发提醒</button>
          </div>
          <textarea className="search" style={{ width: "100%", minHeight: 90 }} placeholder="提醒内容（可选模板后编辑）" value={rmContent} onChange={(e) => setRmContent(e.target.value)} />
          {(reminders.data?.length ?? 0) > 0 && (
            <table className="table">
              <thead><tr><th>标题</th><th>强制</th><th>下发</th><th>状态</th></tr></thead>
              <tbody>
                {(reminders.data ?? []).map((r) => (
                  <tr key={r.id}>
                    <td className="small">{r.title}</td>
                    <td className="small">{r.ack_required ? "是" : "否"}</td>
                    <td className="small">{fmt(r.sent_at)}</td>
                    <td><span className={`tag${r.status === "acknowledged" ? " tag-low" : " tag-high"}`}>{r.status === "acknowledged" ? `已确认 ${fmt(r.acknowledged_at)}` : "待确认"}</span></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">
          承运合同 · 合同库
          {!contract.data && <button className="btn-primary" disabled={genContract.isPending} onClick={() => genContract.mutate()}>生成合同</button>}
        </div>
        {contract.data ? (
          <div style={{ padding: 16 }} className="stack">
            <div className="form-row" style={{ gap: 12, alignItems: "center", padding: 0, flexWrap: "wrap" }}>
              <span className="mono">{contract.data.contract_no}</span>
              <span className={`tag${contract.data.confirm_status === "confirmed" ? " tag-low" : contract.data.confirm_status === "rejected" ? " tag-high" : ""}`}>{contract.data.status_label}</span>
              {contract.data.sent_at && <span className="muted small">发送 {fmt(contract.data.sent_at)}</span>}
              {contract.data.driver_reply && <span className="muted small">司机回复：{contract.data.driver_reply}</span>}
              <span style={{ flex: 1 }} />
              {contract.data.pdf_url && <a className="link small" href={contract.data.pdf_url} target="_blank" rel="noreferrer">查看PDF</a>}
              {contract.data.confirm_status === "pending" && <button className="btn-ghost" disabled={sendContract.isPending} onClick={() => sendContract.mutate()}>发送给司机</button>}
              {contract.data.confirm_status === "sent" && (
                <>
                  <button className="btn-primary" disabled={confirmContract.isPending} onClick={() => confirmContract.mutate(true)}>司机确认</button>
                  <button className="btn-ghost" disabled={confirmContract.isPending} onClick={() => confirmContract.mutate(false)}>拒签</button>
                </>
              )}
            </div>
            <pre className="result-box" style={{ margin: 0, whiteSpace: "pre-wrap", maxHeight: 220 }}>{contract.data.content}</pre>
          </div>
        ) : (
          <div className="muted small" style={{ padding: 16 }}>尚未生成承运合同。点击「生成合同」自动出具含中文 PDF 的承运合同。</div>
        )}
      </div>

      <div className="panel">
        <div className="panel-head">在途异常 · 上报与处理</div>
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
          <select value={excType} onChange={(e) => setExcType(e.target.value)}>
            {Object.entries(EXC_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
          <select value={excLevel} onChange={(e) => setExcLevel(e.target.value)}>
            <option value="low">低</option><option value="medium">中</option><option value="high">高</option>
          </select>
          <input className="search" style={{ flex: 1, minWidth: 200 }} placeholder="异常描述（如：高速拥堵预计延误2小时）" value={excDesc} onChange={(e) => setExcDesc(e.target.value)} />
          <button className="btn-primary" disabled={reportExc.isPending || !excDesc.trim()} onClick={() => reportExc.mutate()}>上报异常</button>
        </div>
        {(exceptions.data?.items?.length ?? 0) > 0 && (
          <table className="table">
            <thead><tr><th>类型</th><th>级别</th><th>描述</th><th>状态</th><th>责任/金额</th></tr></thead>
            <tbody>
              {(exceptions.data?.items ?? []).map((ex) => (
                <tr key={ex.id}>
                  <td>{EXC_TYPE_LABEL[ex.exception_type] ?? ex.exception_type}</td>
                  <td><span className={`tag tag-${ex.level === "high" ? "high" : ex.level === "low" ? "low" : "medium"}`}>{RISK_LABEL[ex.level] ?? ex.level}</span></td>
                  <td className="small">{ex.description || "-"}</td>
                  <td><Link className="link" to="/exceptions">{EXC_STATUS_LABEL[ex.status] ?? ex.status}</Link></td>
                  <td className="small">{ex.responsibility_party || "-"}{Number(ex.amount) > 0 ? ` · ¥${ex.amount}` : ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="wb-grid">
        <div className="panel">
          <div className="panel-head">节点时间线</div>
          <ul className="timeline">
            {w.timeline.map((e) => (
              <li key={e.id}>
                <span className="dot" />
                <div>
                  <div className="tl-type">{e.event_type}</div>
                  <div className="muted small">{new Date(e.event_time).toLocaleString("zh-CN")}</div>
                </div>
              </li>
            ))}
          </ul>
        </div>

        <div className="stack">
          <div className="panel">
            <div className="panel-head">货物 / 资源</div>
            <div className="kv">
              <div><span>客户</span>{w.customer_name || "-"}</div>
              <div><span>承运商</span>{w.carrier_name || "-"}</div>
              <div><span>牵引车牌</span>{w.vehicle_plate || "-"}</div>
              <div><span>挂车牌</span>{w.trailer_plate || "-"}</div>
              <div><span>主驾</span>{w.driver_name ? `${w.driver_name}${w.driver_phone ? ` · ${w.driver_phone}` : ""}` : "-"}</div>
              <div><span>司机关系</span>{w.driver_employment || "-"}</div>
              {w.ai_conversation_id && <div><span>AI会话ID</span><span className="mono small">{w.ai_conversation_id}</span></div>}
              <div><span>件数</span>{w.cargo.quantity}</div>
              <div><span>重量(吨)</span>{w.cargo.weight_ton}</div>
              <div><span>体积(方)</span>{w.cargo.volume_cbm}</div>
              <div><span>ETA偏移(分)</span>{w.eta_drift_minutes}</div>
            </div>
          </div>

          {w.drivers && w.drivers.length > 0 && (
            <div className="panel">
              <div className="panel-head">随车司机 · {w.drivers.length} 人</div>
              <table className="table">
                <thead><tr><th>姓名</th><th>角色</th><th>关系</th><th>电话</th><th>微信</th><th>App</th><th>区间</th></tr></thead>
                <tbody>
                  {w.drivers.map((d) => (
                    <tr key={d.id}>
                      <td>{d.name}</td>
                      <td><span className={`tag${d.role === "main" ? " tag-high" : ""}`}>{d.role_label}</span></td>
                      <td className="small">{d.employment}</td>
                      <td className="small">{d.phone || "-"}</td>
                      <td className="small">{d.wechat || "-"}</td>
                      <td><span className={`tag${d.app_registered ? " tag-low" : ""}`}>{d.app_registered ? "已注册" : "未注册"}</span></td>
                      <td className="small">{d.note || "-"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <div className="panel">
            <div className="panel-head">费用构成与毛利</div>
            {costs.data ? (
              <>
                <div className="kv">
                  <div><span>应收合计</span>¥{costs.data.receivable_total.toFixed(2)}</div>
                  <div><span>应付合计</span>¥{costs.data.payable_total.toFixed(2)}</div>
                  <div><span>毛利</span>¥{costs.data.gross_profit.toFixed(2)}</div>
                  <div><span>毛利率</span>{(costs.data.gross_margin * 100).toFixed(1)}%</div>
                </div>
                {(costs.data.payables.length > 0 || costs.data.receivables.length > 0) && (
                  <table className="table">
                    <thead><tr><th>方向</th><th>科目</th><th>金额</th><th>收/付款方</th></tr></thead>
                    <tbody>
                      {[...costs.data.receivables, ...costs.data.payables].map((e) => (
                        <tr key={e.id}>
                          <td><span className={`tag${e.direction === "receivable" ? " tag-low" : " tag-high"}`}>{e.direction === "receivable" ? "应收" : "应付"}</span></td>
                          <td className="small">{e.item_label}</td>
                          <td className="small">¥{e.amount.toFixed(2)}</td>
                          <td className="small">{e.payee_label}{e.payee_ref ? ` · ${e.payee_ref}` : ""}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
                {costs.data.payables_by_payee.length > 0 && (
                  <div className="muted small" style={{ padding: "0 16px 10px" }}>
                    应付归集：{costs.data.payables_by_payee.map((p) => `${p.payee_label} ¥${p.amount.toFixed(2)}`).join(" · ")}
                  </div>
                )}
                {editable && (
                  <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
                    <select value={exDir} onChange={(e) => { const d = e.target.value as "payable" | "receivable"; setExDir(d); setExItem(d === "payable" ? "TRANSPORT_COST" : "TRANSPORT_INCOME"); setExPayeeType(d === "payable" ? "carrier" : "customer"); }}>
                      <option value="payable">应付</option><option value="receivable">应收</option>
                    </select>
                    <select value={exItem} onChange={(e) => setExItem(e.target.value)}>
                      {Object.entries(exDir === "payable" ? (catalog.data?.cost_items ?? {}) : (catalog.data?.income_items ?? {})).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                    </select>
                    <input className="search" style={{ width: 90 }} placeholder="金额" value={exAmount} onChange={(e) => setExAmount(e.target.value)} />
                    <select value={exPayeeType} onChange={(e) => setExPayeeType(e.target.value)}>
                      {Object.entries(catalog.data?.payees ?? {}).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                    </select>
                    <input className="search" style={{ width: 110 }} placeholder="收/付款方" value={exPayeeRef} onChange={(e) => setExPayeeRef(e.target.value)} />
                    <button className="btn-primary" disabled={addExpense.isPending || !exAmount} onClick={() => addExpense.mutate()}>加明细</button>
                  </div>
                )}
              </>
            ) : (
              <div className="muted small">加载中…</div>
            )}
          </div>

          <div className="panel">
            <div className="panel-head">AI 建议</div>
            {w.agent_suggestions.length === 0 ? (
              <div className="muted small" style={{ padding: "12px 18px" }}>暂无建议</div>
            ) : (
              <ul className="suggestions">
                {w.agent_suggestions.map((s) => (
                  <li key={s.id}>
                    <div className="sg-title">{s.title}</div>
                    <div className="muted small">{s.body}</div>
                    <div className="sg-actions">
                      <span className={`tag tag-${s.status === "accepted" ? "low" : s.status === "rejected" ? "none" : "medium"}`}>
                        {s.status}
                      </span>
                      {s.status === "pending" && (
                        <>
                          <button className="btn-ghost" onClick={() => confirm.mutate({ id: s.id, status: "accepted" })}>
                            采纳
                          </button>
                          <button className="btn-ghost" onClick={() => confirm.mutate({ id: s.id, status: "rejected" })}>
                            驳回
                          </button>
                        </>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="panel">
            <div className="panel-head">回单 / OCR</div>
            <div style={{ padding: "12px 18px" }}>
              <input
                type="file"
                ref={fileInput}
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  if (f) upload.mutate(f);
                }}
              />
              {upload.isPending && <span className="muted small"> 上传中…</span>}
            </div>
            <ul className="suggestions">
              {(receipts.data?.items ?? []).map((r) => (
                <li key={r.id}>
                  <div className="sg-title">{r.receipt_type} · OCR {r.ocr_status}</div>
                  <div className="muted small">
                    {r.file_display || r.file_url || "—"}
                    {r.signatory ? ` · 签收 ${r.signatory}` : ""}
                  </div>
                </li>
              ))}
              {(receipts.data?.items ?? []).length === 0 && (
                <div className="muted small" style={{ padding: "8px 0" }}>暂无回单</div>
              )}
            </ul>
          </div>
        </div>
      </div>
    </div>
  );
}
