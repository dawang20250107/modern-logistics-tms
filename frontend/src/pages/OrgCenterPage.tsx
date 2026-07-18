import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Fragment, useMemo, useState } from "react";

import { apiDownload, apiGet, apiPost, apiUpload } from "../api/client";
import { confirmAction } from "../api/confirm";
import { hasPerm, useAuth } from "../auth/auth";
import { toast } from "../api/toast";
import type {
  AccountHandover,
  CoverageResult,
  Employee,
  OrgOption,
  OrgOverview,
  OrgTreeNode,
  Paginated,
  RbacMatrix,
  ServiceArea,
} from "../api/types";
import { AREA_TYPE_LABEL, ORG_PROPERTY_LABEL } from "../api/types";

function useOrgOptions() {
  return useQuery({
    queryKey: ["org-options"],
    queryFn: () => apiGet<Paginated<OrgOption>>("/org/organizations?page_size=200&ordering=sort_order"),
    select: (d) => d.items,
  });
}

type Tab = "overview" | "org" | "employees" | "areas" | "rbac" | "audit";

const STATUS_TAG: Record<string, string> = { active: "low", disabled: "medium", left: "high" };
const PROPERTY_TAG: Record<string, string> = {
  self: "low", franchise: "medium", outsource: "medium", partner: "low", jv: "medium",
};

