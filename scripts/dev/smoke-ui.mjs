// 浏览器级冒烟：逐页加载，抓非 2xx 请求与控制台错误。
//
// 存在的理由：有些问题只有真跑浏览器才看得见。SSE 端点下线后，前端三个消费方
// 的 EventSource 按浏览器默认行为无限自动重连，每个页面都在持续打 404 ——
// tsc 不报、vitest 不报、人也不看控制台，就这么一直错着。
//
// 用法（需先起好 :8000 网关与 :5173 前端）：
//   node scripts/dev/smoke-ui.mjs [baseUrl]
// 退出码非 0 表示发现问题，可直接接进 CI。
import { launchBrowser } from "./lib/browser.mjs";
import { login, assertAppPage } from "./lib/browser-login.mjs";

const BASE = process.argv[2] ?? "http://127.0.0.1:5173";
const API = process.env.API_BASE ?? "http://127.0.0.1:8000";
const USER = process.env.TMS_USER ?? "admin";
const PASS = process.env.TMS_PASS ?? "Admin12345!";
const PAGES = [
  ["驾驶舱", "/"], ["订单管理", "/waybills"], ["调度工作台", "/dispatch-board"],
  ["对账中心", "/reconciliation"], ["资源库", "/fleet"], ["计价规则", "/pricing"],
  ["组织中心", "/org"], ["录单", "/intake"], ["审计", "/audit"],
  // /admin 那一页（两张链接卡片的中转页）已删，只留重定向；这条留着是为了
  // 万一哪天重定向掉了能被抓到——老书签打不开是会有人来报的那种问题。
  ["管理后台旧地址→组织与权限", "/admin"],
];
// 已知会 4xx 且属预期的（例如未配置 AI 时的 503）写在这里，避免噪声掩盖真问题
const EXPECTED = [/\/ai\/deepseek\/status/];

const browser = await launchBrowser();
const page = await (await browser.newContext({ viewport: { width: 1680, height: 1000 } })).newPage();

const netErrors = new Map();
const consoleErrors = new Map();
let current = "登录";
page.on("response", (r) => {
  if (r.status() < 400) return;
  const u = r.url();
  if (EXPECTED.some((re) => re.test(u))) return;
  const key = `${r.status()} ${r.request().method()} ${u.split("?")[0].replace(/^https?:\/\/[^/]+/, "")}`;
  if (!netErrors.has(key)) netErrors.set(key, current);
});
page.on("pageerror", (e) => {
  const key = String(e).split("\n")[0].slice(0, 160);
  if (!consoleErrors.has(key)) consoleErrors.set(key, current);
});

// 登录不确认成功的话，这个脚本会以最坏的方式坏掉：网关不通时请求在网络层
// 就断了，连响应都没有，page.on("response") 不触发，于是它报"6 个页面全绿"——
// 而这 6 个页面它一个都没打开过。假绿比假红更难发现。
await login(page, BASE, { user: USER, pass: PASS, api: API });

for (const [name, path] of PAGES) {
  current = name;
  await page.goto(BASE + path, { waitUntil: "networkidle" }).catch(() => {});
  // 多等一会：SSE / 轮询这类副作用不在首屏 networkidle 里
  await page.waitForTimeout(2000);
  // /admin 是重定向到组织与权限的中转页，落点不是登录页即可；
  // 其余页面都要确认真的进去了。
  await assertAppPage(page, name, API);
}
await browser.close();

const fail = netErrors.size + consoleErrors.size;
if (netErrors.size) {
  console.log(`\n非 2xx 请求（${netErrors.size}）：`);
  for (const [k, where] of netErrors) console.log(`  [${where}] ${k}`);
}
if (consoleErrors.size) {
  console.log(`\n未捕获异常（${consoleErrors.size}）：`);
  for (const [k, where] of consoleErrors) console.log(`  [${where}] ${k}`);
}
console.log(fail ? `\n✗ 发现 ${fail} 个问题` : `\n✓ ${PAGES.length} 个页面无非 2xx 请求、无未捕获异常`);
process.exit(fail ? 1 : 0);
