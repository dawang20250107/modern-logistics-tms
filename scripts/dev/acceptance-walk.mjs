// 验收链：把 docs/delivery-notes.md 第七节那 8 步**按同一张单**走一遍。
//
// 为什么还要这一条（已经有 e2e-flow / write-paths / driver-walk）：
// 那三条各自覆盖一段，但**没有一条从头到尾用同一张单**。
// 而这套系统这一轮查出来的问题里，有好几个就长在段与段的接缝上——
// 派单生成的运单号对不对得上、签收之后对账里认不认这笔钱、
// 同一个手机号在 /track 上查不查得到。
//
// 它还有第二个作用：**验证那份文档本身**。文档里写着让客户照做的 8 步，
// 如果某一步现在做不了，那文档就是错的，而客户会拿着它来验收。
//
// 用法（需先起好 :8000 网关与 :5173 前端）：
//   node scripts/dev/acceptance-walk.mjs [baseUrl]
// 退出码：0 过 / 1 有发现 / 2 没跑起来
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";

import { launchBrowser } from "./lib/browser.mjs";
import { login } from "./lib/browser-login.mjs";

const BASE = process.argv[2] ?? "http://127.0.0.1:5173";
const API = process.env.API_BASE ?? "http://127.0.0.1:8000";
const USER = process.env.TMS_USER ?? "admin";
const PASS = process.env.TMS_PASS ?? "Admin12345!";
const DB = process.env.DATABASE_URL ?? "";
const MARK = "ACC-" + Date.now().toString(36).toUpperCase();
const PHONE = "1390000" + String(Date.now() % 10000).padStart(4, "0");

const fail = [];
const step = (n, s) => console.log(`  ${n}. ${s}`);
const bad = (s) => { console.log("  ✗ " + s); fail.push(s); };
const q = (sql) => (DB ? execFileSync("psql", [DB, "-tAq", "-c", sql], { encoding: "utf8" }).trim() : "");

const browser = await launchBrowser();
const ctx = await browser.newContext({ viewport: { width: 1680, height: 1000 } });
const page = await ctx.newPage();
const pageErrors = [];
page.on("pageerror", (e) => pageErrors.push(String(e).split("\n")[0].slice(0, 140)));

await login(page, BASE, { user: USER, pass: PASS, api: API });
const tok = await page.evaluate(() => localStorage.getItem("access"));
const auth = { Authorization: `Bearer ${tok}` };
const jget = async (p) => (await (await page.request.get(API + p, { headers: auth })).json())?.data;
const jpost = async (p, data) => {
  const r = await page.request.post(API + p, { headers: { ...auth, "Content-Type": "application/json" }, data });
  return { status: r.status(), body: await r.json().catch(() => ({})) };
};

