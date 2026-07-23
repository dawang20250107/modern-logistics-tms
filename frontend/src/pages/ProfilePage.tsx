import { useQuery } from "@tanstack/react-query";
import { useMemo, useRef, useState } from "react";

import { ApiError, apiGet } from "../api/client";
import { fmtDateTime, fmtRelative } from "../api/format";
import type { LoginAttemptRow, UserPreferences } from "../api/types";
import { useAuth } from "../auth/auth";
import { PasswordField } from "../auth/PasswordField";
import { passwordStrength } from "../auth/password";
import { StateView } from "../components/StateView";
import { toast } from "../api/toast";

const RESULT_LABEL: Record<string, string> = {
  success: "登录成功",
  bad_credentials: "密码错误",
  inactive: "账号停用",
  locked: "已锁定",
};

function initialOf(name: string): string {
  const c = (name || "?").trim()[0] ?? "?";
  return c.toUpperCase();
}

const ROUTE_OPTIONS: { value: string; label: string }[] = [
  { value: "/", label: "运输驾驶舱（默认）" },
  { value: "/intake", label: "客服工作台" },
  { value: "/dispatch-board", label: "调度工作台" },
  { value: "/waybills", label: "订单管理" },
  { value: "/reconciliation", label: "对账中心" },
  { value: "/admin", label: "管理后台" },
];

