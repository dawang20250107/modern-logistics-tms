// 用受限角色真的登进去走一遍。
//
// 存在的理由：其余五条走查全用超管跑（admin 带 `*` 权限），
// 而超管**永远看不到 403 长什么样**。这一轮补的全是受限角色的体验：
// 订单读补闸、客服拿到建单权限、三条路由挂上权限门——
// 这些改动的好坏，只有拿一个受限账号真的点一遍才知道。
//
// 演示客服（seed_cs）的权限点：waybill.view + waybill.create + masterdata.view，
// 数据范围 org（上海）。它该能做的和不该能做的都写在下面。
//
// 三件事：
//   1. 该看得到的面真的能用（客服工作台能建单，订单管理有数据）
//   2. 该挡住的面**说人话**——是"没有查看…的权限"，不是空白页、
//      不是"加载失败请重试"，更不是一屏红色报错
//   3. 页面上不该出现意外的 403：接口 403 而界面不说明，就是那种
//      "用户以为坏了、去提工单"的场景
//
// 退出码：0 通过 / 1 有发现 / 2 没跑起来（登录失败等，结论作废）
import { execFileSync } from "node:child_process";

import { launchBrowser } from "./lib/browser.mjs";
import { login, assertAppPage, EXIT_NOT_RUN } from "./lib/browser-login.mjs";

const BASE = process.argv[2] ?? "http://127.0.0.1:5173";
const API = process.env.API_BASE ?? "http://127.0.0.1:8000";
const USER = process.env.TMS_ROLE_USER ?? "seed_cs";
const PASS = process.env.TMS_ROLE_PASS ?? "Demo12345!";
const DB = process.env.DATABASE_URL ?? "";

const fail = [];
const bad = (m) => { fail.push(m); console.log("  ✗ " + m); };
const ok = (m) => console.log("  · " + m);

function q(sql) {
  if (!DB) return null;
  try {
    return execFileSync("psql", [DB, "-tAq", "-c", sql], { encoding: "utf8" }).trim();
  } catch { return null; }
}

// 前置：这个账号的权限点必须真是我们以为的那一组，否则整条走查的判据是空的。
const perms = q(`SELECT string_agg(p.code, ',' ORDER BY p.code)
  FROM accounts_user u
  JOIN iam_role_assignment ra ON ra.user_id = u.id
  JOIN iam_role_permissions rp ON rp.role_id = ra.role_id
  JOIN iam_permission p ON p.id = rp.permission_id
  WHERE u.username = '${USER}'`);
if (DB && perms !== "masterdata.view,waybill.create,waybill.view") {
  console.log(`✗ ${USER} 的权限点是「${perms}」，不是预期的 ` +
    `masterdata.view,waybill.create,waybill.view。\n` +
    `  这条走查的每一条判据都建立在那一组权限上，权限变了结论就不作数。\n` +
    `  如果是有意调整，改这里；如果库是旧的，重新播种。`);
  process.exit(EXIT_NOT_RUN);
}

const browser = await launchBrowser();
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();

// 记录每一页出现的 403，最后和"界面有没有说明"对账
let current = "登录";
const forbidden = new Map();
// 预期之内的 403：能力探测。界面会据此把入口标成"无权限"而不是亮着让人点，
// 这一条本身有 vitest 用例守着（SpotlightCommandBar 那处判错过一次：
// 请求失败时 data 是 undefined，`undefined !== false` 判成"可用"）。
const EXPECTED_403 = [/\/ai\/deepseek\/status/];
page.on("response", (r) => {
  if (r.status() !== 403) return;
  const p = r.url().split("?")[0].replace(/^https?:\/\/[^/]+/, "");
  if (EXPECTED_403.some((re) => re.test(p))) return;
  if (!forbidden.has(current)) forbidden.set(current, new Set());
  forbidden.get(current).add(p);
});

await login(page, BASE, { user: USER, pass: PASS, api: API });
console.log(`已用「${USER}」登录（${perms ?? "权限点未知"}）`);

