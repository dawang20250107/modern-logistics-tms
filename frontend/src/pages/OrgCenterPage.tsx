import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Fragment, useMemo, useRef, useState } from "react";

import { apiDownload, apiGet, apiPost, apiUpload } from "../api/client";
import { fmtDateTime, fmtNum0, EMPTY } from "../api/format";
import { confirmAction } from "../api/confirm";
import { hasPerm, useAuth } from "../auth/auth";
import { toast } from "../api/toast";
import { useModalA11y } from "../api/useModalA11y";
import type {
  AccountHandover,
  CoverageResult,
  Employee,
  OrgOption,
  OrgOverview,
  OrgTreeNode,
  Paginated,
  RbacMatrix,
  Role,
  RoleAssignment,
  ServiceArea,
} from "../api/types";
import { AREA_TYPE_LABEL, ORG_PROPERTY_LABEL } from "../api/types";
import { StateView } from "../components/StateView";

function useOrgOptions() {
  return useQuery({
    queryKey: ["org-options"],
    queryFn: () => apiGet<Paginated<OrgOption>>("/org/organizations?page_size=200&ordering=sort_order"),
    select: (d) => d.items,
  });
}

type Tab = "overview" | "org" | "employees" | "areas" | "rbac" | "audit";

/** 权限点所属模块的中文名。
 *
 * 后端 iam_permission.module 存的是英文 slug（finance / waybill / …），
 * 矩阵直接把它印在分组行上——整页都是中文，只有这两行是 `finance`、`waybill`。
 * 这和之前订单状态漏出 `pending_confirm` 是同一类问题：内部标识符走到了界面上。
 * 未知模块保留原值（不是所有部署的权限表都一样），但至少已知的这些要说人话。
 */
const PERM_MODULE_LABEL: Record<string, string> = {
  waybill: "运单", order: "订单", finance: "财务结算", analytics: "经营分析",
  carrier: "承运商", masterdata: "主数据", telematics: "车联网", org: "组织与权限",
  audit: "审计", exception: "异常", dispatch: "调度", 通用: "通用",
};

const STATUS_TAG: Record<string, string> = { active: "low", disabled: "medium", left: "high" };
const PROPERTY_TAG: Record<string, string> = {
  self: "low", franchise: "medium", outsource: "medium", partner: "low", jv: "medium",
};

/** 组织中心的存量计数（页签角标用）。
 *
 * 这些数原先撑起一个「运营总览」页签：六个读数 + 两张各只有一行的表 + 700px 空白，
 * 而且是默认落地页。更糟的是排版——`.kv` 两列铺满 1490px 宽的页面后，
 * 「组织总数」和它的「4」隔着 700px 的空白，眼睛得横扫一整屏才能把标签和数字连起来。
 *
 * by_property / by_type 这两张分布表也是多余的：组织架构树每行都带经营属性标签，
 * 服务区划表每行都带类型列——分布在原表里一眼可数，不需要另开一页复述。
 */
function useOrgCounts() {
  const q = useQuery({ queryKey: ["org-overview"], queryFn: () => apiGet<OrgOverview>("/org/overview") });
  const d = q.data;
  return {
    counts: {
      org: d?.organizations.total, employees: d?.employees.total, areas: d?.service_areas.total,
    } as Record<string, number | undefined>,
    noAccount: d?.employees.active_without_account ?? 0,
  };
}

/** 折叠式「新增 X」面板。
 *
 * 组织架构 / 员工名录 / 服务区划三个页签，原先每页顶部都常驻一张展开的新增表单，
 * 各占 160–230px，把真正要看的名录推到折叠线以下。而新增组织这种事一年做几次，
 * 看名录是每天做几十次——版面按频次排，不按 CRUD 的字母序排。
 * 收起时只留一条 33px 的触发条，展开后表单原样。
 */
