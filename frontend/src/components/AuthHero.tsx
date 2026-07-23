// 登录/注册/找回 共用的左栏门面：仅品牌图标 + 名称，居中呈现（科技风）。
export function AuthHero() {
  return (
    <aside className="auth-hero">
      <div className="auth-hero-center">
        <span className="auth-logo-mark" aria-hidden>智</span>
        <span className="auth-logo-name">智运 TMS</span>
      </div>
    </aside>
  );
}