// ── 1. 侧栏只列该看得到的 ──────────────────────────────
current = "侧栏";
const navLabels = await page.$$eval(".side a", (as) => as.map((a) => a.textContent.trim()).filter(Boolean));
console.log("\n── 侧栏 ──");
ok(`看得到 ${navLabels.length} 项：${navLabels.join(" / ")}`);
for (const gone of ["对账中心", "计价规则", "组织与权限"]) {
  if (navLabels.some((l) => l.includes(gone))) {
    bad(`侧栏里出现了「${gone}」—— 客服没有对应的权限点，点进去只会是一页 403`);
  }
}
for (const want of ["订单管理", "客服工作台"]) {
  if (!navLabels.some((l) => l.includes(want))) {
    bad(`侧栏里没有「${want}」—— 客服本职就是这两页，看不到等于用不了`);
  }
}

// ── 2. 该能用的面真的能用 ────────────────────────────
console.log("\n── 能用的面 ──");
current = "订单管理";
await page.goto(`${BASE}/waybills`, { waitUntil: "networkidle" });
await assertAppPage(page, "/waybills", API);
const rows = await page.$$eval("tbody tr", (trs) => trs.length);
// "页面是空的"和"库里本来就没有"必须分得开：后者不是产品问题，
// 报成失败会让人去修一个没坏的东西。所以先问库这个账号该看见几单。
const visible = Number(q(`SELECT count(*) FROM ops_order o
  JOIN accounts_user cb ON cb.id = o.created_by_id
  WHERE NOT o.is_deleted
    AND cb.organization_id = (SELECT organization_id FROM accounts_user WHERE username = '${USER}')`) ?? -1);
if (rows > 0) {
  ok(`订单管理有 ${rows} 行（库里该账号范围内共 ${visible} 单）`);
} else if (visible > 0) {
  bad(`订单管理一行都没有，而库里这个账号的范围内有 ${visible} 单 —— 数据被界面吞掉了`);
} else if (visible === 0) {
  ok("订单管理是空的，但库里这个账号的范围内确实一单也没有（不是界面的问题）");
} else {
  ok("订单管理是空的；没有 DATABASE_URL，无法区分「界面吞了」和「本来就没有」");
}

current = "客服工作台";
await page.goto(`${BASE}/intake`, { waitUntil: "networkidle" });
await assertAppPage(page, "/intake", API);
const denied = await page.$$eval("h2, h3", (hs) => hs.map((h) => h.textContent ?? "").join(" "));
if (/没有查看/.test(denied)) {
  bad("客服进不了客服工作台 —— 权限门要的点和它实际需要的对不上");
} else {
  ok("客服工作台打得开");
}

// ── 3. 该挡住的面要说人话 ────────────────────────────
console.log("\n── 挡住的面 ──");
for (const [name, path, perm] of [
  ["对账中心", "/reconciliation", "finance.view"],
  ["计价规则", "/pricing", "finance.view"],
  ["组织与权限", "/org", "org.rbac"],
]) {
  current = name;
  await page.goto(`${BASE}${path}`, { waitUntil: "networkidle" });
  await assertAppPage(page, path, API);
  const text = (await page.textContent("main")) ?? "";
  const said = text.includes("没有查看") && text.includes(perm);
  const blank = text.replace(/\s+/g, "").length < 120;
  if (!said) {
    bad(`直接敲地址进「${name}」，页面没说清缺的是 ${perm}` +
      (blank ? "（而且几乎是一页空白）" : `（页面文字：${text.replace(/\s+/g, " ").slice(0, 80)}…）`));
  } else {
    ok(`「${name}」明说了需要 ${perm}`);
  }
}

// ── 4. 页面上不该有没被说明的 403 ────────────────────
console.log("\n── 意外的 403 ──");
// 被权限门挡下的页面不会发请求，所以这里剩下的都是"页面渲染了、
// 但里面某个接口被拒了"——那正是用户会当成"坏了"的情形。
// "登录"这一档收的是登录后第一屏（驾驶舱）上的请求——那一页对所有人开放，
// 所以它上面的 403 同样要算。
const unexplained = [...forbidden.entries()].filter(([pg]) => !["对账中心", "计价规则", "组织与权限"].includes(pg));
if (unexplained.length) {
  for (const [pg, paths] of unexplained) {
    bad(`「${pg}」上有被拒的接口，而这一页是让客服进的：${[...paths].join(" ")}`);
  }
} else {
  ok("客服能进的页面上没有被拒的接口");
}

await browser.close();
console.log(fail.length
  ? `\n✗ 受限角色走查有 ${fail.length} 处不对`
  : `\n✓ 受限角色（${USER}）该用的用得了、该挡的说清了`);
process.exit(fail.length ? 1 : 0);
