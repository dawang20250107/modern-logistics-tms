import { useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";

import { ApiError } from "../api/client";
import { useAuth } from "../auth/auth";

export function LoginPage() {
  const { login, user } = useAuth();
  const navigate = useNavigate();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (user) navigate("/", { replace: true });
  }, [user, navigate]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      await login(username, password);
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "登录失败");
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
          <h1 className="auth-slogan">成为世界级<br />物贸生态集团</h1>
          <p className="auth-tagline">AI 驱动的智能运输中台 · TMS · CRM · ERP</p>
          <ul className="auth-points">
            <li><span>🛰️</span> 全程在途可视 · 异常闭环处理</li>
            <li><span>🎯</span> 智能调度 · 一键批量派单排线</li>
            <li><span>🧾</span> 应收应付 · 上下游结算闭环</li>
          </ul>
        </div>
        <div className="auth-foot">© 2026 智运 · Modern Logistics TMS</div>
      </aside>
      <main className="auth-form-wrap">
        <form className="auth-form" onSubmit={onSubmit}>
          <div className="auth-form-brand">成为世界级物贸生态集团</div>
          <div className="auth-form-title">欢迎登录</div>
          <div className="auth-form-sub">请输入账号信息进入控制塔</div>
          <label className="field">
            <span>用户名</span>
            <input value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
          </label>
          <label className="field">
            <span>密码</span>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </label>
          {error && <div className="login-error">{error}</div>}
          <button className="btn-primary auth-submit" type="submit" disabled={busy}>
            {busy ? "登录中…" : "登 录"}
          </button>
        </form>
      </main>
    </div>
  );
}
