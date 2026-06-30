import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { toast } from "../api/toast";
import type {
  AccountHandover,
  Employee,
  OrgOverview,
  OrgTreeNode,
  Paginated,
  ServiceArea,
} from "../api/types";
import { AREA_TYPE_LABEL, EMP_STATUS_LABEL, ORG_PROPERTY_LABEL } from "../api/types";

type Tab = "overview" | "org" | "employees" | "areas";

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
                <tr><td className="muted small" colSpan={2}>暂无区划，执行 seed_org 生成演示数据</td></tr>
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

function OrgTab() {
  const q = useQuery({
    queryKey: ["org-tree"],
    queryFn: () => apiGet<{ tree: OrgTreeNode[]; total: number }>("/org/organizations/tree"),
  });
  return (
    <div className="panel">
      <div className="panel-head">
        组织架构树
        <span className="ai-pill">含子树在职人头汇总</span>
      </div>
      {q.isLoading ? (
        <div className="muted" style={{ padding: 16 }}>加载中…</div>
      ) : (q.data?.tree.length ?? 0) === 0 ? (
        <div className="muted" style={{ padding: 16 }}>暂无组织，执行 <code>python manage.py seed_org</code> 生成演示数据。</div>
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
  );
}

function EmployeesTab() {
  const qc = useQueryClient();
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
      <div className="panel">
        <div className="panel-head">员工名录 · 汇报线 + 账号生命周期</div>
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
          <input className="search" placeholder="搜索工号/姓名/手机/职位" value={search} onChange={(e) => setSearch(e.target.value)} style={{ width: 240 }} />
          <span className="muted small">共 {q.data?.total ?? 0} 人</span>
        </div>
        <table className="table">
          <thead>
            <tr><th>工号</th><th>姓名</th><th>组织</th><th>职位</th><th>直接上级</th><th>账号</th><th>状态</th><th>操作</th></tr>
          </thead>
          <tbody>
            {employees.length === 0 && <tr><td colSpan={8} className="muted small">暂无员工，执行 seed_org 生成演示数据。</td></tr>}
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

function AreasTab() {
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
      <div className="muted small">网点服务区划：决定智能接单与派单的覆盖路由——派送/中转/特殊/不派送/不中转五类。</div>
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
          <div className="muted" style={{ padding: 16 }}>暂无服务区划，执行 seed_org 生成演示数据。</div>
        )}
      </div>
    </div>
  );
}

const TABS: { key: Tab; label: string }[] = [
  { key: "overview", label: "经营总览" },
  { key: "org", label: "组织架构" },
  { key: "employees", label: "员工名录" },
  { key: "areas", label: "服务区划" },
];

export function OrgCenterPage() {
  const [tab, setTab] = useState<Tab>("overview");
  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">
          组织中台 · 企业管理中枢
          <span className="ai-pill">超越 G7 · 汇报线 · 账号移交 · 区划路由</span>
        </div>
        <div className="form-row" style={{ gap: 6, padding: "10px 16px" }}>
          {TABS.map((t) => (
            <button
              key={t.key}
              className={tab === t.key ? "btn-primary" : "btn-ghost"}
              onClick={() => setTab(t.key)}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>
      {tab === "overview" && <OverviewTab />}
      {tab === "org" && <OrgTab />}
      {tab === "employees" && <EmployeesTab />}
      {tab === "areas" && <AreasTab />}
    </div>
  );
}
