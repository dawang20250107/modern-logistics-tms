import { useMutation, useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { apiGet, apiUpload } from "../api/client";
import { fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import { CarrierCenter } from "../components/CarrierCenter";
import { DataTable, type DataColumn } from "../components/DataTable";
import { FilterBuilder, applyFilterModel, activeConditionCount, describeCondition, EMPTY_MODEL, type FilterFieldDef, type FilterModel } from "../components/FilterBuilder";
import { LanePriceLib } from "../components/LanePriceLib";
import { StateView } from "../components/StateView";
import { IconGitBranch, IconMapPin, IconTruck, IconBox, IconDatabase, IconShield, IconWarning, IconArrowRight } from "../components/Icons";
import type {
  Carrier, CarrierLanePrice, CredentialRow, CredSeverity, Customer, Driver, DriverCredential, DriverLookup,
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

// ── 主数据列表通用外壳（搜索 + 高级多条件筛选 + DataTable 顶尖表格能力） ──
function ResourceTable<T>({
  queryKey, url, columns, rowKey, viewKey, exportName, searchKeys, placeholder, filterFields,
}: {
  queryKey: string;
  url: string;
  columns: DataColumn<T>[];
  rowKey: (row: T) => string;
  viewKey: string;
  exportName: string;
  searchKeys: (row: T) => string;
  placeholder: string;
  filterFields?: FilterFieldDef[];
}) {
  const [kw, setKw] = useState("");
  const [model, setModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showFilter, setShowFilter] = useState(false);
  const q = useQuery({ queryKey: [queryKey], queryFn: () => apiGet<Paginated<T>>(url) });
  const activeCount = filterFields ? activeConditionCount(model, filterFields) : 0;
  const rows = useMemo(() => {
    let items = q.data?.items ?? [];
    const k = kw.trim().toLowerCase();
    if (k) items = items.filter((r) => searchKeys(r).toLowerCase().includes(k));
    if (filterFields) items = applyFilterModel(items, model, filterFields);
    return items;
  }, [q.data, kw, searchKeys, model, filterFields]);

  return (
    <div className="panel">
      <div className="panel-head" style={{ gap: 8, flexWrap: "wrap" }}>
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>清单<span className="ai-pill">{rows.length}</span></span>
        <div style={{ flex: 1 }} />
        <input className="search" style={{ width: 240 }} placeholder={placeholder} value={kw} onChange={(e) => setKw(e.target.value)} />
        {filterFields && (
          <div style={{ position: "relative" }}>
            <button className={`btn-ghost${activeCount > 0 || showFilter ? " on-accent" : ""}`} onClick={(e) => { e.stopPropagation(); setShowFilter((v) => !v); }}>
              <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 5h18l-7 8v5l-4 2v-7z" /></svg>
                高级筛选{activeCount > 0 ? ` · ${activeCount}` : ""}
              </span>
            </button>
            {showFilter && <FilterBuilder fields={filterFields} model={model} onChange={setModel} onClose={() => setShowFilter(false)} />}
          </div>
        )}
      </div>
      {filterFields && activeCount > 0 && (
        <div className="om-chips">
          <span className="muted small">条件（{model.combinator === "and" ? "全部满足" : "任一满足"}）：</span>
          {model.conditions.map((c) => {
            const label = describeCondition(c, filterFields);
            if (!label) return null;
            return <span key={c.id} className="filter-chip">{label}<button onClick={() => setModel((m) => ({ ...m, conditions: m.conditions.filter((x) => x.id !== c.id) }))}>×</button></span>;
          })}
          <button className="linkish small" onClick={() => setModel(EMPTY_MODEL)}>清空条件</button>
        </div>
      )}
      {q.isLoading ? (
        <StateView kind="loading" compact />
      ) : q.isError ? (
        <StateView kind="error" onRetry={() => q.refetch()} />
      ) : rows.length === 0 ? (
        <StateView kind="empty" title={kw || activeCount ? "没有匹配的记录" : "暂无数据"} hint={kw || activeCount ? "调整搜索/筛选条件再试。" : undefined} />
      ) : (
        <DataTable<T>
          columns={columns}
          rows={rows}
          rowKey={rowKey}
          viewKey={viewKey}
          exportName={exportName}
          stickyFirst
          toolbarLeft={<span className="muted small">共 {rows.length} 条 · 表头 ⚟ 筛选/排序 · 「列」增减字段</span>}
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
const vehicleFilterFields: FilterFieldDef[] = [
  { key: "plate", label: "车牌", type: "text", accessor: (v) => (v as Vehicle).plate_no },
  { key: "type", label: "车型", type: "text", accessor: (v) => (v as Vehicle).vehicle_class_label || (v as Vehicle).vehicle_type || "" },
  { key: "owner", label: "归属", type: "text", accessor: (v) => (v as Vehicle).carrier_name || "自有" },
  { key: "ton", label: "核载(吨)", type: "number", accessor: (v) => Number((v as Vehicle).load_capacity_ton) || 0 },
  { key: "cbm", label: "容积(方)", type: "number", accessor: (v) => Number((v as Vehicle).volume_capacity_cbm) || 0 },
  { key: "active", label: "状态", type: "enum", options: [{ value: "1", label: "启用" }, { value: "0", label: "停用" }], accessor: (v) => ((v as Vehicle).is_active ? "1" : "0") },
];
function VehiclesTab() {
  return (
    <ResourceTable<Vehicle>
      queryKey="rh-vehicles" url="/vehicles?page_size=300" placeholder="搜索车牌 / 车型" viewKey="fleet-vehicles" exportName="车辆档案"
      rowKey={(v) => v.id} searchKeys={(v) => `${v.plate_no} ${v.vehicle_type ?? ""} ${v.carrier_name ?? ""}`} columns={vehicleColumns} filterFields={vehicleFilterFields}
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
const driverFilterFields: FilterFieldDef[] = [
  { key: "name", label: "姓名", type: "text", accessor: (d) => (d as Driver).name },
  { key: "phone", label: "电话", type: "text", accessor: (d) => (d as Driver).phone || "" },
  { key: "emp", label: "用工", type: "text", accessor: (d) => (d as Driver).employment_label || "" },
  { key: "lic", label: "准驾", type: "text", accessor: (d) => (d as Driver).license_type || "" },
  { key: "owner", label: "归属", type: "text", accessor: (d) => (d as Driver).carrier_name || "自有" },
  { key: "active", label: "状态", type: "enum", options: [{ value: "1", label: "在职" }, { value: "0", label: "停用" }], accessor: (d) => ((d as Driver).is_active ? "1" : "0") },
];
function DriversTab() {
  return (
    <ResourceTable<Driver>
      queryKey="rh-drivers" url="/drivers?page_size=300" placeholder="搜索姓名 / 电话" viewKey="fleet-drivers" exportName="司机档案"
      rowKey={(d) => d.id} searchKeys={(d) => `${d.name} ${d.phone ?? ""} ${d.carrier_name ?? ""}`} columns={driverColumns} filterFields={driverFilterFields}
    />
  );
}

const CUST_LEVEL_TONE: Record<string, string> = { S: "tag-info", A: "tag-low", B: "tag-info", C: "tag-medium", D: "tag-none" };
const CUST_CATEGORY_LABEL: Record<string, string> = { individual: "个体", enterprise: "企业", government: "政府" };

const customerColumns: DataColumn<Customer>[] = [
  { key: "code", header: "编码", width: 110, alwaysVisible: true, sortValue: (c) => c.code, exportValue: (c) => c.code, render: (c) => <span className="mono">{c.code}</span> },
  { key: "name", header: "客户名称", width: 160, sortValue: (c) => c.name, exportValue: (c) => c.name, render: (c) => c.name },
  { key: "level", header: "等级", width: 70, sortValue: (c) => c.level || "B", exportValue: (c) => c.level || "B", render: (c) => <span className={`tag ${CUST_LEVEL_TONE[c.level || "B"] ?? "tag-none"}`} title={c.level_label}>{c.level || "B"}</span> },
  { key: "category", header: "分类", width: 80, sortValue: (c) => c.category || "", exportValue: (c) => CUST_CATEGORY_LABEL[c.category || ""] ?? c.category ?? "", render: (c) => CUST_CATEGORY_LABEL[c.category || "enterprise"] ?? "企业" },
  { key: "contact", header: "联系人", width: 100, sortValue: (c) => c.contact_name || "", exportValue: (c) => c.contact_name || "", render: (c) => c.contact_name || "-" },
  { key: "phone", header: "电话", width: 130, sortValue: (c) => c.contact_phone || "", exportValue: (c) => c.contact_phone || "", render: (c) => <span className="mono">{c.contact_phone || "-"}</span> },
  { key: "credit", header: "授信额度", width: 120, align: "right", sortValue: (c) => Number(c.credit_limit) || 0, exportValue: (c) => Number(c.credit_limit) || 0, render: (c) => Number(c.credit_limit) > 0 ? fmtMoney(c.credit_limit) : "不限" },
  { key: "days", header: "账期(天)", width: 90, align: "right", sortValue: (c) => c.credit_days ?? 0, exportValue: (c) => c.credit_days ?? "", render: (c) => c.credit_days },
  { key: "active", header: "状态", width: 80, sortValue: (c) => (c.is_active ? "1" : "0"), exportValue: (c) => (c.is_active ? "启用" : "停用"), render: (c) => <span className={`tag ${c.is_active ? "tag-low" : "tag-none"}`}>{c.is_active ? "启用" : "停用"}</span> },
];
const customerFilterFields: FilterFieldDef[] = [
  { key: "name", label: "客户名称", type: "text", accessor: (c) => (c as Customer).name },
  { key: "code", label: "编码", type: "text", accessor: (c) => (c as Customer).code },
  { key: "contact", label: "联系人", type: "text", accessor: (c) => (c as Customer).contact_name || "" },
  { key: "level", label: "等级", type: "enum", options: ["S", "A", "B", "C", "D"].map((v) => ({ value: v, label: `${v} 级` })), accessor: (c) => (c as Customer).level || "B" },
  { key: "category", label: "分类", type: "enum", options: Object.entries(CUST_CATEGORY_LABEL).map(([value, label]) => ({ value, label })), accessor: (c) => (c as Customer).category || "enterprise" },
  { key: "credit", label: "授信额度", type: "number", accessor: (c) => Number((c as Customer).credit_limit) || 0 },
  { key: "days", label: "账期(天)", type: "number", accessor: (c) => Number((c as Customer).credit_days) || 0 },
  { key: "active", label: "状态", type: "enum", options: [{ value: "1", label: "启用" }, { value: "0", label: "停用" }], accessor: (c) => ((c as Customer).is_active ? "1" : "0") },
];
function CustomersTab() {
  return (
    <ResourceTable<Customer>
      queryKey="rh-customers" url="/customers?page_size=300" placeholder="搜索编码 / 名称 / 电话" viewKey="fleet-customers" exportName="客户"
      rowKey={(c) => c.id} searchKeys={(c) => `${c.code} ${c.name} ${c.contact_phone ?? ""}`} columns={customerColumns} filterFields={customerFilterFields}
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
        <div className="table-wrap">
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
        </div>
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
          <div className="table-wrap">
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
          </div>
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

// ── 资源库总览：一眼看清全部运力/客户/合规资产，点击直达对应清单 ──────
function ResourceOverview({ onJump }: { onJump: (tab: string) => void }) {
  const carriers = useQuery({ queryKey: ["rh-ov-carriers"], queryFn: () => apiGet<Paginated<Carrier>>("/carriers?page_size=1") });
  const vehicles = useQuery({ queryKey: ["rh-ov-vehicles"], queryFn: () => apiGet<Paginated<Vehicle>>("/vehicles?page_size=1") });
  const drivers = useQuery({ queryKey: ["rh-ov-drivers"], queryFn: () => apiGet<Paginated<Driver>>("/drivers?page_size=1") });
  const lanes = useQuery({ queryKey: ["rh-ov-lanes"], queryFn: () => apiGet<Paginated<CarrierLanePrice>>("/carrier-lane-prices?page_size=1") });
  const customers = useQuery({ queryKey: ["rh-ov-customers"], queryFn: () => apiGet<Paginated<Customer>>("/customers?page_size=500") });
  const cred = useQuery({ queryKey: ["rh-ov-cred"], queryFn: () => apiGet<ExpiringCredentials>("/credentials/expiring?days=30") });

  // 客户等级分布（资源库同步客户等级 S/A/B/C/D）
  const levelDist = useMemo(() => {
    const dist: Record<string, number> = { S: 0, A: 0, B: 0, C: 0, D: 0 };
    for (const c of customers.data?.items ?? []) dist[c.level || "B"] = (dist[c.level || "B"] || 0) + 1;
    return dist;
  }, [customers.data]);

  const credSum = cred.data?.summary;
  const credAlert = (credSum?.expired ?? 0) + (credSum?.critical ?? 0);

  const cards = [
    { key: "carriers", icon: <IconGitBranch size={20} />, tone: "accent", n: carriers.data?.total, label: "承运商", sub: "外协运力池", jump: "carriers" },
    { key: "lanes", icon: <IconMapPin size={20} />, tone: "blue", n: lanes.data?.total, label: "线路价库", sub: "承运商×线路报价", jump: "lanes" },
    { key: "vehicles", icon: <IconTruck size={20} />, tone: "violet", n: vehicles.data?.total, label: "车辆", sub: "自营 + 挂靠", jump: "vehicles" },
    { key: "drivers", icon: <IconBox size={20} />, tone: "green", n: drivers.data?.total, label: "司机", sub: "在册驾驶员", jump: "drivers" },
    { key: "customers", icon: <IconDatabase size={20} />, tone: "accent", n: customers.data?.total, label: "客户", sub: `S${levelDist.S} · A${levelDist.A} · B${levelDist.B} · C${levelDist.C} · D${levelDist.D}`, jump: "customers" },
    { key: "compliance", icon: credAlert > 0 ? <IconWarning size={20} /> : <IconShield size={20} />, tone: credAlert > 0 ? "red" : "green", n: credAlert, label: "证件预警", sub: credAlert > 0 ? `${credSum?.expired ?? 0} 过期 · ${credSum?.critical ?? 0} 紧急` : "30 天内无临期", jump: "compliance" },
  ];

  return (
    <div className="stack" style={{ gap: 16 }}>
      <div className="rh-hero">
        <div className="rh-hero-brand">
          <div className="rh-hero-ic"><IconDatabase size={22} /></div>
          <div>
            <div className="rh-hero-title">资源库</div>
          </div>
        </div>
      </div>
      <div className="rh-cards">
        {cards.map((c) => (
          <button key={c.key} className={`rh-card rh-${c.tone}`} onClick={() => onJump(c.jump)}>
            <div className="rh-card-top">
              <span className="rh-card-ic">{c.icon}</span>
              <IconArrowRight size={15} className="rh-card-go" />
            </div>
            <div className="rh-card-n">{c.n ?? "—"}</div>
            <div className="rh-card-l">{c.label}</div>
            <div className="rh-card-s">{c.sub}</div>
          </button>
        ))}
      </div>
      {credAlert > 0 && (
        <div className="rh-alert" onClick={() => onJump("compliance")}>
          <IconWarning size={16} className="icon-offset" />
          <span>有 <b>{credAlert}</b> 项车辆/司机证件已过期或临近到期（≤7天），点击进入「证件合规」处理 →</span>
        </div>
      )}
    </div>
  );
}

// 外协为主：承运商与线路价库置顶，自营车辆/司机档案退居其后
const RESOURCE_TABS: { key: string; label: string; render: (jump: (t: string) => void) => React.ReactNode }[] = [
  { key: "overview", label: "总览", render: (jump) => <ResourceOverview onJump={jump} /> },
  { key: "carriers", label: "承运商中心", render: () => <CarrierCenter /> },
  { key: "lanes", label: "线路价库", render: () => <LanePriceLib /> },
  { key: "vehicles", label: "车辆档案", render: () => <VehiclesTab /> },
  { key: "drivers", label: "司机档案", render: () => <DriversTab /> },
  { key: "customers", label: "客户", render: () => <CustomersTab /> },
  { key: "compliance", label: "证件合规", render: () => <ComplianceTab /> },
];

export function FleetPage() {
  const [tab, setTab] = useState("overview");
  const current = RESOURCE_TABS.find((t) => t.key === tab) ?? RESOURCE_TABS[0];
  return (
    <div className="stack">
      <div className="seg-tabs">
        {RESOURCE_TABS.map((t) => (
          <button key={t.key} className={tab === t.key ? "active" : ""} onClick={() => setTab(t.key)}>{t.label}</button>
        ))}
      </div>
      {current.render(setTab)}
    </div>
  );
}
