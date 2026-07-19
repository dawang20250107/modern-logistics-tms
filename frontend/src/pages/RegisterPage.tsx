import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { ApiError } from "../api/client";
import { useAuth } from "../auth/auth";
import { PasswordField } from "../auth/PasswordField";
import { passwordStrength } from "../auth/password";

export function RegisterPage() {
  const { register, user } = useAuth();
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [nickname, setNickname] = useState("");
  const [phone, setPhone] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (user) navigate("/profile", { replace: true });
  }, [user, navigate]);

  const strength = useMemo(() => passwordStrength(password), [password]);
  const mismatch = confirm.length > 0 && confirm !== password;
  const phoneOk = !phone || /^1[3-9]\d{9}$/.test(phone);
  const canSubmit =
    username.trim().length >= 3 && strength.score >= 2 && !mismatch && confirm.length > 0 && phoneOk;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (username.trim().length < 3) return setError("用户名至少 3 位");
    if (!phoneOk) return setError("请输入有效的手机号");
    if (strength.score < 2) return setError("密码强度不足，请混合大小写、数字或符号");
    if (password !== confirm) return setError("两次输入的密码不一致");
    setError("");
    setBusy(true);
    try {
      await register({ username: username.trim(), nickname: nickname.trim(), phone: phone.trim(), password });
      navigate("/profile", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "注册失败，请稍后重试");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth">
      <aside className="auth-hero">
        <div className="auth-hero-top">
          <span className="brand-mark auth-mark">智</span>
          <span className="auth-logo">智运 TMS</span>
        </div>
        <div className="auth-hero-mid">
          <h1 className="auth-slogan">加入承运协同<br />开启数字化运营</h1>
          <p className="auth-tagline">注册后由管理员为你分配组织与角色，即可进入对应工作台</p>
          <ul className="auth-points">
            <li><span>◆</span>客服 · 调度 · 承运 · 财务多角色协同</li>
            <li><span>◆</span>权限按岗位收敛，看到的就是你能做的</li>
            <li><span>◆</span>全程操作留痕，安全合规可追溯</li>
          </ul>
        </div>
        <div className="auth-foot">© 2026 智运 · Modern Logistics TMS</div>
      </aside>
      <main className="auth-form-wrap">
        <form className="auth-form" onSubmit={onSubmit}>
          <div className="auth-form-brand">ZHIYUN TMS</div>
          <div className="auth-form-title">注册新账号</div>
          <div className="auth-form-sub">创建账号后等待管理员分配组织与角色</div>
          <label className="field">
            <span>用户名 <em className="req">*</em></span>
            <input value={username} autoComplete="username" autoFocus placeholder="登录账号，3 位以上" onChange={(e) => setUsername(e.target.value)} />
          </label>
          <label className="field">
            <span>姓名 / 昵称</span>
            <input value={nickname} placeholder="用于系统内显示" onChange={(e) => setNickname(e.target.value)} />
          </label>
          <label className="field">
            <span>手机号</span>
            <input value={phone} inputMode="numeric" placeholder="选填，用于找回与通知" onChange={(e) => setPhone(e.target.value)} />
            {!phoneOk && <span className="field-err">手机号格式不正确</span>}
          </label>
          <PasswordField label="密码" value={password} onChange={setPassword} autoComplete="new-password" placeholder="至少 8 位，混合大小写/数字/符号" />
          {password.length > 0 && (
            <div className="pwd-meter">
              <div className="pwd-meter-bar">
                <div className="pwd-meter-fill" style={{ width: `${strength.pct}%`, background: strength.color }} />
              </div>
              <div className="pwd-meter-row">
                <span style={{ color: strength.color, fontWeight: 600 }}>{strength.label}</span>
                {strength.hints.length > 0 && <span className="muted small">{strength.hints.join(" · ")}</span>}
              </div>
            </div>
          )}
          <PasswordField label="确认密码" value={confirm} onChange={setConfirm} autoComplete="new-password" />
          {mismatch && <span className="field-err">两次输入的密码不一致</span>}
          {error && <div className="login-error" role="alert">{error}</div>}
          <button className="btn-primary auth-submit" type="submit" disabled={busy || !canSubmit}>
            {busy ? "注册中…" : "注 册"}
          </button>
          <div className="auth-alt">
            已有账号？<Link className="link" to="/login">返回登录</Link>
          </div>
        </form>
      </main>
    </div>
  );
}
