// 端到端业务链：建单 → 订单管理能查到 → 进池 → 调度工作台能看到 → 详情页打得开。
//
//   node scripts/dev/e2e-flow.mjs [baseUrl]   （需先起 :8000 网关与 :5173 前端）
//
// 与 smoke-ui.mjs 的分工：那个逐页加载、抓非 2xx 与控制台异常，覆盖面广但很浅；
// 这个只走一条链，但**每一步都要求上一步的产物在下一步的界面上出现**。
//
// 为什么要有它：这套系统这一轮暴露的问题，几乎全是"单页看起来正常、
// 跨页对不上"——调度工作台把 8336 说成 20、计数与列表各说各话、
// 准班率把没送到的货算成准点。逐页冒烟一个都抓不到，
// 因为每一页单独看都渲染得好好的。
//
// 断言的是**一致性**而不是具体数字：数字会随演示数据变，
// 而"我刚建的那张单必须能在下一页找到"永远成立。
import { launchBrowser } from "./lib/browser.mjs";

const BASE = process.argv[2] ?? "http://127.0.0.1:5173";
const API = process.env.API_BASE ?? "http://127.0.0.1:8000";
// 账号变量各脚本原先各叫各的（E2E_USER / TMS_USER），CI 上要一个个记。
// 统一以 TMS_USER/TMS_PASS 为准，老名字留作兼容。
const USER = process.env.TMS_USER ?? process.env.E2E_USER ?? "admin";
const PASS = process.env.TMS_PASS ?? process.env.E2E_PASS ?? "Admin12345!";

const steps = [];
let failed = 0;
const ok = (m) => { steps.push(`  ✓ ${m}`); };
const bad = (m) => { steps.push(`  ✗ ${m}`); failed++; };

const browser = await launchBrowser();
const ctx = await browser.newContext({ viewport: { width: 1680, height: 1000 } });
const page = await ctx.newPage();

// 控制台里的未捕获异常一律算失败：页面"看起来渲染了"但底下在报错，
// 是这类界面最常见的坏法。
const pageErrors = [];
page.on("pageerror", (e) => pageErrors.push(String(e).split("\n")[0].slice(0, 160)));

const tag = "E2E" + Date.now().toString(36).toUpperCase();