function CreatePanel({ title, children, actions }: { title: string; children: React.ReactNode; actions: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="panel">
      <div className="panel-head">
        {title}
        <div className="panel-actions">
          <button className="btn-ghost small" onClick={() => setOpen((v) => !v)}>{open ? "收起" : `+ ${title}`}</button>
        </div>
      </div>
      {open && <>
        <div className="grid-form" style={{ padding: "16px 18px" }}>{children}</div>
        <div className="form-actions">{actions}</div>
      </>}
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
        <td className="small">{node.manager_name || EMPTY}</td>
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
    <CreatePanel title="新增组织" actions={<button className="btn-primary" disabled={create.isPending || !form.code || !form.name} onClick={() => create.mutate()} title={!form.code ? "请填写编码" : !form.name ? "请填写名称" : undefined}>新增组织</button>}>
        <label>编码 *<input value={form.code} onChange={(e) => set("code", e.target.value)} placeholder="如 SH01" /></label>
        <label>名称 *<input value={form.name} onChange={(e) => set("name", e.target.value)} placeholder="如 上海分公司" /></label>
        <label>简称<input value={form.short_name} onChange={(e) => set("short_name", e.target.value)} placeholder="如 上海" /></label>
        <label>类型
          <select value={form.type} onChange={(e) => set("type", e.target.value)}>
            {Object.entries(ORG_TYPES).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
        </label>
        <label>经营属性
          <select value={form.org_property} onChange={(e) => set("org_property", e.target.value)}>
            {Object.entries(ORG_PROPERTY_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
        </label>
        <label>上级组织
          <select value={form.parent} onChange={(e) => set("parent", e.target.value)}>
            <option value="">无上级（根）</option>
            {orgs.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
          </select>
        </label>
        <label>负责人<input value={form.manager_name} onChange={(e) => set("manager_name", e.target.value)} placeholder="负责人姓名" /></label>
    </CreatePanel>
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
        <StateView kind="loading" compact />
      ) : q.isError ? (
        <StateView kind="error" hint="组织架构暂时无法加载。" onRetry={() => q.refetch()} compact />
      ) : (q.data?.tree.length ?? 0) === 0 ? (
        <StateView kind="empty" title="暂无组织" />
      ) : (
        <div className="table-wrap">
        <table className="table">
          <thead>
            <tr><th>组织</th><th>类型</th><th>属性</th><th>负责人</th><th>直属</th><th>子树合计</th></tr>
          </thead>
          <tbody>
            {q.data!.tree.map((n) => <OrgTreeNodeRow key={n.id} node={n} depth={0} />)}
          </tbody>
        </table>
        </div>
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
    <CreatePanel title="新增员工" actions={<button className="btn-primary" disabled={create.isPending || !form.employee_no || !form.name} onClick={() => create.mutate()} title={!form.employee_no ? "请填写工号" : !form.name ? "请填写姓名" : undefined}>新增员工</button>}>
        <label>工号 *<input value={form.employee_no} onChange={(e) => set("employee_no", e.target.value)} placeholder="如 2026001" /></label>
        <label>姓名 *<input value={form.name} onChange={(e) => set("name", e.target.value)} placeholder="员工姓名" /></label>
        <label>手机号<input value={form.phone} onChange={(e) => set("phone", e.target.value)} placeholder="手机号" /></label>
        <label>所属组织
          <select value={form.organization} onChange={(e) => set("organization", e.target.value)}>
            <option value="">选择所属组织</option>
            {orgs.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
          </select>
        </label>
        <label>职位<input value={form.position} onChange={(e) => set("position", e.target.value)} placeholder="如 调度专员" /></label>
    </CreatePanel>
  );
}

// 给员工分配角色（落到其登录账号的 RoleAssignment）——权限管理闭环的用户侧。
function RoleAssignModal({ emp, onClose }: { emp: Employee; onClose: () => void }) {
  const qc = useQueryClient();
  const roles = useQuery({ queryKey: ["roles-all"], queryFn: () => apiGet<Paginated<Role>>("/org/roles?page_size=100") });
  const current = useQuery({ queryKey: ["emp-roles", emp.id], queryFn: () => apiGet<RoleAssignment[]>(`/org/employees/${emp.id}/roles`) });
  const [sel, setSel] = useState<Set<string> | null>(null);
  const state = sel ?? new Set((current.data ?? []).map((a) => a.role));
  const modalRef = useRef<HTMLDivElement>(null);
  useModalA11y(true, modalRef, onClose);

  const toggle = (id: string) => setSel(() => { const n = new Set(state); n.has(id) ? n.delete(id) : n.add(id); return n; });
  const save = useMutation({
    mutationFn: () => apiPost(`/org/employees/${emp.id}/roles`, { roles: [...state] }),
    onSuccess: () => {
      toast.success(`已更新 ${emp.name} 的角色`);
      qc.invalidateQueries({ queryKey: ["org-employees"] });
      qc.invalidateQueries({ queryKey: ["emp-roles", emp.id] });
      onClose();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div ref={modalRef} tabIndex={-1} role="dialog" aria-modal="true" aria-label="分配角色" className="modal-card" style={{ width: "min(520px, 94vw)" }} onClick={(e) => e.stopPropagation()}>
        <div className="bd-head">
          <div>
            <div className="bd-title">分配角色</div>
            <div className="muted small" style={{ marginTop: 3 }}>{emp.name}（{emp.employee_no}）· {emp.username || "未绑定账号"}</div>
          </div>
          <button className="btn-ghost" onClick={onClose}>关闭 [Esc]</button>
        </div>
        <div className="bd-body">
          {!emp.user ? (
            <StateView kind="empty" title="该员工尚未绑定登录账号" hint="绑定账号后方可分配角色。" />
          ) : roles.isLoading ? <StateView kind="loading" compact /> : (
            <div className="stack" style={{ gap: 8 }}>
              {(roles.data?.items ?? []).map((r) => (
                <label key={r.id} className="role-pick">
                  <input type="checkbox" checked={state.has(r.id)} onChange={() => toggle(r.id)} />
                  <span className="role-pick-body">
                    <b>{r.name}</b> <span className="muted small mono">{r.code}</span>
                    <span className="muted small"> · 数据范围 {r.data_scope_label} · {r.permission_count} 权限点</span>
                  </span>
                </label>
              ))}
              {(roles.data?.items ?? []).length === 0 && <StateView kind="empty" title="暂无角色" hint="先在「权限授权」建立角色并授予权限点。" />}
            </div>
          )}
        </div>
        <div className="bd-foot">
          <span className="muted small">角色决定该账号可用功能与数据范围</span>
          <div style={{ flex: 1 }} />
          <button className="btn-ghost" onClick={onClose}>取消</button>
          <button className="btn-primary" disabled={!emp.user || save.isPending} onClick={() => save.mutate()}>{save.isPending ? "保存中…" : "保存角色"}</button>
        </div>
      </div>
    </div>
  );
}

function EmployeesTab() {
  const qc = useQueryClient();
  const orgs = useOrgOptions();
  const [search, setSearch] = useState("");
  const [roleEmp, setRoleEmp] = useState<Employee | null>(null);
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
    // 明文凭证不能用自动消失的 toast（易错过），改用需手动确认的弹窗，并尝试写入剪贴板
    onSuccess: (d) => {
      navigator.clipboard?.writeText(`${d.username} / ${d.password}`).catch(() => {});
      confirmAction({
        title: "密码已重置",
        message: `账号：${d.username}\n新密码：${d.password}\n\n已尝试复制到剪贴板，请立即转交本人并妥善保管（本弹窗关闭后不再显示）。`,
        confirmText: "我已复制保存",
      });
    },
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
            <label className="btn-ghost file-trigger" style={{ cursor: "pointer" }}>
              {importCsv.isPending ? "导入中…" : "导入 CSV"}
              <input className="file-input-accessible" type="file" accept=".csv,text/csv" disabled={importCsv.isPending} onChange={(e) => { const f = e.target.files?.[0]; if (f) importCsv.mutate(f); e.target.value = ""; }} />
            </label>
            <button className="btn-ghost" onClick={() => apiDownload("/org/employees/export", "employees.csv")}>导出 CSV</button>
          </div>
        </div>
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
          <input className="search" placeholder="搜索工号/姓名/手机/职位" value={search} onChange={(e) => setSearch(e.target.value)} style={{ width: 240 }} />
          <span className="muted small">共 {q.data?.total ?? 0} 人</span>
        </div>
        <div className="table-wrap">
        <table className="table">
          <thead>
            <tr><th>工号</th><th>姓名</th><th>组织</th><th>职位</th><th>角色</th><th>账号</th><th>状态</th><th>操作</th></tr>
          </thead>
          <tbody>
            {employees.length === 0 && <tr><td colSpan={8} className="muted small">暂无员工。</td></tr>}
            {employees.map((e) => (
              <tr key={e.id}>
                <td className="mono">{e.employee_no}</td>
                <td><b>{e.name}</b><div className="muted small">{e.phone}</div></td>
                <td className="small">{e.organization_name || EMPTY}</td>
                <td className="small">{e.position || EMPTY}</td>
                <td className="small">{(e.role_names ?? []).length > 0 ? (e.role_names ?? []).map((r) => <span key={r} className="tag tag-info" style={{ marginRight: 3 }}>{r}</span>) : <span className="muted">未分配</span>}</td>
                <td className="small">{e.username ? (e.account_active ? <span className="tag tag-low">{e.username}</span> : <span className="tag tag-medium">{e.username}·禁</span>) : <span className="muted">未绑定</span>}</td>
                <td><span className={`tag tag-${STATUS_TAG[e.status] ?? "low"}`}>{e.status_label}</span></td>
                <td>
                  <div className="form-row" style={{ gap: 4 }}>
                    <button className="btn-ghost small" disabled={!e.user} onClick={() => setRoleEmp(e)}>分配角色</button>
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
      </div>

      {(handoverList.data?.items.length ?? 0) > 0 && (
        <div className="panel">
          <div className="panel-head">账号移交记录</div>
          <div className="table-wrap">
          <table className="table">
            <thead><tr><th>移交人</th><th>接收人</th><th>下属</th><th>部门</th><th>停用账号</th><th>原因</th><th>时间</th></tr></thead>
            <tbody>
              {handoverList.data!.items.map((h) => (
                <tr key={h.id}>
                  <td>{h.from_name}</td><td>{h.to_name}</td>
                  <td className="mono">{h.moved_reports}</td><td className="mono">{h.moved_departments}</td>
                  <td>{h.disabled_account ? "是" : "否"}</td>
                  <td className="small">{h.reason || EMPTY}</td>
                  <td className="small">{fmtDateTime(h.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        </div>
      )}

      {roleEmp && <RoleAssignModal emp={roleEmp} onClose={() => setRoleEmp(null)} />}
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
          <div className="muted small">目的地：{m.data.destination || EMPTY}</div>
          {m.data.resolved.length === 0 ? (
            <div className="muted small">无可承运网点{m.data.excluded.length > 0 ? "（均被排他规则排除）" : ""}。</div>
          ) : (
            <div className="table-wrap">
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
                    <td className="small">{r.manager_name || EMPTY}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            </div>
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
    <CreatePanel title="新增服务区划" actions={<button className="btn-primary" disabled={create.isPending || !org || !regionName} onClick={() => create.mutate()} title={!org ? "请选择组织" : !regionName ? "请填写区划名称" : undefined}>新增区划</button>}>
        <label>归属网点 *
          <select value={org} onChange={(e) => setOrg(e.target.value)}>
            <option value="">选择归属网点</option>
            {orgs.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
          </select>
        </label>
        <label>区划类型
          <select value={areaType} onChange={(e) => setAreaType(e.target.value)}>
            {Object.entries(AREA_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
        </label>
        <label>区划名称 *<input placeholder="如 上海市浦东新区" value={regionName} onChange={(e) => setRegionName(e.target.value)} /></label>
        <label>优先级<input type="number" placeholder="数值大者优先" value={priority} onChange={(e) => setPriority(Number(e.target.value))} /></label>
    </CreatePanel>
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
          <StateView kind="empty" title="暂无服务区划" />
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

  if (q.isLoading) return <StateView kind="loading" />;
  if (q.isError || !matrix) return <StateView kind="error" hint="权限矩阵暂时无法加载。" onRetry={() => q.refetch()} />;
  if (matrix.roles.length === 0)
    return <StateView kind="empty" title="暂无角色" />;

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
                  <td colSpan={matrix.roles.length + 1} className="muted small" style={{ background: "var(--panel-2)", fontWeight: 600 }}>
                    {PERM_MODULE_LABEL[g.module] ?? g.module}
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
  // 「共多少条、其中失败多少」必须问服务端全量，不能数当前这一页。
  //
  // 原先是 rows.length 和 rows.filter(!success).length——数的是取回来的 100 条。
  // 实测库里 528 条、失败 402 时，界面写的是「100 条 · 失败 80」。
  // 这里是**安全审计**页：有人正是来看有没有人在爆破的，
  // 把 402 次失败显示成 80 次，比不显示更坏——它给了一个"看起来还好"的假象。
  //
  // 取数用 page_size=1 只读 total，跟资源库总览一个路子：
  // 不新开端点，就没有第二份数据范围逻辑要跟着演化。
  const totalQ = useQuery({
    queryKey: ["login-audit-total", only],
    queryFn: () => apiGet<Paginated<LoginAttempt>>(`/org/login-audit?page_size=1${qs}`),
    refetchInterval: 30000,
  });
  const failQ = useQuery({
    queryKey: ["login-audit-fails"],
    queryFn: () => apiGet<Paginated<LoginAttempt>>("/org/login-audit?page_size=1&success=false"),
    refetchInterval: 30000,
  });
  const rows = q.data?.items ?? [];
  const total = totalQ.data?.total ?? 0;
  const fails = failQ.data?.total ?? 0;
  const truncated = total > rows.length;
  return (
    <div className="panel">
      <div className="panel-head">
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
          登录审计
          <span className="ai-pill">共 {fmtNum0(total)} 条 · 失败 {fmtNum0(fails)}</span>
          {/* 列表只列最近 100 条。写出来，免得"共 528 条"配着 100 行看起来像丢了数据 */}
          {truncated && <span className="muted small">下表为最近 {rows.length} 条</span>}
        </span>
        <div className="panel-actions">
          <button className={`chip${only === "" ? " chip-on" : ""}`} onClick={() => setOnly("")}>全部</button>
          <button className={`chip${only === "success" ? " chip-on" : ""}`} onClick={() => setOnly("success")}>成功</button>
          <button className={`chip${only === "fail" ? " chip-on" : ""}`} onClick={() => setOnly("fail")}>失败</button>
        </div>
      </div>
      {q.isLoading ? (
        <StateView kind="loading" compact />
      ) : q.isError ? (
        <StateView kind="error" hint="登录记录暂时无法加载。" onRetry={() => q.refetch()} compact />
      ) : rows.length === 0 ? (
        <StateView kind="empty" title="暂无登录记录" />
      ) : (
        <div className="table-wrap">
        <table className="table">
          <thead><tr><th>时间</th><th>用户名</th><th>结果</th><th>IP</th><th>客户端</th></tr></thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id}>
                <td className="mono small">{fmtDateTime(r.created_at)}</td>
                <td>{r.username}</td>
                <td><span className={`tag ${r.success ? "tag-low" : "tag-high"}`}>{r.result_label || (r.success ? "成功" : "失败")}</span></td>
                <td className="mono small">{r.ip || EMPTY}</td>
                <td className="small muted" style={{ maxWidth: 280, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={r.user_agent}>{r.user_agent || EMPTY}</td>
              </tr>
            ))}
          </tbody>
        </table>
        </div>
      )}
    </div>
  );
}

const TABS: { key: Tab; label: string; perm?: string }[] = [
  { key: "org", label: "组织架构" },
  { key: "employees", label: "员工名录" },
  { key: "areas", label: "服务区划" },
  { key: "rbac", label: "权限授权", perm: "org.rbac" },
  { key: "audit", label: "登录审计", perm: "org.view" },
];

export function OrgCenterPage() {
  const { user } = useAuth();
  const [tab, setTab] = useState<Tab>("org");
  const { counts, noAccount } = useOrgCounts();
  // 无角色权限管理权的用户看不到「权限授权」页签（后端亦 403 兜底）
  const tabs = TABS.filter((t) => !t.perm || hasPerm(user, t.perm));
  return (
    <div className="stack">
      <div className="seg-tabs">
        {tabs.map((t) => (
          <button key={t.key} className={tab === t.key ? "active" : ""} onClick={() => setTab(t.key)}>
            {t.label}
            {counts[t.key] !== undefined && <span className="seg-n">{counts[t.key]}</span>}
          </button>
        ))}
      </div>
      {/* 「在职但没有账号」是待办不是统计：这人来上班了却登不进系统。
          它必须跨页签常驻，藏进某个页签等于没提醒。 */}
      {noAccount > 0 && tab !== "employees" && (
        <button type="button" className="rh-alert" onClick={() => setTab("employees")}>
          <span><b>{noAccount}</b> 名在职员工尚未开通系统账号，去员工名录开通 →</span>
        </button>
      )}
      {tab === "org" && <OrgTab />}
      {tab === "employees" && <EmployeesTab />}
      {tab === "areas" && <AreasTab />}
      {tab === "rbac" && <RbacTab />}
      {tab === "audit" && <LoginAuditTab />}
    </div>
  );
}
