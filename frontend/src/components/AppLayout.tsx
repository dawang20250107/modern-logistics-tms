import { useState } from "react";
import { NavLink, Outlet } from "react-router-dom";

import { hasPerm, useAuth } from "../auth/auth";
import { NotificationBell } from "./NotificationBell";
import { SpotlightCommandBar } from "./SpotlightCommandBar";
import {
  IconTower, IconGrid, IconDatabase, IconMapPin, IconTruck, IconAlert,
  IconGitBranch, IconReceipt, IconCreditCard, IconShield, IconBox, IconMoney,
} from "./Icons";

type NavItem = { to: string; label: string; icon: React.ReactNode; end?: boolean; adminOnly?: boolean; perm?: string };
type NavGroup = { title: string; items: NavItem[] };

const NAV_GROUPS: NavGroup[] = [
  {
    title: "运营",
    items: [
      { to: "/", label: "运营总览", icon: <IconTower size={18} />, end: true },
      { to: "/dispatch-board", label: "调度台", icon: <IconGrid size={18} /> },
      { to: "/waybills", label: "运单管理", icon: <IconDatabase size={18} /> },
      { to: "/dashboard", label: "经营看板", icon: <IconMoney size={18} />, perm: "analytics.view" },
    ],
  },
  {
    title: "资源与合规",
    items: [
      { to: "/fleet", label: "资源库", icon: <IconTruck size={18} /> },
      { to: "/pricing", label: "计价规则", icon: <IconCreditCard size={18} /> },
      { to: "/monitor", label: "在途监控", icon: <IconMapPin size={18} />, perm: "telematics.view" },
      { to: "/alerts", label: "安全预警", icon: <IconAlert size={18} />, perm: "telematics.view" },
      { to: "/exceptions", label: "异常处置", icon: <IconGitBranch size={18} /> },
    ],
  },
  {
    title: "结算",
    items: [
      { to: "/reconciliation", label: "对账中心", icon: <IconReceipt size={18} /> },
    ],
  },
  {
    title: "系统",
    items: [
      { to: "/org", label: "组织与权限", icon: <IconBox size={18} />, perm: "org.view" },
      { to: "/audit", label: "审计日志", icon: <IconShield size={18} />, adminOnly: true },
    ],
  },
];

export function AppLayout() {
  const { user, logout } = useAuth();
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem("nav_collapsed") === "1");

  const toggleCollapsed = () => {
    setCollapsed((v) => {
      const next = !v;
      localStorage.setItem("nav_collapsed", next ? "1" : "0");
      return next;
    });
  };

  const canSee = (item: NavItem) => {
    if (item.adminOnly && !(user?.is_staff || user?.is_superuser)) return false;
    if (item.perm && !hasPerm(user, item.perm)) return false;
    return true;
  };

  return (
    <div className={`app${collapsed ? " nav-collapsed" : ""}`}>
      <aside className="side">
        <div className="brand">
          <span className="brand-mark">智</span>
          <span className="brand-text">智运 TMS<span className="brand-sub">运输管理系统</span></span>
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
                    title={item.label}
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
        <button className="nav-collapse-btn" onClick={toggleCollapsed} title={collapsed ? "展开导航" : "收起导航"}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            {collapsed ? <path d="M9 18l6-6-6-6" /> : <path d="M15 18l-6-6 6-6" />}
          </svg>
          <span className="nav-label">收起导航</span>
        </button>
      </aside>
      <main className="main">
        <header className="topbar">
          <button className="topbar-toggle" onClick={toggleCollapsed} aria-label="切换导航" title="切换导航">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <line x1="3" y1="6" x2="21" y2="6" /><line x1="3" y1="12" x2="21" y2="12" /><line x1="3" y1="18" x2="21" y2="18" />
            </svg>
          </button>
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
      <SpotlightCommandBar />
    </div>
  );
}
