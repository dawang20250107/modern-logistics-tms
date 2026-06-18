import { NavLink, Outlet } from "react-router-dom";

import { useAuth } from "../auth/auth";
import { NotificationBell } from "./NotificationBell";

type NavItem = { to: string; label: string; icon: string; end: boolean; adminOnly?: boolean };

const NAV: NavItem[] = [
  { to: "/", label: "控制塔", icon: "🗼", end: true },
  { to: "/intake", label: "建单", icon: "📝", end: false },
  { to: "/dispatch-board", label: "调度台", icon: "🎯", end: false },
  { to: "/waybills", label: "运单", icon: "🚚", end: false },
  { to: "/command", label: "指挥中心", icon: "🛰️", end: false },
  { to: "/monitor", label: "监控", icon: "📍", end: false },
  { to: "/alerts", label: "报警", icon: "🚨", end: false },
  { to: "/exceptions", label: "异常", icon: "⚠️", end: false },
  { to: "/reconciliation", label: "对账", icon: "🧾", end: false },
  { to: "/dashboard", label: "看板", icon: "📊", end: false },
  { to: "/catalog", label: "资产", icon: "🗂️", end: false },
  { to: "/ai", label: "AI", icon: "🤖", end: false },
  { to: "/audit", label: "审计", icon: "🛡️", end: false, adminOnly: true },
];

export function AppLayout() {
  const { user, logout } = useAuth();
  return (
    <div className="app">
      <aside className="side">
        <div className="mark">智运</div>
        <nav className="nav">
          {NAV.filter((item) => !item.adminOnly || user?.is_staff || user?.is_superuser).map((item) => (
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
        </nav>
        <div className="side-foot">v1.0</div>
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
