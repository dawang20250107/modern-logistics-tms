import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { ApiError } from "../api/client";
import { useAuth } from "../auth/auth";
import { PasswordField } from "../auth/PasswordField";

const REMEMBER_KEY = "login:remember-username";
type Method = "password" | "wechat";

export function LoginPage() {
  const { login, user } = useAuth();
  const navigate = useNavigate();
  const remembered = localStorage.getItem(REMEMBER_KEY) ?? "";
  const [method, setMethod] = useState<Method>("password");
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
      const me = await login(username.trim(), password);
      if (remember) localStorage.setItem(REMEMBER_KEY, username.trim());
      else localStorage.removeItem(REMEMBER_KEY);
      navigate(me.preferences?.default_route || "/", { replace: true });
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
        <div className="auth-form">
          <div className="auth-form-brand">ZHIYUN TMS</div>
          <div className="auth-form-title">欢迎登录</div>
          <div className="auth-form-sub">请选择登录方式进入工作台</div>

          <div className="auth-tabs">
            <button type="button" className={`auth-tab${method === "password" ? " on" : ""}`} onClick={() => setMethod("password")}>账号密码</button>
            <button type="button" className={`auth-tab${method === "wechat" ? " on" : ""}`} onClick={() => setMethod("wechat")}>微信扫码</button>
          </div>

          {method === "password" ? (
            <form className="stack" style={{ gap: 15 }} onSubmit={onSubmit}>
              <label className="field">
                <span>用户名</span>
                <input value={username} autoComplete="username" autoFocus onChange={(e) => setUsername(e.target.value)} />
              </label>
              <PasswordField label="密码" value={password} onChange={setPassword} autoComplete="current-password" />
              <div className="auth-row">
                <label className="checkline">
                  <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
                  <span>记住用户名</span>
                </label>
                <Link className="link small" to="/forgot">忘记密码？</Link>
              </div>
              {error && <div className="login-error" role="alert">{error}</div>}
              <button className="btn-primary auth-submit" type="submit" disabled={busy}>
                {busy ? "登录中…" : "登 录"}
              </button>
            </form>
          ) : (
            <div className="wechat-panel">
              <div className="wechat-qr" aria-hidden="true">
                <svg width="120" height="120" viewBox="0 0 120 120" fill="none">
                  <rect x="0" y="0" width="120" height="120" rx="10" fill="var(--panel-2)" />
                  {/* 装饰性二维码占位（非真实码） */}
                  <g fill="var(--faint)">
                    <rect x="16" y="16" width="28" height="28" rx="4" /><rect x="76" y="16" width="28" height="28" rx="4" /><rect x="16" y="76" width="28" height="28" rx="4" />
                    <rect x="24" y="24" width="12" height="12" fill="var(--panel-2)" /><rect x="84" y="24" width="12" height="12" fill="var(--panel-2)" /><rect x="24" y="84" width="12" height="12" fill="var(--panel-2)" />
                    <rect x="58" y="20" width="8" height="8" /><rect x="58" y="36" width="8" height="8" /><rect x="58" y="58" width="8" height="8" /><rect x="76" y="58" width="8" height="8" /><rect x="94" y="58" width="8" height="8" /><rect x="58" y="76" width="8" height="8" /><rect x="76" y="76" width="8" height="8" /><rect x="94" y="94" width="8" height="8" /><rect x="76" y="94" width="8" height="8" />
                  </g>
                </svg>
                <div className="wechat-badge">预留</div>
              </div>
              <div className="wechat-title">微信扫码登录</div>
              <div className="muted small" style={{ textAlign: "center", maxWidth: 260 }}>
                该能力已预留，配置微信开放平台 / 企业微信后即可启用。当前请使用账号密码登录。
              </div>
              <button type="button" className="btn-ghost" onClick={() => setMethod("password")}>改用账号密码登录</button>
            </div>
          )}

          <div className="auth-alt">
            还没有账号？<Link className="link" to="/register">注册新账号</Link>
          </div>
        </div>
      </main>
    </div>
  );
}
