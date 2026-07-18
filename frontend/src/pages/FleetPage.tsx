import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiUpload } from "../api/client";
import { toast } from "../api/toast";
import type { CredentialRow, CredSeverity, DriverCredential, DriverLookup, ExpiringCredentials } from "../api/types";
import { CRED_SEVERITY_LABEL, CRED_TYPE_LABEL } from "../api/types";

const SEVERITY_TAG: Record<CredSeverity, string> = {
  expired: "high", critical: "medium", warning: "low",
};

function daysText(d: number): string {
  if (d < 0) return `已逾期 ${-d} 天`;
  if (d === 0) return "今天到期";
  return `剩 ${d} 天`;
}

function CredTable({ title, rows, subjectLabel }: { title: string; rows: CredentialRow[]; subjectLabel: string }) {
  return (
    <div className="panel">
      <div className="panel-head">{title} · {rows.length}</div>
      {rows.length === 0 ? (
        <div className="muted" style={{ padding: 16 }}>无到期证件</div>
      ) : (
        <table className="table">
          <thead>
            <tr><th>{subjectLabel}</th><th>证件</th><th>到期日</th><th>剩余</th><th>状态</th></tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i}>
                <td className="mono">{r.subject}</td>
                <td>{r.credential}</td>
                <td>{r.expiry}</td>
                <td style={r.days_left < 0 ? { color: "var(--red)", fontWeight: 600 } : {}}>{daysText(r.days_left)}</td>
                <td><span className={`tag tag-${SEVERITY_TAG[r.severity]}`}>{CRED_SEVERITY_LABEL[r.severity]}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function CredentialLibrary() {
  const [name, setName] = useState("");
  const [idTail, setIdTail] = useState("");
  const [result, setResult] = useState<DriverLookup | null>(null);
  const [credType, setCredType] = useState("id_card");
  const [side, setSide] = useState("main");

  const lookup = useMutation({
    mutationFn: () => apiGet<DriverLookup>(`/drivers/lookup?name=${encodeURIComponent(name)}&id_tail=${encodeURIComponent(idTail)}`),
    onSuccess: (d) => { setResult(d); if (!d.matched) toast.info("未匹配到司机，请核对姓名与身份证后6位"); },
  });
  const upload = useMutation({
    mutationFn: (file: File) => {
      const fd = new FormData();
      fd.append("driver", result!.driver!.id);
      fd.append("cred_type", credType);
      fd.append("side", side);
      fd.append("self_uploaded", "false");
      fd.append("file", file);
      return apiUpload<DriverCredential>("/driver-credentials", fd);
    },
    onSuccess: () => { toast.success("证件已上传，识别中"); lookup.mutate(); },
  });

  return (
    <div className="panel">
      <div className="panel-head">司机证件库</div>
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
        <input className="search" style={{ width: 130 }} placeholder="司机姓名" value={name} onChange={(e) => setName(e.target.value)} />
        <input className="search" style={{ width: 130 }} placeholder="身份证后6位" value={idTail} onChange={(e) => setIdTail(e.target.value)} />
        <button className="btn-primary" disabled={lookup.isPending || (!name && !idTail)} onClick={() => lookup.mutate()}>带出档案</button>
      </div>
      {result?.matched && result.driver && (
        <div style={{ padding: "0 16px 14px" }} className="stack">
          <div className="muted small">
            {result.driver.name} · {result.driver.phone} · {result.driver.employment_label ?? ""}
          </div>
          <table className="table">
            <thead><tr><th>证件</th><th>面</th><th>持有人/车牌</th><th>证号</th><th>有效期</th><th>识别</th><th>文件</th></tr></thead>
            <tbody>
              {result.credentials.length === 0 && <tr><td colSpan={7} className="muted small">暂无证件，下方上传。</td></tr>}
              {result.credentials.map((c) => (
                <tr key={c.id}>
                  <td>{c.cred_type_label}</td>
                  <td className="small">{c.side_label}</td>
                  <td className="small">{c.holder_name || "-"}</td>
                  <td className="small">{c.cert_no || "-"}</td>
                  <td className="small">{c.expiry_date || "-"}</td>
                  <td><span className={`tag${c.ocr_status === "done" ? " tag-low" : ""}`}>{c.ocr_status}</span></td>
                  <td>{c.file_display ? <a className="link small" href={c.file_display} target="_blank" rel="noreferrer">查看</a> : "-"}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
            <select value={credType} onChange={(e) => setCredType(e.target.value)}>
              {Object.entries(CRED_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
            <select value={side} onChange={(e) => setSide(e.target.value)}>
              <option value="main">主页/正面</option><option value="back">副页/反面</option>
            </select>
            <label className="btn-ghost" style={{ cursor: "pointer" }}>
              {upload.isPending ? "上传中…" : "上传证件"}
              <input type="file" accept="image/*,application/pdf" style={{ display: "none" }} onChange={(e) => { const f = e.target.files?.[0]; if (f) upload.mutate(f); e.target.value = ""; }} />
            </label>
          </div>
        </div>
      )}
    </div>
  );
}

export function FleetPage() {
  const [days, setDays] = useState(30);
  const q = useQuery({
    queryKey: ["credentials", days],
    queryFn: () => apiGet<ExpiringCredentials>(`/credentials/expiring?days=${days}`),
    refetchInterval: 60000,
  });

  const s = q.data?.summary;

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">
          车队合规预警
          <span className="ai-pill"></span>
        </div>
        <div className="form-row">
          <span className="muted small">预警窗口</span>
          <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
            <option value={7}>7 天内</option>
            <option value={15}>15 天内</option>
            <option value={30}>30 天内</option>
            <option value={60}>60 天内</option>
            <option value={90}>90 天内</option>
          </select>
        </div>
        {s && (
          <div className="kv">
            <div><span>合计</span><b>{s.total}</b></div>
            <div><span>已过期</span><b style={s.expired > 0 ? { color: "var(--red)" } : {}}>{s.expired}</b></div>
            <div><span>紧急(≤7天)</span><b>{s.critical}</b></div>
            <div><span>临期</span><b>{s.warning}</b></div>
          </div>
        )}
      </div>

      {q.isLoading ? (
        <div className="muted" style={{ padding: 16 }}>加载中…</div>
      ) : (
        <div className="ct-grid">
          <CredTable title="车辆证件" rows={q.data?.vehicles ?? []} subjectLabel="车牌" />
          <CredTable title="司机资质" rows={q.data?.drivers ?? []} subjectLabel="司机" />
        </div>
      )}

      <CredentialLibrary />
    </div>
  );
}
