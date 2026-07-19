import { useMutation, useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { apiGet, apiUpload } from "../api/client";
import { toast } from "../api/toast";
import { CarrierCenter } from "../components/CarrierCenter";
import { DataTable, type DataColumn } from "../components/DataTable";
import { LanePriceLib } from "../components/LanePriceLib";
import { StateView } from "../components/StateView";
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

// ── 主数据列表通用外壳（搜索 + DataTable 顶尖表格能力） ─────────
function ResourceTable<T>({
  queryKey, url, columns, rowKey, viewKey, exportName, searchKeys, placeholder,
}: {
  queryKey: string;
  url: string;
  columns: DataColumn<T>[];
  rowKey: (row: T) => string;
  viewKey: string;
  exportName: string;
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
        <StateView kind="loading" compact />
      ) : q.isError ? (
        <StateView kind="error" onRetry={() => q.refetch()} />
      ) : rows.length === 0 ? (
        <StateView kind="empty" title={kw ? "没有匹配的记录" : "暂无数据"} hint={kw ? "调整搜索关键词再试。" : undefined} />
      ) : (
        <DataTable<T>
          columns={columns}
          rows={rows}
          rowKey={rowKey}
          viewKey={viewKey}
          exportName={exportName}
          stickyFirst
          toolbarLeft={<span className="muted small">共 {rows.length} 条 · 点击表头排序 · 「列」增减字段</span>}
        />
      )}
    </div>
  );
}

const vehicleColumns: DataColumn<Vehicle>[] = [
  { key: "plate", header: "车牌", width: 130, alwaysVisible: true, sortValue: (v) => v.plate_no, exportValue: (v) => v.plate_no, render: (v) => <span className="mono">{v.plate_no}</span> },
  { key: "type", header: "车型", width: 110, sortValue: (v) => v.vehicle_class_label || v.vehicle_type || "", exportValue: (v) => v.vehicle_class_label || v.vehicle_type || "", render: (v) => v.vehicle_class_label || v.vehicle_type || "-" },
  { key: "body", header: "车厢", width: 90, sortValue: (v) => v.body_type_label || "", exportValue: (v) => v.body_type_label || "", render: (v) => v.body_type_label || "-" },
  { key: "ton", header: "核载(吨)", width: 100, align: "right", sortValue: (v) => Number(v.load_capacity_ton) || 0, exportValue: (v) => v.load_capacity_ton ?? "", render: (v) => v.load_capacity_ton ?? "-" },
  { key: "cbm", header: "容积(方)", width: 100, align: "right", sortValue: (v) => Number(v.volume_capacity_cbm) || 0, exportValue: (v) => v.volume_capacity_cbm ?? "", render: (v) => v.volume_capacity_cbm ?? "-" },
  { key: "owner", header: "归属", width: 120, sortValue: (v) => v.carrier_name || v.dispatch_source_label || "", exportValue: (v) => v.carrier_name || v.dispatch_source_label || "自有", render: (v) => v.carrier_name || (v.dispatch_source_label ?? "自有") },
  { key: "active", header: "状态", width: 80, sortValue: (v) => (v.is_active ? "1" : "0"), exportValue: (v) => (v.is_active ? "启用" : "停用"), render: (v) => <span className={`tag ${v.is_active ? "tag-low" : "tag-none"}`}>{v.is_active ? "启用" : "停用"}</span> },
];
function VehiclesTab() {
  return (
    <ResourceTable<Vehicle>
      queryKey="rh-vehicles" url="/vehicles?page_size=300" placeholder="搜索车牌 / 车型" viewKey="fleet-vehicles" exportName="车辆档案"
      rowKey={(v) => v.id} searchKeys={(v) => `${v.plate_no} ${v.vehicle_type ?? ""} ${v.carrier_name ?? ""}`} columns={vehicleColumns}
    />
  );
}