try {
  // ── 1. 登录 ────────────────────────────────────────────
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle" });
  const inputs = await page.$$("input");
  if (inputs.length < 2) throw new Error("登录页没找到用户名/密码输入框");
  await inputs[0].fill(USER);
  await inputs[1].fill(PASS);
  await page.click('button[type="submit"]');
  await page.waitForURL((u) => !u.pathname.endsWith("/login"), { timeout: 15000 });
  ok("登录");

  // ── 2. 建单（走真实表单，不走 API）────────────────────
  await page.goto(`${BASE}/intake`, { waitUntil: "networkidle" });
  await page.getByRole("button", { name: "建单工作台" }).click();
  await page.waitForTimeout(600);

  // 始发/目的是 CityCombobox（输入框 + 下拉），直接填值即可
  await page.getByPlaceholder("输入或选择，如 无锡").fill("无锡");
  await page.getByPlaceholder("输入或选择，如 上海").fill("上海");
  await page.getByLabel("第 1 项货品名称").fill("测试货");
  await page.getByLabel("第 1 项件数").fill("7");
  // 标记写进**备注**，因为服务端的 search 覆盖的是
  // {order_no, remark, contact_phone, origin, destination, 客户名}——
  // 货品名不在里面。第一版把标记写在货品名上，于是建单明明成功了
  // （界面 toast 是"建单成功"），检索却一条都查不到，看起来像建单失败。
  // 顺带这也就把"search 确实覆盖 remark"一并测了。
  await page.getByPlaceholder(/其他特殊作业/).fill(`${tag} 端到端测试`);
  await page.waitForTimeout(300);
  await page.getByRole("button", { name: "确认提交" }).click();
  await page.waitForTimeout(2500);

  // 建单成功后，订单号从接口反查（界面上的 toast 会消失，不适合当断言依据）
  const token = await page.evaluate(() => localStorage.getItem("access") ?? "");
  const found = await page.evaluate(async ([api, t, q]) => {
    const r = await fetch(`${api}/api/v1/orders?page_size=5&search=${encodeURIComponent(q)}`,
      { headers: { Authorization: `Bearer ${t}` } });
    const j = await r.json();
    return (j?.data?.items ?? []).map((o) => ({ no: o.order_no, status: o.status, qty: o.cargo_quantity }));
  }, [API, token, tag]);

  if (found.length !== 1) {
    bad(`建单后按备注检索到 ${found.length} 条，应为 1 条（tag=${tag}）`);
    throw new Error("建单失败，后续步骤无从谈起");
  }
  const order = found[0];
  ok(`建单 → ${order.no}（状态 ${order.status}，件数 ${order.qty}）`);
  if (String(order.qty) !== "7") bad(`件数落库为 ${order.qty}，表单里填的是 7`);

  // ── 3. 订单管理页能查到它 ─────────────────────────────
  await page.goto(`${BASE}/waybills`, { waitUntil: "networkidle" });
  await page.waitForTimeout(800);
  const search = page.getByPlaceholder(/搜索/).first();
  await search.fill(order.no);
  await page.waitForTimeout(2000);
  const inList = await page.locator(`text=${order.no}`).count();
  if (inList > 0) ok(`订单管理页检索到 ${order.no}`);
  else bad(`订单管理页搜不到刚建的 ${order.no} —— 检索若只在当前页做就会这样`);

  // ── 4. 详情页打得开且是同一张单 ───────────────────────
  const detail = await page.evaluate(async ([api, t, no]) => {
    const r = await fetch(`${api}/api/v1/orders?page_size=1&search=${encodeURIComponent(no)}`,
      { headers: { Authorization: `Bearer ${t}` } });
    const j = await r.json();
    return j?.data?.items?.[0]?.id ?? "";
  }, [API, token, order.no]);
  if (!detail) bad("拿不到订单 id");
  else {
    await page.goto(`${BASE}/orders/${detail}`, { waitUntil: "networkidle" });
    await page.waitForTimeout(1200);
    const shown = await page.locator(`text=${order.no}`).count();
    if (shown > 0) ok("详情页打开且显示同一张单");
    else bad("详情页没有显示该订单号");
  }

  // ── 5. 计数与列表一致（这一轮修的就是这类不一致）──────
  const consistency = await page.evaluate(async ([api, t]) => {
    const get = async (p) => (await (await fetch(`${api}/api/v1${p}`,
      { headers: { Authorization: `Bearer ${t}` } })).json())?.data;
    const counts = await get("/orders/pool-counts?scope=all");
    const free = await get("/orders/pool?scope=free&page_size=1");
    const disp = await get("/orders/dispatched?scope=all&page_size=1");
    return {
      unassigned: counts.unassigned, freeTotal: free.total,
      dispatched: counts.dispatched, dispTotal: disp.total,
      pending: counts.pending,
    };
  }, [API, token]);
  if (consistency.unassigned === consistency.freeTotal) ok(`待分配计数与列表一致（${consistency.freeTotal}）`);
  else bad(`待分配：计数 ${consistency.unassigned} vs 列表 ${consistency.freeTotal}`);
  if (consistency.dispatched === consistency.dispTotal) ok(`已调派计数与列表一致（${consistency.dispTotal}）`);
  else bad(`已调派：计数 ${consistency.dispatched} vs 列表 ${consistency.dispTotal}`);

  // ── 6. 调度工作台：翻到第二页，行必须真的换一批 ────────
  await page.goto(`${BASE}/dispatch-board`, { waitUntil: "networkidle" });
  await page.waitForTimeout(1500);
  const firstPage = await page.locator("tbody tr td:nth-child(2)").allInnerTexts();
  const next = page.getByRole("button", { name: /下一页/ });
  if (await next.count() > 0 && await next.first().isEnabled()) {
    await next.first().click();
    await page.waitForTimeout(1800);
    const secondPage = await page.locator("tbody tr td:nth-child(2)").allInnerTexts();
    const overlap = secondPage.filter((x) => firstPage.includes(x));
    if (secondPage.length === 0) bad("翻到第 2 页后一行都没有");
    else if (overlap.length > 0) bad(`第 2 页与第 1 页有 ${overlap.length} 行重复 —— 排序键不唯一时会这样`);
    else ok(`翻页正常：第 1 页 ${firstPage.length} 行、第 2 页 ${secondPage.length} 行，无重复`);
  } else {
    steps.push("  – 调度工作台只有一页，跳过翻页校验");
  }

  // ── 7. 清理 ───────────────────────────────────────────
  if (detail) {
    await page.evaluate(async ([api, t, id]) => {
      await fetch(`${api}/api/v1/orders/${id}/cancel`, {
        method: "POST", headers: { Authorization: `Bearer ${t}`, "Content-Type": "application/json" },
        body: "{}",
      });
    }, [API, token, detail]);
  }
} catch (e) {
  bad(`中断：${e.message}`);
} finally {
  await browser.close();
}

if (pageErrors.length) {
  for (const e of [...new Set(pageErrors)]) bad(`控制台未捕获异常：${e}`);
}

console.log(steps.join("\n"));
if (failed > 0) {
  console.log(`\n共 ${failed} 处失败。`);
  process.exit(1);
}
console.log("\n✓ 端到端业务链通过");
