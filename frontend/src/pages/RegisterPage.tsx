import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { ApiError } from "../api/client";
import { useAuthMethods } from "../api/useAuthMethods";
import { useAuth } from "../auth/auth";
import { PasswordField } from "../auth/PasswordField";
import { passwordStrength } from "../auth/password";
import { AuthHero } from "../components/AuthHero";

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

  const { registrationEnabled, data: methods } = useAuthMethods();

  useEffect(() => {
    if (user) navigate("/profile", { replace: true });
  }, [user, navigate]);

  // 直接输地址进来的也要拦住：入口藏了不等于路由没了。
  // 等 methods 到手再判，否则首帧会闪一下"未开放"。
  if (methods && !registrationEnabled) {
    return (
      <div className="auth-page">
        <AuthHero />
        <main className="auth-form-wrap">
          <div className="auth-form">
            <h1 className="auth-title">未开放自助注册</h1>
            <p className="muted" style={{ marginTop: 8 }}>
              本系统的账号由管理员在「组织与权限 → 员工名录」中开通，开通时即绑定组织与角色。
              请联系你所在网点的管理员。
            </p>
            <div className="auth-alt" style={{ marginTop: 18 }}>
              <Link className="link" to="/login">返回登录</Link>
            </div>
          </div>
        </main>
      </div>
    );
  }

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
      <AuthHero />
      <main className="auth-form-wrap">
        <form className="auth-form" onSubmit={onSubmit}>
          <div className="auth-mobile-brand" aria-label="智运 TMS">
            <span className="auth-mobile-mark" aria-hidden="true">智</span>
            <span>智运 TMS</span>
          </div>
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
