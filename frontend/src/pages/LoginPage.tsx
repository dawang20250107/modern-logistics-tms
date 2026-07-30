import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { ApiError } from "../api/client";
import { useAuth } from "../auth/auth";
import { PasswordField } from "../auth/PasswordField";
import { AuthHero } from "../components/AuthHero";

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
      <AuthHero />
      <main className="auth-form-wrap">
        <div className="auth-form">
          <div className="auth-mobile-brand" aria-label="智运 TMS">
            <span className="auth-mobile-mark" aria-hidden="true">智</span>
            <span>智运 TMS</span>
          </div>
          <div className="auth-form-brand">ZHIYUN TMS</div>
          <div className="auth-form-title">欢迎登录</div>
          <div className="auth-form-sub">请选择登录方式进入工作台</div>

          <div className="auth-tabs" role="tablist" aria-label="登录方式">
            <button
              id="login-tab-password"
              type="button"
              role="tab"
              aria-selected={method === "password"}
              aria-controls="login-panel-password"
              className={`auth-tab${method === "password" ? " on" : ""}`}
              onClick={() => setMethod("password")}
            >账号密码</button>
            <button
              id="login-tab-wechat"
              type="button"
              role="tab"
              aria-selected={method === "wechat"}
              aria-controls="login-panel-wechat"
              className={`auth-tab${method === "wechat" ? " on" : ""}`}
              onClick={() => setMethod("wechat")}
            >微信扫码</button>
          </div>

          {method === "password" ? (
            <form id="login-panel-password" className="stack" style={{ gap: 15 }} onSubmit={onSubmit} role="tabpanel" aria-labelledby="login-tab-password">
              <label className="field">
                <span>用户名</span>
                <input
                  id="login-username"
                  value={username}
                  autoComplete="username"
                  autoFocus
                  aria-invalid={Boolean(error) || undefined}
                  aria-describedby={error ? "login-error" : undefined}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </label>
              <PasswordField
                id="login-password"
                label="密码"
                value={password}
                onChange={setPassword}
                autoComplete="current-password"
                ariaInvalid={Boolean(error) || undefined}
                ariaDescribedBy={error ? "login-error" : undefined}
              />
              <div className="auth-row">
                <label className="checkline">
                  <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
                  <span>记住用户名</span>
                </label>
                <Link className="link small" to="/forgot">忘记密码？</Link>
              </div>
              {error && <div id="login-error" className="login-error" role="alert">{error}</div>}
              <button className="btn-primary auth-submit" type="submit" disabled={busy}>
                {busy ? "登录中…" : "登 录"}
              </button>
            </form>
          ) : (
            <div id="login-panel-wechat" className="wechat-panel" role="tabpanel" aria-labelledby="login-tab-wechat">
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
