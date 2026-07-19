import { useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { ApiError, apiPost } from "../api/client";
import { PasswordField } from "../auth/PasswordField";
import { passwordStrength } from "../auth/password";

interface RequestResult {
  sent: boolean;
  target?: string;
  channel?: string;
  dev_code?: string;
}

export function ForgotPasswordPage() {
  const navigate = useNavigate();
  const [step, setStep] = useState<1 | 2>(1);
  const [identifier, setIdentifier] = useState("");
  const [target, setTarget] = useState<string | undefined>();
  const [devCode, setDevCode] = useState<string | undefined>();
  const [code, setCode] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [ok, setOk] = useState("");
  const [busy, setBusy] = useState(false);
  const strength = useMemo(() => passwordStrength(newPwd), [newPwd]);

  async function requestCode(e: FormEvent) {
    e.preventDefault();
    if (!identifier.trim()) return setError("请输入邮箱或手机号");
    setError(""); setBusy(true);
    try {
      const res = await apiPost<RequestResult>("/auth/password-reset/request", { identifier: identifier.trim() });
      setTarget(res.target);
      setDevCode(res.dev_code);
      if (res.dev_code) setCode(res.dev_code);
      setStep(2);
      setOk(res.target ? `验证码已发送至 ${res.target}` : "若该账号存在，验证码已发送");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "请求失败，请稍后重试");
    } finally {
      setBusy(false);
    }
  }

  async function resetPwd(e: FormEvent) {
    e.preventDefault();
    if (code.trim().length !== 6) return setError("请输入 6 位验证码");
    if (strength.score < 2) return setError("新密码强度不足");
    if (newPwd !== confirm) return setError("两次输入的新密码不一致");
    setError(""); setBusy(true);
    try {
      await apiPost("/auth/password-reset/confirm", { identifier: identifier.trim(), code: code.trim(), new_password: newPwd });
      setOk("密码已重置，正在跳转登录…");
      setTimeout(() => navigate("/login", { replace: true }), 1200);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "重置失败，请检查验证码");
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
          <h1 className="auth-slogan">找回访问<br />安全重设密码</h1>
          <p className="auth-tagline">通过绑定的邮箱或手机号验证身份后重设密码</p>
          <ul className="auth-points">
            <li><span>◆</span>验证码 10 分钟内有效、一次性使用</li>
            <li><span>◆</span>不泄露账号是否存在，防撞库枚举</li>
            <li><span>◆</span>重设即时生效，全端重新登录</li>
          </ul>
        </div>
        <div className="auth-foot">© 2026 智运 · Modern Logistics TMS</div>
      </aside>
      <main className="auth-form-wrap">
        {step === 1 ? (
          <form className="auth-form" onSubmit={requestCode}>
            <div className="auth-form-brand">ZHIYUN TMS</div>
            <div className="auth-form-title">找回密码</div>
            <div className="auth-form-sub">输入账号绑定的邮箱或手机号获取验证码</div>
            <label className="field">
              <span>邮箱 / 手机号</span>
              <input value={identifier} autoFocus placeholder="you@company.com 或 138…" onChange={(e) => setIdentifier(e.target.value)} />
            </label>
            {error && <div className="login-error" role="alert">{error}</div>}
            <button className="btn-primary auth-submit" type="submit" disabled={busy}>{busy ? "发送中…" : "获取验证码"}</button>
            <div className="auth-alt">想起来了？<Link className="link" to="/login">返回登录</Link></div>
          </form>
        ) : (
          <form className="auth-form" onSubmit={resetPwd}>
            <div className="auth-form-brand">ZHIYUN TMS</div>
            <div className="auth-form-title">重设密码</div>
            <div className="auth-form-sub">{ok || `验证码已发送${target ? `至 ${target}` : ""}`}</div>
            {devCode && <div className="dev-hint">开发环境验证码：<b className="mono">{devCode}</b>（生产由短信/邮件下发）</div>}
            <label className="field">
              <span>验证码</span>
              <input value={code} inputMode="numeric" maxLength={6} placeholder="6 位数字" className="mono" onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
            </label>
            <PasswordField label="新密码" value={newPwd} onChange={setNewPwd} autoComplete="new-password" placeholder="至少 8 位，混合大小写/数字/符号" />
            {newPwd.length > 0 && (
              <div className="pwd-meter">
                <div className="pwd-meter-bar"><div className="pwd-meter-fill" style={{ width: `${strength.pct}%`, background: strength.color }} /></div>
                <div className="pwd-meter-row"><span style={{ color: strength.color, fontWeight: 600 }}>{strength.label}</span>{strength.hints.length > 0 && <span className="muted small">{strength.hints.join(" · ")}</span>}</div>
              </div>
            )}
            <PasswordField label="确认新密码" value={confirm} onChange={setConfirm} autoComplete="new-password" />
            {confirm.length > 0 && newPwd !== confirm && <span className="field-err">两次输入的新密码不一致</span>}
            {error && <div className="login-error" role="alert">{error}</div>}
            <button className="btn-primary auth-submit" type="submit" disabled={busy}>{busy ? "提交中…" : "重设密码"}</button>
            <div className="auth-alt">
              <button type="button" className="linkish" onClick={() => { setStep(1); setError(""); setOk(""); }}>← 换个账号</button>
              <span style={{ margin: "0 8px", color: "var(--line-2)" }}>·</span>
              <Link className="link" to="/login">返回登录</Link>
            </div>
          </form>
        )}
      </main>
    </div>
  );
}
