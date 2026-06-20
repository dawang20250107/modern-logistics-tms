import { NavLink, Outlet } from "react-router-dom";

import { useAuth } from "../auth/auth";
import { NotificationBell } from "./NotificationBell";

type NavItem = { to: string; label: string; icon: string; end?: boolean; adminOnly?: boolean };
type NavGroup = { title: string; items: NavItem[] };

const NAV_GROUPS: NavGroup[] = [
  {
    title: "运营",
    items: [
      { to: "/", label: "控制塔", icon: "🗼", end: true },
      { to: "/intake", label: "建单", icon: "📝" },
      { to: "/dispatch-board", label: "调度台", icon: "🎯" },
      { to: "/waybills", label: "运单", icon: "🚚" },
      { to: "/command", label: "指挥中心", icon: "🛰️" },
    ],
  },
  {
    title: "在途",
    items: [
      { to: "/monitor", label: "监控", icon: "📍" },
      { to: "/fleet", label: "车队", icon: "🚛" },
      { to: "/alerts", label: "报警", icon: "🚨" },
      { to: "/exceptions", label: "异常", icon: "⚠️" },
    ],
  },
  {
    title: "财务",
    items: [
      { to: "/reconciliation", label: "对账", icon: "🧾" },
      { to: "/pricing", label: "合同价", icon: "💰" },
      { to: "/dashboard", label: "看板", icon: "📊" },
    ],
  },
  {
    title: "系统",
    items: [
      { to: "/catalog", label: "资产", icon: "🗂️" },
      { to: "/ai", label: "AI", icon: "🤖" },
      { to: "/audit", label: "审计", icon: "🛡️", adminOnly: true },
    ],
  },
];

export function AppLayout() {
  const { user, logout } = useAuth();
  const canSee = (item: NavItem) => !item.adminOnly || user?.is_staff || user?.is_superuser;
  return (
    <div className="app">
      <aside className="side">
        <div className="brand">
          <span className="brand-mark">智</span>
          <span className="brand-text">智运 TMS<span className="brand-sub">成为世界级物贸生态集团</span></span>
        </div>
        <nav className="nav">
          {NAV_GROUPS.map((group) => {
            const items = group.items.filter(canSee);
            if (items.length === 0) return null;
            return (
              <div className="nav-group" key={group.title}>
                <div className="nav-group-title">{group.title}</div>
                {items.map((item) => (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    end={item.end}
                    className={({ isActive }) => `nav-item${isActive ? " active" : ""}`}
                  >
                    <span className="nav-icon">{item.icon}</span>
                    <span className="nav-label">{item.label}</span>
                  </NavLink>
                ))}
              </div>
            );
          })}
        </nav>
        <div className="side-foot">v1.0 · Modern TMS</div>
      </aside>
      <main className="main">
        <header className="topbar">
          <div className="topbar-title">
            AI 物流中台
            <span className="sub">TMS · CRM · ERP</span>
            <span className="ai-pill">AI 加持</span>
          </div>
          <div className="topbar-user">
            <NotificationBell />
            <span>{user?.nickname || user?.username}</span>
            <button className="btn-ghost" onClick={logout}>
              退出
            </button>
          </div>
        </header>
        <section className="content">
          <Outlet />
        </section>
      </main>
    </div>
  );
}
