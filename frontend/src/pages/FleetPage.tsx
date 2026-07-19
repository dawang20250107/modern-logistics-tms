import { useMutation, useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { apiGet, apiUpload } from "../api/client";
import { toast } from "../api/toast";
import { CarrierCenter } from "../components/CarrierCenter";
import { LanePriceLib } from "../components/LanePriceLib";
import type {
  CredentialRow, CredSeverity, Customer, Driver, DriverCredential, DriverLookup,
  ExpiringCredentials, Paginated, Vehicle,
} from "../api/types";
import { CRED_SEVERITY_LABEL, CRED_TYPE_LABEL } from "../api/types";

const SEVERITY_TAG: Record<CredSeverity, string> = {
  expired: "high", critical: "medium", warning: "low",
};

function daysText(d: number): string {
  if (d < 0) return `已逾期 ${-d} 天`;
  if (d === 0) return "今天到期";
  return `剩 ${d} 天`;
}

// ── 主数据列表通用外壳（搜索 + 表格） ─────────────────────────
function ListPanel<T>({
  queryKey, url, columns, searchKeys, placeholder,
}: {
  queryKey: string;
  url: string;
  columns: { header: string; render: (row: T) => React.ReactNode; num?: boolean }[];
  searchKeys: (row: T) => string;
  placeholder: string;
}) {
  const [kw, setKw] = useState("");
  const q = useQuery({ queryKey: [queryKey], queryFn: () => apiGet<Paginated<T>>(url) });
  const rows = useMemo(() => {
    const items = q.data?.items ?? [];
    const k = kw.trim().toLowerCase();
    return k ? items.filter((r) => searchKeys(r).toLowerCase().includes(k)) : items;
  }, [q.data, kw, searchKeys]);

  return (
    <div className="panel">
      <div className="panel-head">
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>清单<span className="ai-pill">{rows.length}</span></span>
        <input className="search" style={{ width: 240 }} placeholder={placeholder} value={kw} onChange={(e) => setKw(e.target.value)} />
      </div>
      {q.isLoading ? (
        <div className="muted" style={{ padding: 16 }}>加载中…</div>
      ) : rows.length === 0 ? (
        <div className="muted" style={{ padding: 16 }}>暂无数据。</div>
      ) : (
        <table className="table">
          <thead><tr>{columns.map((c, i) => <th key={i} className={c.num ? "num" : undefined}>{c.header}</th>)}</tr></thead>
          <tbody>
            {rows.map((row, ri) => (
              <tr key={ri}>{columns.map((c, ci) => <td key={ci} className={c.num ? "num" : undefined}>{c.render(row)}</td>)}</tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function VehiclesTab() {
  return (
    <ListPanel<Vehicle>
      queryKey="rh-vehicles" url="/vehicles?page_size=300" placeholder="搜索车牌 / 车型"
      searchKeys={(v) => `${v.plate_no} ${v.vehicle_type ?? ""} ${v.carrier_name ?? ""}`}
      columns={[
        { header: "车牌", render: (v) => <span className="mono">{v.plate_no}</span> },
        { header: "车型", render: (v) => v.vehicle_class_label || v.vehicle_type || "-" },
        { header: "车厢", render: (v) => v.body_type_label || "-" },
        { header: "核载(吨)", num: true, render: (v) => v.load_capacity_ton ?? "-" },
        { header: "容积(方)", num: true, render: (v) => v.volume_capacity_cbm ?? "-" },
        { header: "归属", render: (v) => v.carrier_name || (v.dispatch_source_label ?? "自有") },
        { header: "状态", render: (v) => <span className={`tag ${v.is_active ? "tag-low" : "tag-none"}`}>{v.is_active ? "启用" : "停用"}</span> },
      ]}
    />
  );
}

function DriversTab() {
  return (
    <ListPanel<Driver>
      queryKey="rh-drivers" url="/drivers?page_size=300" placeholder="搜索姓名 / 电话"
      searchKeys={(d) => `${d.name} ${d.phone ?? ""} ${d.carrier_name ?? ""}`}
      columns={[
        { header: "姓名", render: (d) => d.name },
        { header: "电话", render: (d) => <span className="mono">{d.phone || "-"}</span> },
        { header: "用工", render: (d) => d.employment_label || "-" },
        { header: "准驾", render: (d) => d.license_type || "-" },
        { header: "驾照有效期", render: (d) => d.license_expiry || "-" },
        { header: "归属", render: (d) => d.carrier_name || "自有" },
        { header: "状态", render: (d) => <span className={`tag ${d.is_active ? "tag-low" : "tag-none"}`}>{d.is_active ? "在职" : "停用"}</span> },
      ]}
    />
  );
}

function CustomersTab() {
  return (
    <ListPanel<Customer>
      queryKey="rh-customers" url="/customers?page_size=300" placeholder="搜索编码 / 名称 / 电话"
      searchKeys={(c) => `${c.code} ${c.name} ${c.contact_phone ?? ""}`}
      columns={[
        { header: "编码", render: (c) => <span className="mono">{c.code}</span> },
        { header: "客户名称", render: (c) => c.name },
        { header: "联系人", render: (c) => c.contact_name || "-" },
        { header: "电话", render: (c) => <span className="mono">{c.contact_phone || "-"}</span> },
        { header: "授信额度", num: true, render: (c) => Number(c.credit_limit) > 0 ? `¥${Number(c.credit_limit).toLocaleString()}` : "不限" },
        { header: "账期(天)", num: true, render: (c) => c.credit_days },
        { header: "状态", render: (c) => <span className={`tag ${c.is_active ? "tag-low" : "tag-none"}`}>{c.is_active ? "启用" : "停用"}</span> },
      ]}
    />
  );
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

function ComplianceTab() {
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
        <div className="panel-head">证件合规预警</div>
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

// 外协为主：承运商与线路价库置顶，自营车辆/司机档案退居其后
const RESOURCE_TABS: { key: string; label: string; render: () => React.ReactNode }[] = [
  { key: "carriers", label: "承运商中心", render: () => <CarrierCenter /> },
  { key: "lanes", label: "线路价库", render: () => <LanePriceLib /> },
  { key: "vehicles", label: "车辆档案", render: () => <VehiclesTab /> },
  { key: "drivers", label: "司机档案", render: () => <DriversTab /> },
  { key: "customers", label: "客户", render: () => <CustomersTab /> },
  { key: "compliance", label: "证件合规", render: () => <ComplianceTab /> },
];

export function FleetPage() {
  const [tab, setTab] = useState("carriers");
  const current = RESOURCE_TABS.find((t) => t.key === tab) ?? RESOURCE_TABS[0];
  return (
    <div className="stack">
      <div className="seg-tabs">
        {RESOURCE_TABS.map((t) => (
          <button key={t.key} className={tab === t.key ? "active" : ""} onClick={() => setTab(t.key)}>{t.label}</button>
        ))}
      </div>
      {current.render()}
    </div>
  );
}
