// 登录/注册/找回 共用的左栏门面。
//
// 这半屏原先只放了一个图标和一行名字——占了 60% 的宽度，讲了 0 句话。
// 登录页是每天第一眼看到的界面，也是唯一能对外说明"这是什么系统"的地方，
// 所以给它一句定位和四个能力锚点。不做营销辞令：写的都是系统真做的事。
const PILLARS: { k: string; v: string }[] = [
  { k: "接单", v: "多渠道建单 · 自然语言转结构化" },
  { k: "调度", v: "订单池抢单 · 承运比价 · 智能排线" },
  { k: "在途", v: "GPS 实时轨迹 · ETA 预测 · 异常闭环" },
  { k: "结算", v: "按项目/线路归集 · 应收应付一体" },
];

export function AuthHero() {
  return (
    <aside className="auth-hero">
      <div className="auth-hero-inner">
        <div className="auth-hero-brand">
          <span className="auth-logo-mark" aria-hidden>智</span>
          <span className="auth-logo-name">智运 TMS</span>
        </div>
        <p className="auth-hero-tagline">
          从一通电话到一张对账单，<br />整条运输链路只在一个系统里跑完。
        </p>
        <ul className="auth-hero-pillars">
          {PILLARS.map((p) => (
            <li key={p.k}>
              <span className="auth-pillar-k">{p.k}</span>
              <span className="auth-pillar-v">{p.v}</span>
            </li>
          ))}
        </ul>
      </div>
    </aside>
  );
}