function OverviewTab() {
  const q = useQuery({ queryKey: ["org-overview"], queryFn: () => apiGet<OrgOverview>("/org/overview") });
  const d = q.data;
  if (!d) return <div className="muted" style={{ padding: 16 }}>加载中…</div>;
  return (
    <div className="stack">
      <div className="kv">
        <div><span>组织总数</span><b>{d.organizations.total}</b></div>
        <div><span>部门</span><b>{d.departments}</b></div>
        <div><span>在职员工</span><b>{d.employees.active}</b></div>
        <div><span>员工总数</span><b>{d.employees.total}</b></div>
        <div><span>服务区划</span><b>{d.service_areas.total}</b></div>
        <div>
          <span>在职无账号</span>
          <b style={d.employees.active_without_account > 0 ? { color: "var(--red)" } : {}}>
            {d.employees.active_without_account}
          </b>
        </div>
      </div>
      <div className="ct-grid">
        <div className="panel">
          <div className="panel-head">经营属性分布</div>
          <table className="table">
            <tbody>
              {Object.entries(d.organizations.by_property).map(([k, v]) => (
                <tr key={k}><td>{ORG_PROPERTY_LABEL[k] ?? k}</td><td className="mono">{v}</td></tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="panel">
          <div className="panel-head">服务区划构成</div>
          <table className="table">
            <tbody>
              {Object.entries(d.service_areas.by_type).map(([k, v]) => (
                <tr key={k}><td>{AREA_TYPE_LABEL[k] ?? k}</td><td className="mono">{v}</td></tr>
              ))}
              {Object.keys(d.service_areas.by_type).length === 0 && (
                <tr><td className="muted small" colSpan={2}>暂无区划</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function OrgTreeNodeRow({ node, depth }: { node: OrgTreeNode; depth: number }) {
  const [open, setOpen] = useState(true);
  const hasChildren = node.children.length > 0;
  return (
    <>
      <tr>
        <td>
          <span style={{ paddingLeft: depth * 18, userSelect: "none" }}>
            {hasChildren ? (
              <button className="btn-ghost" style={{ padding: "0 6px" }} onClick={() => setOpen((o) => !o)}>
                {open ? "▾" : "▸"}
              </button>
            ) : <span style={{ display: "inline-block", width: 20 }} />}
            <b>{node.name}</b> <span className="muted small mono">{node.code}</span>
          </span>
        </td>
        <td><span className="tag">{node.type_label}</span></td>
        <td><span className={`tag tag-${PROPERTY_TAG[node.org_property] ?? "low"}`}>{node.org_property_label}</span></td>
        <td className="small">{node.manager_name || "-"}</td>
        <td className="mono">{node.direct_headcount}</td>
        <td className="mono"><b>{node.total_headcount}</b></td>
      </tr>
      {open && node.children.map((c) => <OrgTreeNodeRow key={c.id} node={c} depth={depth + 1} />)}
    </>
  );
}

const ORG_TYPES: Record<string, string> = {
  group: "集团", company: "公司", region: "片区", dept: "部门", station: "网点",
};

function OrgCreateForm({ orgs, onDone }: { orgs: OrgOption[]; onDone: () => void }) {
  const [form, setForm] = useState({
    code: "", name: "", short_name: "", type: "station", org_property: "self", parent: "", manager_name: "",
  });
  const set = (k: string, v: string) => setForm((f) => ({ ...f, [k]: v }));
  const create = useMutation({
    mutationFn: () => apiPost<unknown>("/org/organizations", { ...form, parent: form.parent || null }),
    onSuccess: () => {
      toast.success("组织已新增");
      setForm({ code: "", name: "", short_name: "", type: "station", org_property: "self", parent: "", manager_name: "" });
      onDone();
    },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <div className="panel">
      <div className="panel-head">新增组织</div>
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
        <input className="search" style={{ width: 110 }} placeholder="编码" value={form.code} onChange={(e) => set("code", e.target.value)} />
        <input className="search" style={{ width: 150 }} placeholder="名称" value={form.name} onChange={(e) => set("name", e.target.value)} />
        <input className="search" style={{ width: 100 }} placeholder="简称" value={form.short_name} onChange={(e) => set("short_name", e.target.value)} />
        <select value={form.type} onChange={(e) => set("type", e.target.value)}>
          {Object.entries(ORG_TYPES).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <select value={form.org_property} onChange={(e) => set("org_property", e.target.value)}>
          {Object.entries(ORG_PROPERTY_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <select value={form.parent} onChange={(e) => set("parent", e.target.value)}>
          <option value="">无上级（根）</option>
          {orgs.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
        </select>
        <input className="search" style={{ width: 90 }} placeholder="负责人" value={form.manager_name} onChange={(e) => set("manager_name", e.target.value)} />
        <button className="btn-primary" disabled={create.isPending || !form.code || !form.name} onClick={() => create.mutate()}>新增</button>
      </div>
    </div>
  );
}

function OrgTab() {
  const qc = useQueryClient();
  const orgs = useOrgOptions();
  const q = useQuery({
    queryKey: ["org-tree"],
    queryFn: () => apiGet<{ tree: OrgTreeNode[]; total: number }>("/org/organizations/tree"),
  });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["org-tree"] });
    qc.invalidateQueries({ queryKey: ["org-options"] });
    qc.invalidateQueries({ queryKey: ["org-overview"] });
  };
  return (
    <div className="stack">
    <OrgCreateForm orgs={orgs.data ?? []} onDone={refresh} />
    <div className="panel">
      <div className="panel-head">
        组织架构树
        <span className="ai-pill">含子树在职人数合计</span>
        <button className="btn-ghost" style={{ marginLeft: "auto" }} onClick={() => apiDownload("/org/organizations/export", "organizations.csv")}>导出 CSV</button>
      </div>
      {q.isLoading ? (
        <div className="muted" style={{ padding: 16 }}>加载中…</div>
      ) : (q.data?.tree.length ?? 0) === 0 ? (
        <div className="muted" style={{ padding: 16 }}>暂无组织。</div>
      ) : (
        <table className="table">
          <thead>
            <tr><th>组织</th><th>类型</th><th>属性</th><th>负责人</th><th>直属</th><th>子树合计</th></tr>
          </thead>
          <tbody>
            {q.data!.tree.map((n) => <OrgTreeNodeRow key={n.id} node={n} depth={0} />)}
          </tbody>
        </table>
      )}
    </div>
    </div>
  );
}

function EmployeeCreateForm({ orgs, onDone }: { orgs: OrgOption[]; onDone: () => void }) {
  const [form, setForm] = useState({ employee_no: "", name: "", phone: "", organization: "", position: "" });
  const set = (k: string, v: string) => setForm((f) => ({ ...f, [k]: v }));
  const create = useMutation({
    mutationFn: () => apiPost<Employee>("/org/employees", { ...form, organization: form.organization || null }),
    onSuccess: () => { toast.success("员工已新增"); setForm({ employee_no: "", name: "", phone: "", organization: "", position: "" }); onDone(); },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <div className="panel">
      <div className="panel-head">新增员工</div>
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
        <input className="search" style={{ width: 110 }} placeholder="工号" value={form.employee_no} onChange={(e) => set("employee_no", e.target.value)} />
        <input className="search" style={{ width: 110 }} placeholder="姓名" value={form.name} onChange={(e) => set("name", e.target.value)} />
        <input className="search" style={{ width: 130 }} placeholder="手机号" value={form.phone} onChange={(e) => set("phone", e.target.value)} />
        <select value={form.organization} onChange={(e) => set("organization", e.target.value)}>
          <option value="">选择所属组织</option>
          {orgs.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
        </select>
        <input className="search" style={{ width: 120 }} placeholder="职位" value={form.position} onChange={(e) => set("position", e.target.value)} />
        <button className="btn-primary" disabled={create.isPending || !form.employee_no || !form.name} onClick={() => create.mutate()}>新增</button>
      </div>
    </div>
  );
}

function EmployeesTab() {
  const qc = useQueryClient();
  const orgs = useOrgOptions();
  const [search, setSearch] = useState("");
  const q = useQuery({
    queryKey: ["org-employees", search],
    queryFn: () => apiGet<Paginated<Employee>>(`/org/employees?page_size=100&search=${encodeURIComponent(search)}`),
  });
  const handoverList = useQuery({
    queryKey: ["org-handovers"],
    queryFn: () => apiGet<Paginated<AccountHandover>>("/org/handovers?page_size=20"),
  });
  const employees = q.data?.items ?? [];

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["org-employees"] });
    qc.invalidateQueries({ queryKey: ["org-handovers"] });
    qc.invalidateQueries({ queryKey: ["org-overview"] });
  };

  const toggle = useMutation({
    mutationFn: ({ id, action }: { id: string; action: "disable" | "enable" }) =>
      apiPost<Employee>(`/org/employees/${id}/${action}`, {}),
    onSuccess: (_d, v) => { toast.success(v.action === "disable" ? "已停用" : "已启用"); invalidate(); },
  });
  const resetPwd = useMutation({
    mutationFn: (id: string) => apiPost<{ username: string; password: string }>(`/org/employees/${id}/reset-password`, {}),
    onSuccess: (d) => toast.success(`新密码（请复制）：${d.username} / ${d.password}`),
    onError: (e: Error) => toast.error(e.message),
  });
  const handover = useMutation({
    mutationFn: ({ id, to }: { id: string; to: string }) =>
      apiPost<AccountHandover>(`/org/employees/${id}/handover`, { to_employee: to, disable: true }),
    onSuccess: (d) => { toast.success(`移交完成：下属 ${d.moved_reports} 人、部门 ${d.moved_departments} 个已改挂`); invalidate(); },
    onError: (e: Error) => toast.error(e.message),
  });
  const importCsv = useMutation({
    mutationFn: (file: File) => {
      const fd = new FormData();
      fd.append("file", file);
      return apiUpload<{ created: number; updated: number; errors: Array<{ row: number; error: string }> }>("/org/employees/import", fd);
    },
    onSuccess: (d) => {
      toast.success(`导入完成：新增 ${d.created}、更新 ${d.updated}${d.errors.length ? `、失败 ${d.errors.length}` : ""}`);
      if (d.errors.length) toast.error(`首条失败：第 ${d.errors[0].row} 行 ${d.errors[0].error}`);
      invalidate();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const doDisable = async (e: Employee) => {
    if (await confirmAction({ title: "停用员工", message: `确认停用 ${e.name}（${e.employee_no}）？其登录账号将被禁用。`, tone: "danger", confirmText: "停用" }))
      toggle.mutate({ id: e.id, action: "disable" });
  };
  const doHandover = async (e: Employee) => {
    const candidates = employees.filter((x) => x.id !== e.id && x.status === "active");
    if (candidates.length === 0) { toast.error("无可接收的在职员工"); return; }
    const to = window.prompt(
      `将 ${e.name} 的下属与所辖部门移交给（输入工号）：\n` +
        candidates.map((c) => `${c.employee_no} ${c.name}`).join("\n")
    );
    if (!to) return;
    const target = candidates.find((c) => c.employee_no === to.trim());
    if (!target) { toast.error("未匹配到该工号"); return; }
    handover.mutate({ id: e.id, to: target.id });
  };

  return (
    <div className="stack">
      <EmployeeCreateForm orgs={orgs.data ?? []} onDone={invalidate} />
      <div className="panel">
        <div className="panel-head">
          员工名录
          <div className="form-row" style={{ marginLeft: "auto", gap: 6 }}>
            <label className="btn-ghost" style={{ cursor: "pointer" }}>
              {importCsv.isPending ? "导入中…" : "导入 CSV"}
              <input type="file" accept=".csv,text/csv" style={{ display: "none" }} onChange={(e) => { const f = e.target.files?.[0]; if (f) importCsv.mutate(f); e.target.value = ""; }} />
            </label>
            <button className="btn-ghost" onClick={() => apiDownload("/org/employees/export", "employees.csv")}>导出 CSV</button>
          </div>
        </div>
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
          <input className="search" placeholder="搜索工号/姓名/手机/职位" value={search} onChange={(e) => setSearch(e.target.value)} style={{ width: 240 }} />
          <span className="muted small">共 {q.data?.total ?? 0} 人</span>
        </div>
        <table className="table">
          <thead>
            <tr><th>工号</th><th>姓名</th><th>组织</th><th>职位</th><th>直接上级</th><th>账号</th><th>状态</th><th>操作</th></tr>
          </thead>
          <tbody>
            {employees.length === 0 && <tr><td colSpan={8} className="muted small">暂无员工。</td></tr>}
            {employees.map((e) => (
              <tr key={e.id}>
                <td className="mono">{e.employee_no}</td>
                <td><b>{e.name}</b><div className="muted small">{e.phone}</div></td>
                <td className="small">{e.organization_name || "-"}</td>
                <td className="small">{e.position || "-"}</td>
                <td className="small">{e.supervisor_name || "-"}</td>
                <td className="small">{e.username ? (e.account_active ? <span className="tag tag-low">{e.username}</span> : <span className="tag tag-medium">{e.username}·禁</span>) : <span className="muted">未绑定</span>}</td>
                <td><span className={`tag tag-${STATUS_TAG[e.status] ?? "low"}`}>{e.status_label}</span></td>
                <td>
                  <div className="form-row" style={{ gap: 4 }}>
                    {e.status === "active" ? (
                      <button className="btn-ghost small" onClick={() => doDisable(e)}>停用</button>
                    ) : (
                      <button className="btn-ghost small" onClick={() => toggle.mutate({ id: e.id, action: "enable" })}>启用</button>
                    )}
                    <button className="btn-ghost small" disabled={!e.user} onClick={() => resetPwd.mutate(e.id)}>重置密码</button>
                    <button className="btn-ghost small" onClick={() => doHandover(e)}>移交</button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {(handoverList.data?.items.length ?? 0) > 0 && (
        <div className="panel">
          <div className="panel-head">账号移交记录</div>
          <table className="table">
            <thead><tr><th>移交人</th><th>接收人</th><th>下属</th><th>部门</th><th>停用账号</th><th>原因</th><th>时间</th></tr></thead>
            <tbody>
              {handoverList.data!.items.map((h) => (
                <tr key={h.id}>
                  <td>{h.from_name}</td><td>{h.to_name}</td>
                  <td className="mono">{h.moved_reports}</td><td className="mono">{h.moved_departments}</td>
                  <td>{h.disabled_account ? "是" : "否"}</td>
                  <td className="small">{h.reason || "-"}</td>
                  <td className="small">{new Date(h.created_at).toLocaleString("zh-CN")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function CoverageRouter() {
  const [city, setCity] = useState("");
  const [district, setDistrict] = useState("");
  const m = useMutation({
    mutationFn: () =>
      apiGet<CoverageResult>(
        `/org/route-resolve?city=${encodeURIComponent(city)}&district=${encodeURIComponent(district)}`
      ),
  });
  return (
    <div className="panel">
      <div className="panel-head">
        区划路由
      </div>
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
        <input className="search" style={{ width: 130 }} placeholder="城市，如 上海市" value={city} onChange={(e) => setCity(e.target.value)} />
        <input className="search" style={{ width: 140 }} placeholder="区县，如 浦东新区" value={district} onChange={(e) => setDistrict(e.target.value)} />
        <button className="btn-primary" disabled={m.isPending || (!city && !district)} onClick={() => m.mutate()}>解析负责网点</button>
      </div>
      {m.data && (
        <div style={{ padding: "0 16px 14px" }} className="stack">
          <div className="muted small">目的地：{m.data.destination || "-"}</div>
          {m.data.resolved.length === 0 ? (
            <div className="muted small">无可承运网点{m.data.excluded.length > 0 ? "（均被排他规则排除）" : ""}。</div>
          ) : (
            <table className="table">
              <thead><tr><th>排名</th><th>网点</th><th>方式</th><th>命中区划</th><th>优先级</th><th>负责人</th></tr></thead>
              <tbody>
                {m.data.resolved.map((r, i) => (
                  <tr key={r.organization_id}>
                    <td className="mono">{i === 0 ? <span className="tag tag-low">首选</span> : i + 1}</td>
                    <td><b>{r.organization_name}</b></td>
                    <td><span className={`tag tag-${r.area_type === "deliver" ? "low" : "medium"}`}>{r.area_type_label}</span></td>
                    <td className="small">{r.region_name}</td>
                    <td className="mono">{r.priority}</td>
                    <td className="small">{r.manager_name || "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {m.data.excluded.length > 0 && (
            <div className="muted small">
              已排除：{m.data.excluded.map((e) => `${e.organization_name}（${e.reason}）`).join("、")}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function AreaCreateForm({ orgs, onDone }: { orgs: OrgOption[]; onDone: () => void }) {
  const [org, setOrg] = useState("");
  const [areaType, setAreaType] = useState("deliver");
  const [regionName, setRegionName] = useState("");
  const [priority, setPriority] = useState(10);
  const create = useMutation({
    mutationFn: () =>
      apiPost<ServiceArea>("/org/service-areas", {
        organization: org, area_type: areaType, region_name: regionName, priority,
      }),
    onSuccess: () => { toast.success("区划已新增"); setRegionName(""); onDone(); },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <div className="panel">
      <div className="panel-head">新增服务区划</div>
      <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
        <select value={org} onChange={(e) => setOrg(e.target.value)}>
          <option value="">选择归属网点</option>
          {orgs.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
        </select>
        <select value={areaType} onChange={(e) => setAreaType(e.target.value)}>
          {Object.entries(AREA_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <input className="search" style={{ width: 200 }} placeholder="区划名，如 上海市浦东新区" value={regionName} onChange={(e) => setRegionName(e.target.value)} />
        <input className="search" style={{ width: 90 }} type="number" placeholder="优先级" value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
        <button className="btn-primary" disabled={create.isPending || !org || !regionName} onClick={() => create.mutate()}>新增</button>
      </div>
    </div>
  );
}

function AreasTab() {
  const qc = useQueryClient();
  const orgs = useOrgOptions();
  const q = useQuery({
    queryKey: ["org-areas"],
    queryFn: () => apiGet<Paginated<ServiceArea>>("/org/service-areas?page_size=200"),
  });
  const grouped = useMemo(() => {
    const m: Record<string, ServiceArea[]> = {};
    for (const a of q.data?.items ?? []) (m[a.area_type] ??= []).push(a);
    return m;
  }, [q.data]);
  const types = ["deliver", "transfer", "special", "no_deliver", "no_transfer"];
  return (
    <div className="stack">
      <CoverageRouter />
      <AreaCreateForm orgs={orgs.data ?? []} onDone={() => qc.invalidateQueries({ queryKey: ["org-areas"] })} />
      <div className="muted small">网点服务区划：派送、中转、特殊、不派送、不中转五类，用于接单与派单的覆盖路由。</div>
      <div className="ct-grid">
        {types.filter((t) => grouped[t]?.length).map((t) => (
          <div className="panel" key={t}>
            <div className="panel-head">{AREA_TYPE_LABEL[t]} · {grouped[t].length}</div>
            <table className="table">
              <thead><tr><th>区划</th><th>归属网点</th><th>优先级</th></tr></thead>
              <tbody>
                {grouped[t].map((a) => (
                  <tr key={a.id}>
                    <td>{a.region_name}</td>
                    <td className="small">{a.organization_name}</td>
                    <td className="mono">{a.priority}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}
        {(q.data?.items.length ?? 0) === 0 && (
          <div className="muted" style={{ padding: 16 }}>暂无服务区划。</div>
        )}
      </div>
    </div>
  );
}

const SCOPE_LABEL: Record<string, string> = {
  self: "仅本人", org: "本组织", org_sub: "本组织及下级", all: "全部",
};

function RbacTab() {
  const q = useQuery({ queryKey: ["rbac-matrix"], queryFn: () => apiGet<RbacMatrix>("/org/rbac/matrix") });
  // 本地草稿：roleId -> Set(permission code)
  const [draft, setDraft] = useState<Record<string, Set<string>> | null>(null);
  const matrix = q.data;
  const codeToId = useMemo(() => {
    const m: Record<string, string> = {};
    for (const g of matrix?.modules ?? []) for (const p of g.permissions) m[p.code] = p.id;
    return m;
  }, [matrix]);
  const state = useMemo(() => {
    if (draft) return draft;
    const m: Record<string, Set<string>> = {};
    for (const r of matrix?.roles ?? []) m[r.id] = new Set(r.permission_codes);
    return m;
  }, [draft, matrix]);

  const toggle = (roleId: string, code: string) => {
    setDraft(() => {
      const next: Record<string, Set<string>> = {};
      for (const [k, v] of Object.entries(state)) next[k] = new Set(v);
      next[roleId] ??= new Set();
      if (next[roleId].has(code)) next[roleId].delete(code);
      else next[roleId].add(code);
      return next;
    });
  };

  const save = useMutation({
    mutationFn: async () => {
      for (const role of matrix?.roles ?? []) {
        const codes = [...(state[role.id] ?? new Set())];
        const ids = codes.map((c) => codeToId[c]).filter(Boolean);
        await apiPost(`/org/roles/${role.id}/set-permissions`, { permissions: ids });
      }
    },
    onSuccess: () => { toast.success("权限矩阵已保存"); setDraft(null); q.refetch(); },
    onError: (e: Error) => toast.error(e.message),
  });

  if (!matrix) return <div className="muted" style={{ padding: 16 }}>加载中…</div>;
  if (matrix.roles.length === 0)
    return <div className="muted" style={{ padding: 16 }}>暂无角色。</div>;

  return (
    <div className="panel">
      <div className="panel-head">
        角色 × 权限矩阵
        <span className="ai-pill">{matrix.roles.length} 角色 · {matrix.permission_total} 权限点</span>
        <button className="btn-primary" style={{ marginLeft: "auto" }} disabled={save.isPending || !draft} onClick={() => save.mutate()}>
          {save.isPending ? "保存中…" : "保存矩阵"}
        </button>
      </div>
      <div style={{ overflowX: "auto" }}>
        <table className="table">
          <thead>
            <tr>
              <th style={{ minWidth: 180 }}>权限点</th>
              {matrix.roles.map((r) => (
                <th key={r.id} style={{ textAlign: "center" }}>
                  {r.name}
                  <div className="muted small">{SCOPE_LABEL[r.data_scope] ?? r.data_scope}</div>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {matrix.modules.map((g) => (
              <Fragment key={g.module}>
                <tr>
                  <td colSpan={matrix.roles.length + 1} className="muted small" style={{ background: "var(--panel-2, rgba(255,255,255,0.03))", fontWeight: 600 }}>
                    {g.module}
                  </td>
                </tr>
                {g.permissions.map((p) => (
                  <tr key={p.id}>
                    <td>{p.name} <span className="muted small mono">{p.code}</span></td>
                    {matrix.roles.map((r) => (
                      <td key={r.id} style={{ textAlign: "center" }}>
                        <input
                          type="checkbox"
                          checked={state[r.id]?.has(p.code) ?? false}
                          onChange={() => toggle(r.id, p.code)}
                        />
                      </td>
                    ))}
                  </tr>
                ))}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

interface LoginAttempt {
  id: string; username: string; success: boolean; result: string; result_label: string;
  ip: string; user_agent: string; created_at: string;
}

function LoginAuditTab() {
  const [only, setOnly] = useState<"" | "success" | "fail">("");
  const qs = only === "success" ? "&success=true" : only === "fail" ? "&success=false" : "";
  const q = useQuery({
    queryKey: ["login-audit", only],
    queryFn: () => apiGet<Paginated<LoginAttempt>>(`/org/login-audit?page_size=100&ordering=-created_at${qs}`),
    refetchInterval: 30000,
  });
  const rows = q.data?.items ?? [];
  const fails = rows.filter((r) => !r.success).length;
  return (
    <div className="panel">
      <div className="panel-head">
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>登录审计<span className="ai-pill">{rows.length} 条 · 失败 {fails}</span></span>
        <div className="panel-actions">
          <button className={`chip${only === "" ? " chip-on" : ""}`} onClick={() => setOnly("")}>全部</button>
          <button className={`chip${only === "success" ? " chip-on" : ""}`} onClick={() => setOnly("success")}>成功</button>
          <button className={`chip${only === "fail" ? " chip-on" : ""}`} onClick={() => setOnly("fail")}>失败</button>
        </div>
      </div>
      {q.isLoading ? (
        <div className="muted" style={{ padding: 16 }}>加载中…</div>
      ) : rows.length === 0 ? (
        <div className="muted" style={{ padding: 16 }}>暂无登录记录。</div>
      ) : (
        <table className="table">
          <thead><tr><th>时间</th><th>用户名</th><th>结果</th><th>IP</th><th>客户端</th></tr></thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id}>
                <td className="mono small">{new Date(r.created_at).toLocaleString("zh-CN")}</td>
                <td>{r.username}</td>
                <td><span className={`tag ${r.success ? "tag-low" : "tag-high"}`}>{r.result_label || (r.success ? "成功" : "失败")}</span></td>
                <td className="mono small">{r.ip || "-"}</td>
                <td className="small muted" style={{ maxWidth: 280, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={r.user_agent}>{r.user_agent || "-"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

const TABS: { key: Tab; label: string; perm?: string }[] = [
  { key: "overview", label: "运营总览" },
  { key: "org", label: "组织架构" },
  { key: "employees", label: "员工名录" },
  { key: "areas", label: "服务区划" },
  { key: "rbac", label: "权限授权", perm: "org.rbac" },
  { key: "audit", label: "登录审计", perm: "org.view" },
];

export function OrgCenterPage() {
  const { user } = useAuth();
  const [tab, setTab] = useState<Tab>("overview");
  // 无角色权限管理权的用户看不到「权限授权」页签（后端亦 403 兜底）
  const tabs = TABS.filter((t) => !t.perm || hasPerm(user, t.perm));
  return (
    <div className="stack">
      <div className="seg-tabs">
        {tabs.map((t) => (
          <button key={t.key} className={tab === t.key ? "active" : ""} onClick={() => setTab(t.key)}>{t.label}</button>
        ))}
      </div>
      {tab === "overview" && <OverviewTab />}
      {tab === "org" && <OrgTab />}
      {tab === "employees" && <EmployeesTab />}
      {tab === "areas" && <AreasTab />}
      {tab === "rbac" && <RbacTab />}
      {tab === "audit" && <LoginAuditTab />}
    </div>
  );
}
