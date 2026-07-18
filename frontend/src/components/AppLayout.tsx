import { NavLink, Outlet, useLocation } from "react-router-dom";

import { hasPerm, useAuth } from "../auth/auth";
import { NotificationBell } from "./NotificationBell";
import { SpotlightCommandBar } from "./SpotlightCommandBar";
import {
  IconTower, IconFileText, IconGrid, IconDatabase, IconMapPin, IconTruck, IconAlert,
  IconGitBranch, IconReceipt, IconCreditCard, IconShield, IconBox, IconMoney, IconZap,
  IconRobot, IconTerminal,
} from "./Icons";

type NavItem = { to: string; label: string; icon: React.ReactNode; end?: boolean; adminOnly?: boolean; perm?: string };
type NavGroup = { title: string; items: NavItem[] };

const NAV_GROUPS: NavGroup[] = [
  {
    title: "运营",
    items: [
      { to: "/", label: "运营总览", icon: <IconTower size={18} />, end: true },
      { to: "/intake", label: "新建订单", icon: <IconFileText size={18} /> },
      { to: "/dispatch-board", label: "调度台", icon: <IconGrid size={18} /> },
      { to: "/waybills", label: "运单", icon: <IconDatabase size={18} /> },
      { to: "/dashboard", label: "经营看板", icon: <IconMoney size={18} />, perm: "analytics.view" },
    ],
  },
  {
    title: "监控与安全",
    items: [
      { to: "/monitor", label: "在途监控", icon: <IconMapPin size={18} />, perm: "telematics.view" },
      { to: "/command", label: "指挥中心", icon: <IconZap size={18} />, perm: "telematics.view" },
      { to: "/fleet", label: "车队", icon: <IconTruck size={18} /> },
      { to: "/alerts", label: "安全预警", icon: <IconAlert size={18} />, perm: "telematics.view" },
      { to: "/exceptions", label: "异常处置", icon: <IconGitBranch size={18} /> },
    ],
  },
  {
    title: "财务",
    items: [
      { to: "/reconciliation", label: "对账", icon: <IconReceipt size={18} /> },
      { to: "/pricing", label: "运价", icon: <IconCreditCard size={18} /> },
    ],
  },
  {
    title: "组织与智能",
    items: [
      { to: "/org", label: "组织", icon: <IconBox size={18} />, perm: "org.view" },
      { to: "/ai", label: "AI 助手", icon: <IconRobot size={18} />, perm: "ai.use" },
    ],
  },
  {
    title: "系统",
    items: [
      { to: "/catalog", label: "数据目录", icon: <IconTerminal size={18} />, perm: "analytics.view" },
      { to: "/audit", label: "审计日志", icon: <IconShield size={18} />, adminOnly: true },
    ],
  },
];

export function AppLayout() {
  const { user, logout } = useAuth();
  const { pathname } = useLocation();
  const canSee = (item: NavItem) => {
    if (item.adminOnly && !(user?.is_staff || user?.is_superuser)) return false;
    if (item.perm && !hasPerm(user, item.perm)) return false;
    return true;
  };
  const allItems = NAV_GROUPS.flatMap((g) => g.items);
  const active = allItems
    .filter((i) => (i.end ? pathname === i.to : pathname.startsWith(i.to)))
    .sort((a, b) => b.to.length - a.to.length)[0];

  return (
    <div className="app">
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
        <div className="side-foot">智运 TMS · v1.0</div>
      </aside>
      <main className="main">
        <header className="topbar">
          <div className="topbar-title">{active?.label ?? "智运 TMS"}</div>
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
