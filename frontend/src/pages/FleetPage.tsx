import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet } from "../api/client";
import type { CredentialRow, CredSeverity, ExpiringCredentials } from "../api/types";
import { CRED_SEVERITY_LABEL } from "../api/types";

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
        <div className="muted" style={{ padding: 16 }}>无到期证件 ✓</div>
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
          <span className="ai-pill">证件到期 · 资质风控</span>
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
    </div>
  );
}