const driverColumns: DataColumn<Driver>[] = [
  { key: "name", header: "姓名", width: 110, alwaysVisible: true, sortValue: (d) => d.name, exportValue: (d) => d.name, render: (d) => d.name },
  { key: "phone", header: "电话", width: 130, sortValue: (d) => d.phone || "", exportValue: (d) => d.phone || "", render: (d) => <span className="mono">{d.phone || "-"}</span> },
  { key: "emp", header: "用工", width: 100, sortValue: (d) => d.employment_label || "", exportValue: (d) => d.employment_label || "", render: (d) => d.employment_label || "-" },
  { key: "lic", header: "准驾", width: 80, sortValue: (d) => d.license_type || "", exportValue: (d) => d.license_type || "", render: (d) => d.license_type || "-" },
  { key: "exp", header: "驾照有效期", width: 120, sortValue: (d) => d.license_expiry || "", exportValue: (d) => d.license_expiry || "", render: (d) => d.license_expiry || "-" },
  { key: "owner", header: "归属", width: 120, sortValue: (d) => d.carrier_name || "", exportValue: (d) => d.carrier_name || "自有", render: (d) => d.carrier_name || "自有" },
  { key: "active", header: "状态", width: 80, sortValue: (d) => (d.is_active ? "1" : "0"), exportValue: (d) => (d.is_active ? "在职" : "停用"), render: (d) => <span className={`tag ${d.is_active ? "tag-low" : "tag-none"}`}>{d.is_active ? "在职" : "停用"}</span> },
];
function DriversTab() {
  return (
    <ResourceTable<Driver>
      queryKey="rh-drivers" url="/drivers?page_size=300" placeholder="搜索姓名 / 电话" viewKey="fleet-drivers" exportName="司机档案"
      rowKey={(d) => d.id} searchKeys={(d) => `${d.name} ${d.phone ?? ""} ${d.carrier_name ?? ""}`} columns={driverColumns}
    />
  );
}

const customerColumns: DataColumn<Customer>[] = [
  { key: "code", header: "编码", width: 110, alwaysVisible: true, sortValue: (c) => c.code, exportValue: (c) => c.code, render: (c) => <span className="mono">{c.code}</span> },
  { key: "name", header: "客户名称", width: 160, sortValue: (c) => c.name, exportValue: (c) => c.name, render: (c) => c.name },
  { key: "contact", header: "联系人", width: 100, sortValue: (c) => c.contact_name || "", exportValue: (c) => c.contact_name || "", render: (c) => c.contact_name || "-" },
  { key: "phone", header: "电话", width: 130, sortValue: (c) => c.contact_phone || "", exportValue: (c) => c.contact_phone || "", render: (c) => <span className="mono">{c.contact_phone || "-"}</span> },
  { key: "credit", header: "授信额度", width: 120, align: "right", sortValue: (c) => Number(c.credit_limit) || 0, exportValue: (c) => Number(c.credit_limit) || 0, render: (c) => Number(c.credit_limit) > 0 ? `¥${Number(c.credit_limit).toLocaleString()}` : "不限" },
  { key: "days", header: "账期(天)", width: 90, align: "right", sortValue: (c) => c.credit_days ?? 0, exportValue: (c) => c.credit_days ?? "", render: (c) => c.credit_days },
  { key: "active", header: "状态", width: 80, sortValue: (c) => (c.is_active ? "1" : "0"), exportValue: (c) => (c.is_active ? "启用" : "停用"), render: (c) => <span className={`tag ${c.is_active ? "tag-low" : "tag-none"}`}>{c.is_active ? "启用" : "停用"}</span> },
];
function CustomersTab() {
  return (
    <ResourceTable<Customer>
      queryKey="rh-customers" url="/customers?page_size=300" placeholder="搜索编码 / 名称 / 电话" viewKey="fleet-customers" exportName="客户"
      rowKey={(c) => c.id} searchKeys={(c) => `${c.code} ${c.name} ${c.contact_phone ?? ""}`} columns={customerColumns}
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
