import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { apiGet, apiPost, apiUpload } from "../api/client";
import { toast } from "../api/toast";
import { STATUS_LABEL, type CostSummary, type ExceptionRecord, type Paginated, type Receipt, type WaybillDetail } from "../api/types";
import { SignaturePad } from "../components/SignaturePad";
import { TrajectoryMap, type Trajectory } from "../components/TrajectoryMap";

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

  if (detail.isLoading) return <div className="muted">加载中…</div>;
  if (detail.isError || !detail.data) return <div className="muted">运单不存在或无权访问。</div>;
  const w = detail.data;

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
              <div><span>车牌</span>{w.vehicle_plate || "-"}</div>
              <div><span>司机</span>{w.driver_name || "-"}</div>
              <div><span>件数</span>{w.cargo.quantity}</div>
              <div><span>重量(吨)</span>{w.cargo.weight_ton}</div>
              <div><span>体积(方)</span>{w.cargo.volume_cbm}</div>
              <div><span>ETA偏移(分)</span>{w.eta_drift_minutes}</div>
            </div>
          </div>

          <div className="panel">
            <div className="panel-head">费用与毛利</div>
            {costs.data ? (
              <div className="kv">
                <div><span>应收</span>{costs.data.receivables.reduce((s, r) => s + r.amount, 0)}</div>
                <div><span>应付</span>{costs.data.payables.reduce((s, r) => s + r.amount, 0)}</div>
                <div><span>毛利</span>{costs.data.gross_profit}</div>
                <div><span>毛利率</span>{(costs.data.gross_margin * 100).toFixed(1)}%</div>
              </div>
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
