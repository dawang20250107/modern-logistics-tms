// 把每一页的**主要写操作**真按一次，看服务端答不答应。
//
// 为什么单列一个脚本：smoke-ui 只加载页面，e2e-flow 只走建单那一条链，
// 两个都只读或只走一条路。而这一轮发布前查出来的三个问题
// （订单附件丢字节、回单和司机证件直接 400）全在写操作上，
// 而且全都是"前端发 multipart、后端只解 JSON"这种**两边约定对不上**的错——
// 光看后端用例是绿的，光加载页面也看不出来，必须真按下那个按钮。
//
// 用法（需先起好 :8000 网关与 :5173 前端）：
//   node scripts/dev/write-paths.mjs [baseUrl]
// 退出码非 0 = 有写操作打不通。
const { chromium } = await import(
  process.env.PLAYWRIGHT_PKG ?? "/opt/node22/lib/node_modules/playwright/index.js"
).then((m) => m.default ?? m);
import { writeFileSync } from "node:fs";

const BASE = process.argv[2] ?? "http://127.0.0.1:5173";
const API = process.env.API_BASE ?? "http://127.0.0.1:8000";
const USER = process.env.TMS_USER ?? "admin";
const PASS = process.env.TMS_PASS ?? "Admin12345!";

// 每次跑用一个不同的标记。用固定内容的话，页面上那条链接指向的可能是
// **上一次**留下的凭证，这次到底存没存进去就验不出来了——
// 检查会一直是绿的，而它绿的原因和这次的运行无关。
const MARK = "WRITE-PATHS-" + Date.now().toString(36);
const TMP = "/tmp/write-paths-sample.txt";
writeFileSync(TMP, "凭证样本 " + MARK);

const browser = await chromium.launch({ executablePath: "/opt/pw-browsers/chromium", args: ["--no-sandbox"] });
const ctx = await browser.newContext({ viewport: { width: 1680, height: 1000 } });
const page = await ctx.newPage();

// 记录写请求的结果。只看写方法：GET 的 4xx 由 smoke-ui 管。
const writes = [];
page.on("response", async (r) => {
  const m = r.request().method();
  if (!["POST", "PATCH", "PUT", "DELETE"].includes(m)) return;
  const path = r.url().replace(/^https?:\/\/[^/]+/, "").split("?")[0];
  writes.push({ m, path, status: r.status() });
});

const fail = [];
const note = (s) => console.log("  " + s);

async function login() {
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle" });
  await page.locator("input").nth(0).fill(USER);
  await page.locator('input[type="password"]').fill(PASS);
  await page.locator('button[type="submit"], button:has-text("登录")').first().click();
  await page.waitForURL((u) => !u.pathname.startsWith("/login"), { timeout: 15000 });
}

// api 直接问后端要一条真实数据，省得在界面上找
async function pick(path) {
  const r = await page.request.get(`${API}/api/v1${path}`, {
    headers: { Authorization: "Bearer " + (await token()) },
  });
  const j = await r.json();
  return j?.data?.items?.[0] ?? null;
}
let _tok;
async function token() {
  if (_tok) return _tok;
  const r = await page.request.post(`${API}/api/v1/auth/token`, { data: { username: USER, password: PASS } });
  _tok = (await r.json()).data.access;
  return _tok;
}

// upload 在某一页上传一个文件，并断言列出来的链接真能打开
async function upload(label, url, linkSel) {
  await page.goto(url, { waitUntil: "networkidle" });
  await page.waitForTimeout(600);
  const input = page.locator('input[type="file"]').first();
  if ((await input.count()) === 0) {
    fail.push(`${label}：页面上找不到文件输入框`);
    return;
  }
  await input.setInputFiles(TMP);
  await page.waitForTimeout(1800);
  const hrefs = (await page.locator(linkSel).evaluateAll((as) => as.map((a) => a.getAttribute("href")))).filter(Boolean);
  if (hrefs.length === 0) {
    fail.push(`${label}：传完之后没有可点开的链接（凭证存了也看不了，等于没存）`);
    return;
  }
  // 逐个点开，只要有一条是这次传的就算通过。
  // 不假设"最新的排在最后"——回单列表是倒序、附件列表是正序，
  // 写死取哪一条的话，其中一边验的就是**上一次**留下的凭证。
  let hit = null;
  const codes = [];
  for (const href of hrefs) {
    const res = await page.request.get(href.startsWith("http") ? href : API + href);
    codes.push(`${href} → ${res.status()}`);
    if (res.status() === 200 && (await res.text()).includes(MARK)) {
      hit = href;
      break;
    }
  }
  if (!hit) {
    fail.push(`${label}：这次传的凭证在页面上取不回来。逐条点开的结果：\n      ` + codes.join("\n      "));
    return;
  }
  note(`✓ ${label} → ${hit} 200，内容与本次上传一致`);
}

await login();

const order = await pick("/orders?page_size=1");
const waybill = await pick("/waybills?page_size=1");

if (order) {
  await upload("订单附件", `${BASE}/orders/${order.id}`, '.panel:has-text("附件") a[href]');
} else {
  fail.push("订单附件：库里没有订单可用");
}
if (waybill) {
  await upload("电子回单", `${BASE}/waybills/${waybill.waybill_no}`, '.panel:has-text("电子回单") a[href]');
} else {
  fail.push("电子回单：库里没有运单可用");
}

// 运单状态流转：调度员每天点最多的按钮
if (waybill) {
  await page.goto(`${BASE}/waybills/${waybill.waybill_no}`, { waitUntil: "networkidle" });
  await page.waitForTimeout(500);
  const btn = page.locator('button:has-text("发车"), button:has-text("到达"), button:has-text("在途")').first();
  if (await btn.count()) {
    await btn.click();
    await page.waitForTimeout(1200);
    note("· 试点了一次状态流转按钮");
  }
}

await page.waitForTimeout(500);
console.log("\n写请求汇总：");
const bad = [];
for (const w of writes) {
  const flag = w.status >= 400 ? "✗" : "·";
  if (w.status >= 400) bad.push(`${w.m} ${w.path} → ${w.status}`);
  console.log(`  ${flag} ${w.m} ${w.path} → ${w.status}`);
}
// 状态流转按当前状态可能本就不允许（409/400），不算故障；
// 400「请求体不是合法 JSON」这类才是约定对不上。
for (const b of bad) {
  if (!/transition/.test(b)) fail.push(`写请求失败：${b}`);
}

await browser.close();
if (fail.length) {
  console.log("\n发现问题：");
  for (const f of fail) console.log("  ✗ " + f);
  process.exit(1);
}
console.log("\n✓ 各页主要写操作均打通，上传的凭证都能取回原件");
