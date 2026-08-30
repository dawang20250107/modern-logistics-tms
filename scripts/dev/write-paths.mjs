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
  if (url && url !== page.url()) {
    await page.goto(url, { waitUntil: "networkidle" });
  }
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

// 司机证件：第三条上传路径，走的是资源库那一页。
// 它要先"带出档案"才能传，所以先造一个自己的司机——不依赖演示数据里
// 恰好有一个带身份证号的司机（实测演示库里一个都没有）。
{
  const tail = String(Date.now()).slice(-6);
  const idNo = "310101199001" + tail;
  const name = "走查司机" + tail;
  const mk = await page.request.post(`${API}/api/v1/drivers`, {
    headers: { Authorization: "Bearer " + (await token()) },
    data: { name, phone: "137" + tail.padStart(8, "0"), id_no: idNo, employment_type: "fulltime" },
  });
  if (!mk.ok()) {
    fail.push(`司机证件：造不出司机（${mk.status()}），这一段没验到`);
  } else {
    const drvID = (await mk.json()).data.id;
    await page.goto(`${BASE}/fleet`, { waitUntil: "networkidle" });
    await page.locator('button:has-text("证件合规")').first().click();
    await page.waitForTimeout(800);
    const nameBox = page.locator('input[placeholder="司机姓名"]');
    if ((await nameBox.count()) === 0) {
      fail.push("司机证件：资源库页面上找不到「司机姓名」查询框");
    } else {
      await nameBox.fill(name);
      await page.locator('input[placeholder="身份证后6位"]').fill(tail);
      await page.locator('button:has-text("带出档案")').click();
      await page.waitForTimeout(1200);
      await upload("司机证件", page.url(), 'table a[href]');
    }
    await page.request.delete(`${API}/api/v1/drivers/${drvID}`, {
      headers: { Authorization: "Bearer " + (await token()) },
    });
  }
}

// 派单：全系统用得最多的那个动作，走完整个抽屉。
//
// 这一段补的是一个真实缺口：e2e-flow 只比了「已调派」的计数一致性，
// 从没真按过「确认派单」。而派单要落运单、落议定应付金额、改订单状态——
// 中间任何一步坏了，前面那些计数照样自洽。
//
// 注意它会真的消耗一张池子里的订单（派完就转 converted）。池子里有几千张，
// 每次跑掉一张可以接受；但这也是它只适合跑在开发/预发库上的原因。
{
  await page.goto(`${BASE}/dispatch-board`, { waitUntil: "networkidle" });
  await page.waitForTimeout(1500);
  // 先锁一单：待分配的单要锁定之后才能派
  const free = page.locator("tbody tr").first();
  const lockBtn = free.locator('button:has-text("锁定")').first();
  if (await lockBtn.count()) {
    await lockBtn.click();
    await page.waitForTimeout(1500);
  }
  await page.locator('button:has-text("可调派")').first().click();
  await page.waitForTimeout(1800);
  const row = page.locator("tbody tr").first();
  const orderNo = (await row.innerText().catch(() => "")).match(/LT-\d+|DD\d+/)?.[0] ?? "";
  const dispatchBtn = row.locator('button:has-text("派单")').first();
  if (!(await dispatchBtn.count())) {
    fail.push("派单：可调派池里那一行没有「派单」按钮");
  } else {
    await dispatchBtn.click();
    await page.waitForTimeout(1800);
    // 承运商在抽屉的 <select> 里。（第一版我以为是可点的卡片，
    // 因为 innerText 把 option 的文字也铺出来了，看着像一排按钮。）
    let picked = "";
    const sels = page.locator("select:visible");
    for (let i = 0; i < (await sels.count()); i++) {
      const opts = await sels.nth(i).locator("option").evaluateAll((os) => os.map((o) => ({ v: o.value, t: o.textContent })));
      const hit = opts.find((o) => o.v && o.v.length > 20 && /承运|物流|运输|快运/.test(o.t || ""));
      if (hit) { await sels.nth(i).selectOption(hit.v); picked = hit.t ?? ""; await page.waitForTimeout(900); break; }
    }
    if (!picked) {
      fail.push("派单：抽屉里没找到可选的承运商");
    } else {
      const ok = page.locator('button:has-text("确认派单")').last();
      await ok.click({ timeout: 8000 }).catch((e) => fail.push("派单：确认按钮点不动 —— " + e.message.split("\n")[0]));
      await page.waitForTimeout(2500);
      const toasts = (await page.locator('[class*="toast"]').allInnerTexts()).join(" ");
      if (!/派单成功|已生成运单/.test(toasts)) {
        fail.push(`派单：点了确认但没有成功提示（订单 ${orderNo}，承运商 ${picked}）`);
      } else {
        note(`✓ 派单 ${orderNo} → ${picked}，已生成运单`);
      }
    }
  }
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
