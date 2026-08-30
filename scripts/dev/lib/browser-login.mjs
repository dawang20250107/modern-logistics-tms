// 浏览器走查脚本共用的登录与"我到底在哪一页"断言。
//
// 为什么单独抽出来：ui-audit 和 smoke-ui 原先都是「填表 → 回车 → 等 3 秒 →
// 开始量」，从不确认登录到底成没成。网关没起的时候，登录必然失败，
// 后面每一次 goto 都被前端打回 /login——于是：
//
//   · ui-audit 把**登录页**当成六个业务页各量了一遍，报出
//     「a.link 60×19 点击目标过小」「颜色 4/16」这种看着像真的结论。
//     实际上那是登录页上的"忘记密码"链接和记住我勾选框。
//     一次发版走查就这么红在了一条根本不存在的问题上。
//   · smoke-ui 更坏：网关不通时请求连响应都没有（网络层就断了），
//     page.on("response") 压根不触发，于是它报**绿**——
//     "6 个页面无非 2xx 请求"，而这 6 个页面它一个都没打开过。
//
// 检查项报错的原因必须是真的。报错原因不对，比不报更伤——
// 修的人会去改一个没坏的东西，而真正坏的那个一直没人看见。
//
// 约定的退出码：
//   0 = 跑了，没发现问题
//   1 = 跑了，有发现（真问题）
//   2 = 没跑起来（登录失败/页面没打开），本次结论作废，调用方应记"跳过"而非"失败"
export const EXIT_NOT_RUN = 2;

// 登录页的特征类名。取自 frontend/src/pages/Login.tsx，
// 改类名的话这里要跟着改——否则断言会失效，又退回"静默量登录页"。
const LOGIN_MARKERS = ".auth-submit, .auth-logo-mark, .auth-form-brand";

/** 等到"确实不在登录页"为止：URL 不是 /login，且登录页的 DOM 已经卸载。
 *
 * 为什么要等而不是立刻判：waitForURL 在导航发生的那一刻就返回，此时上一屏的
 * DOM 往往还挂着几十毫秒。第一版写成立刻判，结果网关好好的也报"被打回登录页"——
 * 断言比它要防的问题还容易误报，那就没人敢留着它了。
 */
export async function waitForAppPage(page, timeout = 8000) {
  try {
    await page.waitForFunction(
      (sel) => !document.querySelector(sel) &&
               !location.pathname.replace(/\/+$/, "").endsWith("/login"),
      LOGIN_MARKERS, { timeout, polling: 150 },
    );
    return true;
  } catch {
    return false;
  }
}

/** 登录；没成功就带上可操作的原因退出（码 2），不让后续步骤在登录页上瞎量。 */
export async function login(page, base, { user = "admin", pass = "Admin12345!", api = "http://127.0.0.1:8000" } = {}) {
  await page.goto(`${base}/login`, { waitUntil: "networkidle" });
  const inputs = await page.$$("input");
  if (inputs.length < 2) die(page, "登录页上没找到用户名/密码输入框", api);
  await inputs[0].fill(user);
  await page.locator('input[type="password"]').first().fill(pass);
  await page.locator('button[type="submit"], button:has-text("登录")').first().click();
  if (!(await waitForAppPage(page, 15000))) {
    die(page, "登录没成功，页面还停在登录页", api, await errorText(page));
  }
}

/** 每次导航后调用：确认真的进了业务页，而不是被静默打回登录页。
 *  会话中途失效（令牌过期、网关重启）时，只在开头断言一次是拦不住的。 */
export async function assertAppPage(page, where, api = "http://127.0.0.1:8000") {
  if (!(await waitForAppPage(page, 5000))) {
    die(page, `「${where}」没打开，被打回了登录页`, api);
  }
}

async function errorText(page) {
  return page.locator(".auth-error, .form-error, [role=alert]").first()
    .textContent({ timeout: 1000 }).then((t) => (t || "").trim()).catch(() => "");
}

function die(page, what, api, detail = "") {
  console.error(`\n✗ 本次走查没有跑起来：${what}`);
  console.error(`  当前 URL：${page.url()}`);
  if (detail) console.error(`  页面提示：${detail}`);
  console.error(`  常见原因：网关（${api}）没起、库没迁移、或 admin 口令不是默认值。`);
  console.error("  注意：这不是「走查没过」，是「走查没跑」——本次报告不作数。");
  process.exit(EXIT_NOT_RUN);
}