export function ProfilePage() {
  const { user, updateProfile, changePassword, uploadAvatar, removeAvatar } = useAuth();
  const fileRef = useRef<HTMLInputElement>(null);
  const [avatarBusy, setAvatarBusy] = useState(false);

  // 资料编辑
  const [editing, setEditing] = useState(false);
  const [nickname, setNickname] = useState(user?.nickname ?? "");
  const [phone, setPhone] = useState(user?.phone ?? "");
  const [email, setEmail] = useState(user?.email ?? "");
  const [savingProfile, setSavingProfile] = useState(false);

  // 改密
  const [oldPwd, setOldPwd] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [confirmPwd, setConfirmPwd] = useState("");
  const [savingPwd, setSavingPwd] = useState(false);
  const strength = useMemo(() => passwordStrength(newPwd), [newPwd]);

  // 个人偏好
  const [prefs, setPrefs] = useState<UserPreferences>(user?.preferences ?? {});
  const [savingPrefs, setSavingPrefs] = useState(false);
  const prefsDirty = useMemo(
    () => JSON.stringify(prefs) !== JSON.stringify(user?.preferences ?? {}),
    [prefs, user?.preferences],
  );

  const history = useQuery({
    queryKey: ["login-history"],
    queryFn: () => apiGet<LoginAttemptRow[]>("/auth/login-history"),
  });

  if (!user) return <StateView kind="loading" />;

  const displayName = user.nickname || user.username;

  async function saveProfile() {
    setSavingProfile(true);
    try {
      await updateProfile({ nickname: nickname.trim(), phone: phone.trim(), email: email.trim() });
      toast.success("资料已更新");
      setEditing(false);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "保存失败");
    } finally {
      setSavingProfile(false);
    }
  }

  function cancelEdit() {
    setNickname(user!.nickname);
    setPhone(user!.phone);
    setEmail(user!.email);
    setEditing(false);
  }

  async function submitPassword() {
    if (strength.score < 2) return toast.error("新密码强度不足");
    if (newPwd !== confirmPwd) return toast.error("两次输入的新密码不一致");
    setSavingPwd(true);
    try {
      await changePassword(oldPwd, newPwd);
      toast.success("密码已修改，请妥善保管");
      setOldPwd(""); setNewPwd(""); setConfirmPwd("");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "修改失败");
    } finally {
      setSavingPwd(false);
    }
  }

  const pwdReady = oldPwd.length > 0 && strength.score >= 2 && newPwd === confirmPwd && confirmPwd.length > 0;

  async function onPickAvatar(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    if (file.size > 2 * 1024 * 1024) return toast.error("图片过大，请控制在 2MB 内");
    setAvatarBusy(true);
    try {
      await uploadAvatar(file);
      toast.success("头像已更新");
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "上传失败");
    } finally {
      setAvatarBusy(false);
    }
  }

  async function onRemoveAvatar() {
    setAvatarBusy(true);
    try {
      await removeAvatar();
      toast.success("已移除头像");
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "移除失败");
    } finally {
      setAvatarBusy(false);
    }
  }

  async function savePrefs() {
    setSavingPrefs(true);
    try {
      await updateProfile({ preferences: prefs });
      toast.success("偏好已保存");
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "保存失败");
    } finally {
      setSavingPrefs(false);
    }
  }

  return (
    <div className="stack">
      {/* 头部：账户概览 */}
      <div className="panel profile-hero">
        <div className="profile-avatar-wrap">
          <button
            type="button"
            className="profile-avatar"
            onClick={() => fileRef.current?.click()}
            disabled={avatarBusy}
            title="点击更换头像"
            style={user.avatar_url ? { backgroundImage: `url(${user.avatar_url})`, backgroundSize: "cover", backgroundPosition: "center" } : undefined}
          >
            {!user.avatar_url && initialOf(displayName)}
            <span className="profile-avatar-edit">{avatarBusy ? "…" : "更换"}</span>
          </button>
          {user.avatar_url && <button type="button" className="linkish profile-avatar-remove" onClick={onRemoveAvatar} disabled={avatarBusy}>移除</button>}
          <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp,image/gif" hidden onChange={onPickAvatar} />
        </div>
        <div className="profile-id">
          <div className="profile-name">
            {displayName}
            {user.is_superuser && <span className="tag tag-info" style={{ marginLeft: 10 }}>超级管理员</span>}
          </div>
          <div className="muted small mono">@{user.username}</div>
          <div className="profile-meta">
            <span>{user.organization_name || "未分配组织"}</span>
            <span className="dot-sep">·</span>
            <span>加入于 {fmtDateTime(user.date_joined)}</span>
            {user.last_login && <><span className="dot-sep">·</span><span>上次登录 {fmtRelative(user.last_login)}</span></>}
          </div>
        </div>
      </div>

      {!user.is_superuser && user.role_names.length === 0 && (
        <div className="panel" style={{ borderLeft: "4px solid var(--amber)", padding: "14px 18px", background: "var(--amber-weak)" }}>
          <b>账号已创建，等待管理员分配组织与角色。</b>
          <div className="muted small" style={{ marginTop: 4 }}>在分配前，可访问的功能有限。可先完善下方个人资料。</div>
        </div>
      )}

      <div className="profile-grid">
        {/* 左列：资料 + 安全 */}
        <div className="stack">
          <div className="panel">
            <div className="panel-head">
              账户资料
              {!editing ? (
                <button className="btn-ghost" onClick={() => setEditing(true)}>编辑</button>
              ) : (
                <span style={{ display: "flex", gap: 8 }}>
                  <button className="btn-ghost" onClick={cancelEdit}>取消</button>
                  <button className="btn-primary" disabled={savingProfile} onClick={saveProfile}>{savingProfile ? "保存中…" : "保存"}</button>
                </span>
              )}
            </div>
            {!editing ? (
              <div className="kv" style={{ gridTemplateColumns: "1fr" }}>
                <div><span>姓名 / 昵称</span><b>{user.nickname || "—"}</b></div>
                <div><span>用户名</span><b className="mono">{user.username}</b></div>
                <div><span>手机号</span><b>{user.phone || "—"}</b></div>
                <div><span>邮箱</span><b>{user.email || "—"}</b></div>
              </div>
            ) : (
              <div className="stack" style={{ padding: "14px 18px", gap: 12 }}>
                <label className="field"><span>姓名 / 昵称</span><input value={nickname} onChange={(e) => setNickname(e.target.value)} /></label>
                <label className="field"><span>手机号</span><input value={phone} inputMode="numeric" onChange={(e) => setPhone(e.target.value)} /></label>
                <label className="field"><span>邮箱</span><input value={email} type="email" onChange={(e) => setEmail(e.target.value)} /></label>
                <div className="muted small">用户名与组织/角色不可自助修改，如需变更请联系管理员。</div>
              </div>
            )}
          </div>

          <div className="panel">
            <div className="panel-head">安全 · 修改密码</div>
            <div className="stack" style={{ padding: "14px 18px", gap: 12 }}>
              <PasswordField label="当前密码" value={oldPwd} onChange={setOldPwd} autoComplete="current-password" />
              <PasswordField label="新密码" value={newPwd} onChange={setNewPwd} autoComplete="new-password" placeholder="至少 8 位，混合大小写/数字/符号" />
              {newPwd.length > 0 && (
                <div className="pwd-meter">
                  <div className="pwd-meter-bar"><div className="pwd-meter-fill" style={{ width: `${strength.pct}%`, background: strength.color }} /></div>
                  <div className="pwd-meter-row">
                    <span style={{ color: strength.color, fontWeight: 600 }}>{strength.label}</span>
                    {strength.hints.length > 0 && <span className="muted small">{strength.hints.join(" · ")}</span>}
                  </div>
                </div>
              )}
              <PasswordField label="确认新密码" value={confirmPwd} onChange={setConfirmPwd} autoComplete="new-password" />
              {confirmPwd.length > 0 && newPwd !== confirmPwd && <span className="field-err">两次输入的新密码不一致</span>}
              <div>
                <button className="btn-primary" disabled={savingPwd || !pwdReady} onClick={submitPassword}>{savingPwd ? "提交中…" : "更新密码"}</button>
              </div>
            </div>
          </div>
        </div>

        {/* 右列：角色权限 + 登录记录 */}
        <div className="stack">
          <div className="panel">
            <div className="panel-head">我的角色与权限</div>
            <div className="stack" style={{ padding: "14px 18px", gap: 12 }}>
              <div>
                <div className="section-label" style={{ marginBottom: 6 }}>角色</div>
                {user.is_superuser ? (
                  <span className="tag tag-info">超级管理员（全部权限）</span>
                ) : user.role_names.length > 0 ? (
                  <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                    {user.role_names.map((r) => <span key={r} className="tag tag-info">{r}</span>)}
                  </div>
                ) : (
                  <span className="muted small">暂未分配角色</span>
                )}
              </div>
              <div>
                <div className="section-label" style={{ marginBottom: 6 }}>
                  权限点 {user.is_superuser ? "" : `（${user.permissions.length}）`}
                </div>
                {user.is_superuser ? (
                  <span className="muted small">拥有系统全部权限点（*）。</span>
                ) : user.permissions.length > 0 ? (
                  <div style={{ display: "flex", flexWrap: "wrap", gap: 6, maxHeight: 160, overflow: "auto" }}>
                    {user.permissions.map((p) => <span key={p} className="tag tag-none mono" style={{ fontSize: 11 }}>{p}</span>)}
                  </div>
                ) : (
                  <span className="muted small">暂无权限点，等待管理员分配角色后生效。</span>
                )}
              </div>
            </div>
          </div>

          <div className="panel">
            <div className="panel-head">
              偏好设置
              <button className="btn-primary" disabled={savingPrefs || !prefsDirty} onClick={savePrefs}>{savingPrefs ? "保存中…" : "保存"}</button>
            </div>
            <div className="stack" style={{ padding: "14px 18px", gap: 14 }}>
              <label className="field">
                <span>登录后默认进入</span>
                <select value={prefs.default_route ?? "/"} onChange={(e) => setPrefs((p) => ({ ...p, default_route: e.target.value }))}>
                  {ROUTE_OPTIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
                </select>
              </label>
              <label className="field">
                <span>列表密度</span>
                <select value={prefs.table_density ?? "standard"} onChange={(e) => setPrefs((p) => ({ ...p, table_density: e.target.value as "standard" | "compact" }))}>
                  <option value="standard">标准</option>
                  <option value="compact">紧凑</option>
                </select>
              </label>
              <label className="checkline">
                <input type="checkbox" checked={prefs.notify_desktop ?? false} onChange={(e) => setPrefs((p) => ({ ...p, notify_desktop: e.target.checked }))} />
                <span>桌面通知（异常/派单提醒）</span>
              </label>
              <label className="checkline">
                <input type="checkbox" checked={prefs.notify_email ?? false} onChange={(e) => setPrefs((p) => ({ ...p, notify_email: e.target.checked }))} />
                <span>邮件通知（对账/回单摘要）</span>
              </label>
            </div>
          </div>

          <div className="panel">
            <div className="panel-head">最近登录</div>
            {history.isLoading ? (
              <StateView kind="loading" compact />
            ) : history.isError ? (
              <StateView kind="error" onRetry={() => history.refetch()} />
            ) : (history.data ?? []).length === 0 ? (
              <StateView kind="empty" title="暂无登录记录" />
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <thead><tr><th>时间</th><th>结果</th><th>IP</th><th>设备</th></tr></thead>
                  <tbody>
                    {(history.data ?? []).map((r) => (
                      <tr key={r.id}>
                        <td className="small">{fmtDateTime(r.created_at)}</td>
                        <td><span className={`tag ${r.success ? "tag-low" : "tag-high"}`}>{RESULT_LABEL[r.result] ?? r.result}</span></td>
                        <td className="mono small">{r.ip || "—"}</td>
                        <td className="small muted" style={{ maxWidth: 220, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={r.user_agent}>{r.user_agent || "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
