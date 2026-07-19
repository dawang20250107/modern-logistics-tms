import { Link } from "react-router-dom";

import { hasPerm, useAuth } from "../auth/auth";
import type { CurrentUser } from "../api/types";
import {
  IconMoney, IconMapPin, IconAlert, IconGitBranch, IconRobot, IconTerminal,
  IconFileText, IconBox, IconShield,
} from "../components/Icons";

type Entry = {
  to: string; label: string; desc: string; icon: React.ReactNode;
  perm?: string; adminOnly?: boolean;
};
type Section = { title: string; hint: string; items: Entry[] };

const SECTIONS: Section[] = [
  {
    title: "运营与分析",
    hint: "经营视角与在途监控",
    items: [
      { to: "/dashboard", label: "经营看板", desc: "营收/成本/毛利与趋势", icon: <IconMoney size={20} />, perm: "analytics.view" },
      { to: "/monitor", label: "在途监控", desc: "车辆实时定位与状态", icon: <IconMapPin size={20} />, perm: "telematics.view" },
      { to: "/alerts", label: "安全预警", desc: "超速/温控/油量等报警", icon: <IconAlert size={20} />, perm: "telematics.view" },
      { to: "/exceptions", label: "异常处置", desc: "在途异常立案与闭环", icon: <IconGitBranch size={20} /> },
    ],
  },
  {
    title: "智能与数据",
    hint: "AI 辅助与数据资产",
    items: [
      { to: "/ai", label: "AI 工作台", desc: "运单问答与智能建议", icon: <IconRobot size={20} /> },
      { to: "/command", label: "命令中心", desc: "自然语言直达操作", icon: <IconTerminal size={20} /> },
      { to: "/catalog", label: "数据目录", desc: "指标口径与数据资产", icon: <IconFileText size={20} /> },
    ],
  },
  {
    title: "组织与安全",
    hint: "账号权限与合规审计",
    items: [
      { to: "/org", label: "组织与权限", desc: "组织树 / 员工 / 角色 RBAC", icon: <IconBox size={20} />, perm: "org.view" },
      { to: "/audit", label: "审计日志", desc: "关键操作留痕可追溯", icon: <IconShield size={20} />, adminOnly: true },
    ],
  },
];

export function AdminHubPage() {
  const { user } = useAuth();
  const canSee = (e: Entry) => {
    if (e.adminOnly && !(user?.is_staff || user?.is_superuser)) return false;
    if (e.perm && !hasPerm(user as CurrentUser | null, e.perm)) return false;
    return true;
  };

  return (
    <div className="stack">
      <div className="panel" style={{ background: "linear-gradient(135deg, #1b1e25 0%, #16181d 100%)", color: "#fff", border: "none" }}>
        <div style={{ padding: "20px 24px" }}>
          <div style={{ fontSize: 22, fontWeight: 700 }}>管理后台</div>
          <div style={{ fontSize: 13, color: "rgba(255,255,255,0.6)", marginTop: 6 }}>
            运营分析、智能数据与组织安全的统一入口，日常高频操作请使用左侧工作台。
          </div>
        </div>
      </div>

      {SECTIONS.map((section) => {
        const items = section.items.filter(canSee);
        if (items.length === 0) return null;
        return (
          <div className="panel" key={section.title}>
            <div className="panel-head">
              {section.title}
              <span className="muted small" style={{ fontWeight: 400 }}>{section.hint}</span>
            </div>
            <div className="hub-grid">
              {items.map((e) => (
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
        );
      })}
    </div>
  );
}
