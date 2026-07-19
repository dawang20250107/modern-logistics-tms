import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { ApiError } from "../api/client";
import { useAuth } from "../auth/auth";
import { PasswordField } from "../auth/PasswordField";

const REMEMBER_KEY = "login:remember-username";

export function LoginPage() {
  const { login, user } = useAuth();
  const navigate = useNavigate();
  const remembered = localStorage.getItem(REMEMBER_KEY) ?? "";
  const [username, setUsername] = useState(remembered);
  const [password, setPassword] = useState("");
  const [remember, setRemember] = useState(Boolean(remembered));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (user) navigate("/", { replace: true });
  }, [user, navigate]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!username.trim() || !password) {
      setError("请输入用户名与密码");
      return;
    }
    setError("");
    setBusy(true);
    try {
      await login(username.trim(), password);
      if (remember) localStorage.setItem(REMEMBER_KEY, username.trim());
      else localStorage.removeItem(REMEMBER_KEY);
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "登录失败，请检查网络后重试");
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
          <h1 className="auth-slogan">接单 · 调度 · 承运<br />结算一体协同</h1>
          <p className="auth-tagline">面向 B2B 公路货运的承运商协同型运输管理平台</p>
          <ul className="auth-points">
            <li><span>◆</span>客服接单 · 客户上下文一屏直达</li>
            <li><span>◆</span>调度比价 · 外包 / 网货 / 自营协同派单</li>
            <li><span>◆</span>在途可视 · 异常闭环 · 回单对账</li>
          </ul>
        </div>
        <div className="auth-foot">© 2026 智运 · Modern Logistics TMS</div>
      </aside>
      <main className="auth-form-wrap">
        <form className="auth-form" onSubmit={onSubmit}>
          <div className="auth-form-brand">ZHIYUN TMS</div>
          <div className="auth-form-title">欢迎登录</div>
          <div className="auth-form-sub">请输入账号信息进入工作台</div>
          <label className="field">
            <span>用户名</span>
            <input
              value={username}
              autoComplete="username"
              autoFocus
              onChange={(e) => setUsername(e.target.value)}
            />
          </label>
          <PasswordField label="密码" value={password} onChange={setPassword} autoComplete="current-password" />
          <div className="auth-row">
            <label className="checkline">
              <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
              <span>记住用户名</span>
            </label>
            <span className="muted small">忘记密码请联系管理员</span>
          </div>
          {error && <div className="login-error" role="alert">{error}</div>}
          <button className="btn-primary auth-submit" type="submit" disabled={busy}>
            {busy ? "登录中…" : "登 录"}
          </button>
          <div className="auth-alt">
            还没有账号？<Link className="link" to="/register">注册新账号</Link>
          </div>
        </form>
      </main>
    </div>
  );
}
