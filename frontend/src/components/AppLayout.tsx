import { useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";

import { hasPerm, useAuth } from "../auth/auth";
import { NotificationBell } from "./NotificationBell";
import { SpotlightCommandBar } from "./SpotlightCommandBar";
import {
  IconTower, IconGrid, IconDatabase, IconTruck,
  IconReceipt, IconCreditCard, IconShield, IconFileText,
} from "./Icons";

type NavItem = { to: string; label: string; icon: React.ReactNode; end?: boolean; adminOnly?: boolean; superOnly?: boolean; perm?: string };
type NavGroup = { title: string; items: NavItem[] };

// 工作流导向导航：驾驶舱纵览 → 客服接单 → 调度派单 → 订单流转，
// 资源/计价/对账为支撑，管理后台聚合运营分析与系统管理等次级入口。
const NAV_GROUPS: NavGroup[] = [
  {
    title: "工作台",
    items: [
      { to: "/", label: "运输驾驶舱", icon: <IconTower size={18} />, end: true },
      { to: "/intake", label: "客服工作台", icon: <IconFileText size={18} /> },
      { to: "/dispatch-board", label: "调度工作台", icon: <IconGrid size={18} /> },
      { to: "/waybills", label: "订单管理", icon: <IconDatabase size={18} /> },
    ],
  },
  {
    title: "资源与结算",
    items: [
      { to: "/fleet", label: "资源库", icon: <IconTruck size={18} /> },
      { to: "/pricing", label: "计价规则", icon: <IconCreditCard size={18} /> },
      { to: "/reconciliation", label: "对账中心", icon: <IconReceipt size={18} /> },
    ],
  },
  {
    // 组织 / 用户 / 权限 / 审计——仅超级管理员可见可进
    title: "系统",
    items: [
      { to: "/admin", label: "管理后台", icon: <IconShield size={18} />, superOnly: true },
    ],
  },
];


// 管理后台内的页面 + 个人中心：不在侧栏，但需要正确的顶栏标题
const SUB_TITLES: Record<string, string> = {
  "/org": "组织与权限", "/audit": "审计日志", "/profile": "个人中心",
};

function currentPageTitle(pathname: string) {
  const flat = NAV_GROUPS.flatMap((group) => group.items);
  const exact = flat.find((item) => item.to === pathname || (item.end && pathname === "/"));
  if (exact) return exact.label;
  if (SUB_TITLES[pathname]) return SUB_TITLES[pathname];
  if (pathname.startsWith("/orders/")) return "订单详情";
  if (pathname.startsWith("/waybills/")) return "运单详情";
  return "运输驾驶舱";
}

export function AppLayout() {
  const { user, logout } = useAuth();
  const { pathname } = useLocation();
  const pageTitle = currentPageTitle(pathname);
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem("nav_collapsed") === "1");

  const toggleCollapsed = () => {
    setCollapsed((v) => {
      const next = !v;
      localStorage.setItem("nav_collapsed", next ? "1" : "0");
      return next;
    });
  };

  const canSee = (item: NavItem) => {
    if (item.superOnly && !user?.is_superuser) return false;
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
          <div className="topbar-main">
            <button className="topbar-toggle" onClick={toggleCollapsed} aria-label="切换导航" title="切换导航">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <line x1="3" y1="6" x2="21" y2="6" /><line x1="3" y1="12" x2="21" y2="12" /><line x1="3" y1="18" x2="21" y2="18" />
              </svg>
            </button>
            <div className="topbar-title">
              {/* 导航展开时侧栏已高亮当前页，此处不再重复页名（避免"重复词语"）；折叠态才显示页名 */}
              {collapsed && <span className="topbar-title-text">{pageTitle}</span>}
              <span className="sub">B2B 外协承运协同</span>
            </div>
            <span className="topbar-shortcut"><kbd>Ctrl</kbd><kbd>K</kbd>查单 / 派单</span>
          </div>
          <div className="topbar-user">
            <NotificationBell />
            <NavLink to="/profile" className="topbar-account" title="个人中心">
              <span className="topbar-avatar">{((user?.nickname || user?.username || "?").trim()[0] ?? "?").toUpperCase()}</span>
              <span className="topbar-account-name">{user?.nickname || user?.username}</span>
            </NavLink>
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