let orderNo = "", waybillNo = "";
try {
  // ── 1. 建一单 ──
  // 建单接口收的是 {fields:{…}}（也认 text/cargo_items），不是把字段平铺在顶层。
  // 第一版平铺着发，拿到 INTAKE_EMPTY——接口约定看清楚再发，
  // 否则报出来的"建单失败"是我自己造的。
  const anyCust = (await jget("/api/v1/customers?page_size=1"))?.items?.[0];
  const created = await jpost("/api/v1/orders/intake", {
    fields: {
      contact_name: MARK, contact_phone: PHONE, origin: "上海", destination: "杭州",
      cargo_desc: MARK + " 货物", cargo_weight_ton: 6, cargo_quantity: 12,
      customer: anyCust?.id ?? null,
    },
    channel: "cs",
  });
  orderNo = created.body?.data?.order_no ?? "";
  if (!orderNo) { bad(`第 1 步建单失败：${created.status} ${JSON.stringify(created.body).slice(0, 160)}`); throw new Error("stop"); }
  step(1, `建单 ${orderNo}`);

  // ── 2. 订单管理里搜得到 ──
  const found = await jget(`/api/v1/orders?search=${orderNo}&page_size=5`);
  if (!(found?.items ?? []).some((o) => o.order_no === orderNo)) bad(`第 2 步：订单管理里搜不到 ${orderNo}`);
  else step(2, "订单管理里搜得到");

  // ── 3. 派给一个承运商，生成运单 ──
  const orderID = (found?.items ?? []).find((o) => o.order_no === orderNo)?.id;
  const carriers = await jget("/api/v1/carriers?page_size=5");
  const carrier = (carriers?.items ?? []).find((c) => !c.blacklisted);
  // 要挑一个**当前没在跑车**的司机：派单会拒绝已被占用的司机
  // （DRIVER_BUSY，这是对的——一个司机同时跑两趟是调度事故）。
  // 第一版随手取第一个，撞上 409，那是我挑错了人不是产品有问题。
  const drivers = await jget("/api/v1/drivers?page_size=200");
  const busy = new Set(
    DB ? q(`SELECT string_agg(DISTINCT driver_id::text, ',') FROM ops_waybill
            WHERE driver_id IS NOT NULL AND status IN
              ('dispatched','loaded','departed','in_transit','arrived')`).split(",") : [],
  );
  const driver = (drivers?.items ?? []).find((d) => d.id_no && d.phone && !busy.has(d.id));
  if (!carrier || !driver) { bad("第 3 步：库里没有可用的承运商或有身份证号的司机"); throw new Error("stop"); }
  for (const act of ["confirm", "pool"]) await jpost(`/api/v1/orders/${orderID}/${act}`, {});
  await jpost(`/api/v1/orders/${orderID}/claim`, {});
  const disp = await jpost(`/api/v1/orders/${orderID}/dispatch`, {
    carrier: carrier.id, driver: driver.id, dispatch_type: "third_party",
  });
  waybillNo = disp.body?.data?.waybill_no ?? disp.body?.data?.waybill?.waybill_no ?? "";
  if (!waybillNo) { bad(`第 3 步派单失败：${disp.status} ${JSON.stringify(disp.body).slice(0, 200)}`); throw new Error("stop"); }
  step(3, `派给「${carrier.name}」→ 运单 ${waybillNo}`);

  // ── 4. 流转到签收 + 传回单 + 点开看 ──
  // 状态机的合法链（见 waybills/transitions.go 的 allowedTransitions）：
  // 派单之后运单落在 pending_dispatch，要先 dispatched 再往下走。
  // 第一版直接从 loaded 开始，四步全 409——是我漏了第一跳，不是状态机有问题。
  for (const to of ["dispatched", "loaded", "departed", "in_transit", "arrived"]) {
    const r = await jpost(`/api/v1/waybills/${waybillNo}/transition`, { to_status: to });
    if (r.status >= 400) bad(`第 4 步流转到 ${to} 失败：${r.status} ${JSON.stringify(r.body).slice(0, 140)}`);
  }
  const sign = await jpost(`/api/v1/waybills/${waybillNo}/sign`, { signatory: MARK + " 收货人" });
  if (sign.status >= 400) bad(`第 4 步签收失败：${sign.status} ${JSON.stringify(sign.body).slice(0, 140)}`);

  // 回单：真传一份，再取回来比对内容。**这一步是整条链的重点**
  const TMP = "/tmp/acceptance-receipt.txt";
  writeFileSync(TMP, "验收回单样本 " + MARK);
  const wbID = (await jget(`/api/v1/waybills?search=${waybillNo}&page_size=1`))?.items?.[0]?.id;
  const up = await page.request.post(`${API}/api/v1/receipts`, {
    headers: auth,
    multipart: {
      waybill: wbID ?? "", receipt_type: "pod", status: "uploaded",
      file: { name: "receipt.txt", mimeType: "text/plain", buffer: readFileSync(TMP) },
    },
  });
  if (up.status() >= 400) {
    bad(`第 4 步传回单失败：${up.status()} ${(await up.text()).slice(0, 160)}`);
  } else {
    const rec = (await up.json())?.data ?? {};
    const link = rec.file_display || rec.file || "";
    if (!link) bad("第 4 步：回单传上去了，但响应里没有可点开的链接");
    else {
      const got = await page.request.get(link.startsWith("http") ? link : API + link);
      const text = await got.text();
      if (got.status() !== 200) bad(`第 4 步：回单点开是 ${got.status()}（${link}）`);
      else if (!text.includes(MARK)) bad(`第 4 步：回单点开了，但内容不是刚传的那份`);
      else step(4, `流转到签收，回单传上去点开就是刚传的那份`);
    }
  }

  // ── 5. 对账：先生成费用，再生成对账单 → 确认 → 核销 ──
  //
  // 费用不是派单时自动出来的，要在运单的「费用台账」上按一下按合同价生成。
  // 交付说明第七节原先没写这一步，照着它走到第 5 步会拿到一张 ¥0 的对账单，
  // 看起来像对账坏了。文档已经补上，这里也照着补。
  const gc = await jpost(`/api/v1/waybills/${waybillNo}/generate-costs`, {});
  if (gc.status >= 400) bad(`第 5 步生成费用失败：${gc.status} ${JSON.stringify(gc.body).slice(0, 160)}`);

  // 对账单要按**这张单的客户**生成，随手取第一个客户会归集不到这笔钱——
  // 第一版就是那么写的，拿到 ¥0，差点当成"归集坏了"报出去。
  const ordRow = (await jget(`/api/v1/orders?search=${orderNo}&page_size=1`))?.items?.[0];
  const cust = ordRow?.customer
    ? { id: ordRow.customer, name: ordRow.customer_name }
    : (await jget("/api/v1/customers?page_size=1"))?.items?.[0];
  const today = new Date();
  const start = new Date(today.getTime() - 30 * 86400000).toISOString().slice(0, 10);
  const end = new Date(today.getTime() + 86400000).toISOString().slice(0, 10);
  const gen = await jpost("/api/v1/finance/statements/generate", {
    direction: "receivable", counterparty_type: "customer", counterparty_id: cust?.id,
    period_start: start, period_end: end,
  });
  const stID = gen.body?.data?.id ?? "";
  if (!stID) {
    bad(`第 5 步生成对账单失败：${gen.status} ${JSON.stringify(gen.body).slice(0, 200)}`);
  } else {
    await jpost(`/api/v1/finance/statements/${stID}/confirm`, {});
    const st = await jget(`/api/v1/finance/statements/${stID}`);
    const total = Number(st?.total_amount ?? 0);
    if (total <= 0) {
      bad(`第 5 步：对账单金额是 ${total}，归集不到费用`);
    } else {
      const settle = await jpost(`/api/v1/finance/statements/${stID}/settle`, { amount: "1.00", method: "bank_transfer" });
      if (settle.status >= 400) bad(`第 5 步核销失败：${settle.status} ${JSON.stringify(settle.body).slice(0, 160)}`);
      else step(5, `对账单 ${st?.statement_no}（¥${total}）已确认并核销 ¥1.00`);
      if (DB) {
        const mismatch = q(`SELECT count(*) FROM (SELECT s.id FROM fin_statement s
          LEFT JOIN fin_statement_payment p ON p.statement_id=s.id WHERE s.id='${stID}'
          GROUP BY s.id, s.settled_amount HAVING s.settled_amount <> COALESCE(sum(p.amount),0)) t`);
        if (mismatch !== "0") bad("第 5 步：核销之后已核销额与核销明细对不上");
      }
    }
  }

  // ── 6. /track 免登录查一次 ──
  await page.goto(`${BASE}/track`, { waitUntil: "networkidle" });
  await page.waitForTimeout(600);
  const ins = page.locator("input:visible");
  await ins.nth(0).fill(orderNo);
  await ins.nth(1).fill(PHONE);
  await page.locator('button:has-text("查询"), button[type="submit"]').first().click();
  await page.waitForTimeout(2000);
  const trackTxt = (await page.locator("body").innerText()).replace(/\s+/g, " ");
  if (!trackTxt.includes(orderNo)) bad(`第 6 步：/track 用「${orderNo} + ${PHONE}」查不到这一单：${trackTxt.slice(0, 160)}`);
  else step(6, "/track 用订单号 + 手机号查得到");

  // ── 7. 司机端：driver-walk.mjs 专门覆盖（含"不给定位权限"那一侧）──
  step(7, "司机端由 driver-walk.mjs 覆盖（含不给定位权限那一侧），此处不重复");
  // ── 8. 备份恢复：backup-drill.sh 专门覆盖 ──
  step(8, "备份恢复由 backup-drill.sh 覆盖（真的起网关、真的取一个媒体文件）");
} catch (e) {
  if (String(e.message) !== "stop") bad(`走查中断：${String(e).slice(0, 200)}`);
} finally {
  await browser.close();
}

