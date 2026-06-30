import { NavLink, Outlet } from "react-router-dom";

import { useAuth } from "../auth/auth";
import { NotificationBell } from "./NotificationBell";
import { SpotlightCommandBar } from "./SpotlightCommandBar";
import { 
  IconTower, IconFileText, IconGrid, IconDatabase, IconMapPin, 
  IconTruck, IconAlert, IconGitBranch, IconReceipt, IconCreditCard, IconShield 
} from "./Icons";

type NavItem = { to: string; label: string; icon: React.ReactNode; end?: boolean; adminOnly?: boolean };
type NavGroup = { title: string; items: NavItem[] };

const NAV_GROUPS: NavGroup[] = [
  {
    title: "业务中台",
    items: [
      { to: "/", label: "时空控制塔", icon: <IconTower size={18} />, end: true },
      { to: "/intake", label: "智能极速建单", icon: <IconFileText size={18} /> },
      { to: "/dispatch-board", label: "拼单调度台", icon: <IconGrid size={18} /> },
      { to: "/waybills", label: "运单总台账", icon: <IconDatabase size={18} /> },
    ],
  },
  {
    title: "车联网与安全",
    items: [
      { to: "/monitor", label: "在途轨迹监控", icon: <IconMapPin size={18} /> },
      { to: "/fleet", label: "运力资产大盘", icon: <IconTruck size={18} /> },
      { to: "/alerts", label: "主动安全预警", icon: <IconAlert size={18} /> },
      { to: "/exceptions", label: "时空异常处置", icon: <IconGitBranch size={18} /> },
    ],
  },
  {
    title: "业财结算",
    items: [
      { to: "/reconciliation", label: "业财核销对账", icon: <IconReceipt size={18} /> },
      { to: "/pricing", label: "合同运价管理", icon: <IconCreditCard size={18} /> },
    ],
  },
  {
    title: "系统合规",
    items: [
      { to: "/audit", label: "底层审计日志", icon: <IconShield size={18} />, adminOnly: true },
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
        <div className="side-foot">v1.0 · 智运 TMS 核心引擎</div>
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
      <SpotlightCommandBar />
    </div>
  );
}
