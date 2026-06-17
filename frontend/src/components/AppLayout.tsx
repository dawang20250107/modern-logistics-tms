import { NavLink, Outlet } from "react-router-dom";

import { useAuth } from "../auth/auth";

const NAV = [
  { to: "/", label: "控制塔", end: true },
  { to: "/waybills", label: "运单", end: false },
  { to: "/monitor", label: "监控", end: false },
  { to: "/alerts", label: "报警", end: false },
  { to: "/exceptions", label: "异常", end: false },
  { to: "/ai", label: "AI", end: false },
];

export function AppLayout() {
  const { user, logout } = useAuth();
  return (
    <div className="app">
      <aside className="side">
        <div className="mark">TMS</div>
        <nav className="nav">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) => `nav-item${isActive ? " active" : ""}`}
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </aside>
      <main className="main">
        <header className="topbar">
          <div className="topbar-title">
            AI 物流中台
            <span className="sub">TMS · CRM · ERP</span>
            <span className="ai-pill">AI 加持</span>
          </div>
          <div className="topbar-user">
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