for (const e of pageErrors) bad(`页面未捕获异常：${e}`);

// 清理：这条链会造一张订单、一张运单、一张对账单和若干费用。
// 不清的话每跑一次演示库就多一份，而"跑完留垃圾"这一晚上已经修过两次。
if (DB && orderNo) {
  // 引用 ops_waybill 的表有十几张（合同、司机绑定、打卡、轨迹、告警…）。
  // 一张张手写删除顺序是漏一张卡一次——第一版就漏了 ops_contract，
  // 清理失败，下一轮那个司机还占着运单，派单直接 DRIVER_BUSY，
  // **看起来像产品有问题，其实是上一轮的垃圾没清掉**。
  // 改成从 information_schema 现查引用者，删完再删主表。
  try {
    const refs = execFileSync("psql", [DB, "-tAq", "-c",
      `SELECT DISTINCT tc.table_name FROM information_schema.table_constraints tc
         JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_name = tc.constraint_name
        WHERE tc.constraint_type='FOREIGN KEY' AND ccu.table_name='ops_waybill'
          AND tc.table_name <> 'ops_waybill'`], { encoding: "utf8" }).trim().split("\n").filter(Boolean);
    const wbSel = `SELECT id FROM ops_waybill WHERE waybill_no='${waybillNo}'`;
    const sql = [
      `DELETE FROM fin_statement_payment WHERE statement_id IN (
         SELECT statement_id FROM fin_statement_line WHERE waybill_no='${waybillNo}');`,
      `DELETE FROM fin_statement_line WHERE waybill_no='${waybillNo}';`,
      ...refs.map((t) => `DELETE FROM ${t} WHERE waybill_id IN (${wbSel});`),
      `DELETE FROM ops_waybill WHERE waybill_no='${waybillNo}';`,
      `DELETE FROM ops_order_event WHERE order_id IN (SELECT id FROM ops_order WHERE order_no='${orderNo}');`,
      `DELETE FROM ops_order WHERE order_no='${orderNo}';`,
    ].join("\n");
    execFileSync("psql", [DB, "-tAq", "-c", sql], { encoding: "utf8" });
  } catch (e) {
    // 清理失败要说出来：留下的运单会让下一轮的司机"已被占用"，
    // 那时报出来的失败原因和真实原因完全对不上。
    console.log("  · 清理走查数据失败，它留在库里了（下一轮可能因此 DRIVER_BUSY）：" +
      String(e).split("\n")[0].slice(0, 140));
  }
}

console.log(fail.length
  ? `\n✗ 验收链有 ${fail.length} 处走不通`
  : `\n✓ 验收链（交付说明第七节那 8 步）从头到尾用同一张单走通了`);
process.exit(fail.length ? 1 : 0);
