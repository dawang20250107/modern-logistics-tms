import { useMutation, useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { apiGet, apiUpload } from "../api/client";
import { fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import { CarrierCenter } from "../components/CarrierCenter";
import { DataTable, type DataColumn } from "../components/DataTable";
import { FilterBuilder, activeConditionCount, describeCondition, EMPTY_MODEL, type FilterFieldDef, type FilterModel } from "../components/FilterBuilder";
import { LanePriceLib } from "../components/LanePriceLib";
import { StateView } from "../components/StateView";
import { useServerTable } from "../api/useServerTable";
import { IconGitBranch, IconMapPin, IconTruck, IconBox, IconDatabase, IconShield, IconWarning, IconArrowRight } from "../components/Icons";
import type {
  Carrier, CarrierLanePrice, CredentialRow, CredSeverity, Customer, Driver, DriverCredential, DriverLookup,
  ExpiringCredentials, Paginated, Vehicle,
} from "../api/types";
import { CRED_SEVERITY_LABEL, CRED_TYPE_LABEL, OCR_STATUS_LABEL } from "../api/types";

const SEVERITY_TAG: Record<CredSeverity, string> = {
  expired: "high", critical: "medium", warning: "low",
};

function daysText(d: number): string {
  if (d < 0) return `已逾期 ${-d} 天`;
  if (d === 0) return "今天到期";
  return `剩 ${d} 天`;
}

// ── 主数据列表通用外壳（服务端搜索 + 高级多条件筛选 + 分页/排序 + 固定布局） ──
function ResourceTable<T>({
  queryKey, path, columns, rowKey, viewKey, exportName, placeholder, filterFields, defaultSort, title,
}: {
  queryKey: string;
  path: string; // 服务端列表接口，如 "/vehicles"
  columns: DataColumn<T>[];
  rowKey: (row: T) => string;
  viewKey: string;
  exportName: string;
  placeholder: string;
  filterFields?: FilterFieldDef[];
  defaultSort?: { field: string; dir: "asc" | "desc" };
  title: string;
}) {
  const [search, setSearch] = useState("");
  const [model, setModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showFilter, setShowFilter] = useState(false);
  const activeCount = filterFields ? activeConditionCount(model, filterFields) : 0;
  const anyFilter = Boolean(search) || activeCount > 0;
  const st = useServerTable<T>({ queryKey: [queryKey], path, pageSize: 50, defaultSort: defaultSort ?? null, model, search });

  return (
    <div className="panel om-panel">
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
      {st.isError ? (
        <StateView kind="error" onRetry={() => st.refetch()} />
      ) : (
        <DataTable<T>
          columns={columns} rows={st.rows} rowKey={rowKey} viewKey={viewKey} exportName={exportName}
          stickyFirst server={st.server} fill hideExport
          emptyState={anyFilter
            ? <StateView kind="empty" title="没有匹配的记录" hint="调整搜索/筛选条件再试，或清空条件查看全部。" />
            : <StateView kind="empty" title={`暂无${title}`} hint="通过右上「新增」建档后，将在此列出。" />}
          toolbarLeft={
            <>
              <span className="om-title" style={{ marginRight: 2 }}>{title}<span className="ai-pill">{st.total}</span></span>
              <input className="search" style={{ minWidth: 180, flex: 1, maxWidth: 280 }} placeholder={placeholder} value={search} onChange={(e) => setSearch(e.target.value)} />
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
            </>
          }
          toolbarRight={anyFilter ? <button className="linkish small" onClick={() => { setSearch(""); setModel(EMPTY_MODEL); }}>重置</button> : undefined}
        />
      )}
    </div>
  );
}

const vehicleColumns: DataColumn<Vehicle>[] = [
  { key: "plate", header: "车牌", width: 130, alwaysVisible: true, sortField: "plate_no", sortValue: (v) => v.plate_no, exportValue: (v) => v.plate_no, render: (v) => <span className="mono">{v.plate_no}</span> },
  { key: "type", header: "车型", width: 110, sortField: "vehicle_type", sortValue: (v) => v.vehicle_class_label || v.vehicle_type || "", exportValue: (v) => v.vehicle_class_label || v.vehicle_type || "", render: (v) => v.vehicle_class_label || v.vehicle_type || "—" },
  { key: "body", header: "车厢", width: 90, sortValue: (v) => v.body_type_label || "", exportValue: (v) => v.body_type_label || "", render: (v) => v.body_type_label || "—" },
  { key: "ton", header: "核载(吨)", width: 100, align: "right", sortField: "load_capacity_ton", sortValue: (v) => Number(v.load_capacity_ton) || 0, exportValue: (v) => v.load_capacity_ton ?? "", render: (v) => v.load_capacity_ton ?? "—" },
  { key: "cbm", header: "容积(方)", width: 100, align: "right", sortField: "volume_capacity_cbm", sortValue: (v) => Number(v.volume_capacity_cbm) || 0, exportValue: (v) => v.volume_capacity_cbm ?? "", render: (v) => v.volume_capacity_cbm ?? "—" },
  { key: "owner", header: "归属", width: 120, sortField: "owner_name", sortValue: (v) => v.carrier_name || v.dispatch_source_label || "", exportValue: (v) => v.carrier_name || v.dispatch_source_label || "自有", render: (v) => v.carrier_name || (v.dispatch_source_label ?? "自有") },
  { key: "active", header: "状态", width: 80, sortField: "is_active", sortValue: (v) => (v.is_active ? "1" : "0"), exportValue: (v) => (v.is_active ? "启用" : "停用"), render: (v) => <span className={`tag ${v.is_active ? "tag-low" : "tag-none"}`}>{v.is_active ? "启用" : "停用"}</span> },
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
      queryKey="rh-vehicles" path="/vehicles" title="车辆档案" placeholder="搜索车牌 / 车型" viewKey="fleet-vehicles" exportName="车辆档案"
      defaultSort={{ field: "plate_no", dir: "asc" }}
      rowKey={(v) => v.id} columns={vehicleColumns} filterFields={vehicleFilterFields}
    />
  );
}

const DRIVER_EMP_LABEL: Record<string, string> = { employee: "自有员工", outsourced: "外协外调", carrier_driver: "承运商司机", temp: "临时" };
const driverColumns: DataColumn<Driver>[] = [
  { key: "name", header: "姓名", width: 110, alwaysVisible: true, sortField: "name", sortValue: (d) => d.name, exportValue: (d) => d.name, render: (d) => d.name },
  { key: "phone", header: "电话", width: 130, sortField: "phone", sortValue: (d) => d.phone || "", exportValue: (d) => d.phone || "", render: (d) => <span className="mono">{d.phone || "—"}</span> },
  { key: "emp", header: "用工", width: 100, sortField: "employment_type", sortValue: (d) => d.employment_label || "", exportValue: (d) => d.employment_label || "", render: (d) => d.employment_label || "—" },
  { key: "lic", header: "准驾", width: 80, sortField: "license_type", sortValue: (d) => d.license_type || "", exportValue: (d) => d.license_type || "", render: (d) => d.license_type || "—" },
  { key: "exp", header: "驾照有效期", width: 120, sortField: "license_expiry", sortValue: (d) => d.license_expiry || "", exportValue: (d) => d.license_expiry || "", render: (d) => d.license_expiry || "—" },
  { key: "owner", header: "归属", width: 120, sortField: "owner_name", sortValue: (d) => d.carrier_name || "", exportValue: (d) => d.carrier_name || "自有", render: (d) => d.carrier_name || "自有" },
  { key: "active", header: "状态", width: 80, sortField: "is_active", sortValue: (d) => (d.is_active ? "1" : "0"), exportValue: (d) => (d.is_active ? "在职" : "停用"), render: (d) => <span className={`tag ${d.is_active ? "tag-low" : "tag-none"}`}>{d.is_active ? "在职" : "停用"}</span> },
];
const driverFilterFields: FilterFieldDef[] = [
  { key: "name", label: "姓名", type: "text", accessor: (d) => (d as Driver).name },
  { key: "phone", label: "电话", type: "text", accessor: (d) => (d as Driver).phone || "" },
  { key: "emp", label: "用工", type: "enum", options: Object.entries(DRIVER_EMP_LABEL).map(([value, label]) => ({ value, label })), accessor: (d) => (d as Driver).employment_type || "" },
  { key: "license", label: "准驾", type: "text", accessor: (d) => (d as Driver).license_type || "" },
  { key: "owner", label: "归属", type: "text", accessor: (d) => (d as Driver).carrier_name || "自有" },
  { key: "active", label: "状态", type: "enum", options: [{ value: "1", label: "在职" }, { value: "0", label: "停用" }], accessor: (d) => ((d as Driver).is_active ? "1" : "0") },
];
function DriversTab() {
  return (
    <ResourceTable<Driver>
      queryKey="rh-drivers" path="/drivers" title="司机档案" placeholder="搜索姓名 / 电话" viewKey="fleet-drivers" exportName="司机档案"
      defaultSort={{ field: "name", dir: "asc" }}
      rowKey={(d) => d.id} columns={driverColumns} filterFields={driverFilterFields}
    />
  );
}

const CUST_LEVEL_TONE: Record<string, string> = { S: "tag-info", A: "tag-low", B: "tag-info", C: "tag-medium", D: "tag-none" };
const CUST_CATEGORY_LABEL: Record<string, string> = { individual: "个体", enterprise: "企业", government: "政府" };

const customerColumns: DataColumn<Customer>[] = [
  { key: "code", header: "编码", width: 110, alwaysVisible: true, sortField: "code", sortValue: (c) => c.code, exportValue: (c) => c.code, render: (c) => <span className="mono">{c.code}</span> },
  { key: "name", header: "客户名称", width: 160, sortField: "name", sortValue: (c) => c.name, exportValue: (c) => c.name, render: (c) => c.name },
  { key: "level", header: "等级", width: 70, sortField: "level", sortValue: (c) => c.level || "B", exportValue: (c) => c.level || "B", render: (c) => <span className={`tag ${CUST_LEVEL_TONE[c.level || "B"] ?? "tag-none"}`} title={c.level_label}>{c.level || "B"}</span> },
  { key: "category", header: "分类", width: 80, sortField: "category", sortValue: (c) => c.category || "", exportValue: (c) => CUST_CATEGORY_LABEL[c.category || ""] ?? c.category ?? "", render: (c) => CUST_CATEGORY_LABEL[c.category || "enterprise"] ?? "企业" },
  { key: "contact", header: "联系人", width: 100, sortField: "contact_name", sortValue: (c) => c.contact_name || "", exportValue: (c) => c.contact_name || "", render: (c) => c.contact_name || "—" },
  { key: "phone", header: "电话", width: 130, sortField: "contact_phone", sortValue: (c) => c.contact_phone || "", exportValue: (c) => c.contact_phone || "", render: (c) => <span className="mono">{c.contact_phone || "—"}</span> },
  { key: "credit", header: "授信额度", width: 120, align: "right", sortField: "credit_limit", sortValue: (c) => Number(c.credit_limit) || 0, exportValue: (c) => Number(c.credit_limit) || 0, render: (c) => Number(c.credit_limit) > 0 ? fmtMoney(c.credit_limit) : "不限" },
  { key: "days", header: "账期(天)", width: 90, align: "right", sortField: "credit_days", sortValue: (c) => c.credit_days ?? 0, exportValue: (c) => c.credit_days ?? "", render: (c) => c.credit_days },
  { key: "active", header: "状态", width: 80, sortField: "is_active", sortValue: (c) => (c.is_active ? "1" : "0"), exportValue: (c) => (c.is_active ? "启用" : "停用"), render: (c) => <span className={`tag ${c.is_active ? "tag-low" : "tag-none"}`}>{c.is_active ? "启用" : "停用"}</span> },
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
      queryKey="rh-customers" path="/customers" title="客户" placeholder="搜索编码 / 名称 / 电话" viewKey="fleet-customers" exportName="客户"
      defaultSort={{ field: "code", dir: "asc" }}
      rowKey={(c) => c.id} columns={customerColumns} filterFields={customerFilterFields}
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
                  <td className="small">{c.holder_name || "—"}</td>
                  <td className="small">{c.cert_no || "—"}</td>
                  <td className="small">{c.expiry_date || "—"}</td>
                  <td><span className={`tag${c.ocr_status === "done" ? " tag-low" : ""}`}>{OCR_STATUS_LABEL[c.ocr_status] ?? c.ocr_status}</span></td>
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
            <label className="btn-ghost file-trigger" style={{ cursor: "pointer" }}>
              {upload.isPending ? "上传中…" : "上传证件"}
              <input className="file-input-accessible" type="file" accept="image/*,application/pdf" disabled={upload.isPending} onChange={(e) => { const f = e.target.files?.[0]; if (f) upload.mutate(f); e.target.value = ""; }} />
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
        <StateView kind="loading" compact />
      ) : q.isError ? (
        <StateView kind="error" hint="证件合规数据暂时无法加载。" onRetry={() => q.refetch()} compact />
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

// 固定布局（表格内滚 + 底部分页贴底）的 Tab：其余（总览/证件合规）为普通纵向流
const FIXED_TABS = new Set(["carriers", "lanes", "vehicles", "drivers", "customers"]);

export function FleetPage() {
  const [tab, setTab] = useState("overview");
  const current = RESOURCE_TABS.find((t) => t.key === tab) ?? RESOURCE_TABS[0];
  return (
    <div className={`stack${FIXED_TABS.has(tab) ? " table-page" : ""}`}>
      <div className="seg-tabs">
        {RESOURCE_TABS.map((t) => (
          <button key={t.key} className={tab === t.key ? "active" : ""} onClick={() => setTab(t.key)}>{t.label}</button>
        ))}
      </div>
      {current.render(setTab)}
    </div>
  );
}
