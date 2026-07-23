import { Link } from "react-router-dom";

import { useAuth } from "../auth/auth";
import { StateView } from "../components/StateView";
import { IconBox, IconShield } from "../components/Icons";

type Entry = { to: string; label: string; desc: string; icon: React.ReactNode };

const ENTRIES: Entry[] = [
  { to: "/org", label: "组织与权限", desc: "组织树 · 员工账号 · 角色与权限 RBAC · 服务区划 · 登录审计", icon: <IconBox size={22} /> },
  { to: "/audit", label: "审计日志", desc: "关键操作全程留痕，安全合规可追溯", icon: <IconShield size={22} /> },
];

export function AdminHubPage() {
  const { user } = useAuth();

  // 管理后台仅超级管理员可进
  if (!user?.is_superuser) {
    return (
      <StateView
        kind="forbidden"
        title="仅超级管理员可访问"
        hint="管理后台用于组织、账号、权限与审计管理，请联系超级管理员。"
      />
    );
  }

  return (
    <div className="stack">
      <div className="panel" style={{ background: "var(--hero-grad)", color: "var(--hero-ink)", border: "none" }}>
        <div style={{ padding: "20px 24px" }}>
          <div style={{ fontSize: 22, fontWeight: 700 }}>管理后台</div>
          <div style={{ fontSize: 13, color: "rgba(255,255,255,0.6)", marginTop: 6 }}>
            组织、账号、权限与合规审计的统一管理入口 · 仅超级管理员可进
          </div>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">系统管理</div>
        <div className="hub-grid">
          {ENTRIES.map((e) => (
            <Link key={e.to} to={e.to} className="hub-card">
              <span className="hub-icon">{e.icon}</span>
              <span className="hub-text">
                <span className="hub-title">{e.label}</span>
                <span className="hub-desc">{e.desc}</span>
              </span>
            </Link>
          ))}
        </div>
      </div>
    </div>
  );
}
