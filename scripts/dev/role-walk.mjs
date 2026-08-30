// 用受限角色真的登进去走一遍。
//
// 存在的理由：其余五条走查全用超管跑（admin 带 `*` 权限），
// 而超管**永远看不到 403 长什么样**。这一轮补的全是受限角色的体验：
// 订单读补闸、客服拿到建单权限、三条路由挂上权限门——
// 这些改动的好坏，只有拿一个受限账号真的点一遍才知道。
//
// 两个演示角色各走一遍：
//   客服（seed_cs）      waybill.view + waybill.create + masterdata.view，范围本网点
//   财务只读（seed_finance） finance.view + waybill.view + analytics.view，范围全部
// 各自该能做的和不该能做的都写在下面的档案里。
//
// 财务只读那一档抓到的是另一类：**看得见按不动的按钮**。
// 对账台账上五个「确认」、两个「核销」、「批量审计」「+ 生成对账单」
// 对它全都亮着，点下去弹一句「无财务操作权限」——
// 只读的人会以为是自己操作错了，反复点、然后来问。
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
const PASS = process.env.TMS_ROLE_PASS ?? "Demo12345!";
const DB = process.env.DATABASE_URL ?? "";

// 已知会 403 的能力探测：界面据此把入口标成"无权限"而不是亮着让人点。
// 这一条本身有 vitest 用例守着（capability-probe.test.ts）。
const EXPECTED_403 = [/\/ai\/deepseek\/status/];

const PROFILES = [
  {
    user: "seed_cs",
    label: "客服",
    // 权限点必须真是这一组，否则整条走查的判据是空的
    perms: "masterdata.view,waybill.create,waybill.view",
    navMust: ["订单管理", "客服工作台"],
    navMustNot: ["对账中心", "计价规则", "组织与权限"],
    // [页面名, 路径, 缺的权限点]
    blocked: [
      ["对账中心", "/reconciliation", "finance.view"],
      ["计价规则", "/pricing", "finance.view"],
      ["组织与权限", "/org", "org.rbac"],
    ],
    // 该能用的：订单管理要有数据、客服工作台要打得开
    listPage: ["订单管理", "/waybills"],
    openPage: ["客服工作台", "/intake"],
  },
  {
    user: "seed_finance",
    label: "财务（只读）",
    perms: "analytics.view,finance.view,waybill.view",
    navMust: ["对账中心", "计价规则", "订单管理"],
    navMustNot: ["组织与权限"],
    blocked: [["资源库", "/fleet", "masterdata.view"]],
    listPage: ["订单管理", "/waybills"],
    // 只读角色不该看到写按钮：看得见按不动比没有更糟
    noWriteButtons: {
      name: "对账中心·台账",
      path: "/reconciliation",
      tab: "对账单台账",
      labels: ["确认", "核销", "批量审计", "生成对账单", "登记核销", "审计本单", "登记收款", "登记付款"],
      need: "finance.manage",
    },
  },
];

const fail = [];
const bad = (m) => { fail.push(m); console.log("  \u2717 " + m); };
const ok = (m) => console.log("  \u00b7 " + m);

function q(sql) {
  if (!DB) return null;
  try {
    return execFileSync("psql", [DB, "-tAq", "-c", sql], { encoding: "utf8" }).trim();
  } catch { return null; }
}

const browser = await launchBrowser();

for (const prof of PROFILES) {
  console.log(`\n\u2550\u2550 ${prof.label}（${prof.user}）\u2550\u2550`);

  // 前置：权限点必须真是我们以为的那一组。变了就说明演示角色被改过，
  // 而下面每一条判据都建立在那组权限上——那时结论不作数，退 2 而不是报失败。
  const perms = q(`SELECT string_agg(p.code, ',' ORDER BY p.code)
    FROM accounts_user u
    JOIN iam_role_assignment ra ON ra.user_id = u.id
    JOIN iam_role_permissions rp ON rp.role_id = ra.role_id
    JOIN iam_permission p ON p.id = rp.permission_id
    WHERE u.username = '${prof.user}'`);
  if (DB && perms !== prof.perms) {
    console.log(`\u2717 ${prof.user} 的权限点是「${perms}」，不是预期的 ${prof.perms}。\n` +
      `  这条走查的每一条判据都建立在那一组权限上，权限变了结论就不作数。\n` +
      `  如果是有意调整，改 PROFILES；如果库是旧的，重新播种。`);
    await browser.close();
    process.exit(EXIT_NOT_RUN);
  }

  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  let current = "驾驶舱";
  const forbidden = new Map();
  page.on("response", (r) => {
    if (r.status() !== 403) return;
    const u = r.url().split("?")[0].replace(/^https?:\/\/[^/]+/, "");
    if (EXPECTED_403.some((re) => re.test(u))) return;
    if (!forbidden.has(current)) forbidden.set(current, new Set());
    forbidden.get(current).add(u);
  });

  await login(page, BASE, { user: prof.user, pass: PASS, api: API });
  await page.waitForTimeout(1200); // 让首屏（驾驶舱）的请求都发完

  // ── 侧栏 ──
  const nav = await page.$$eval(".side a", (as) => as.map((a) => a.textContent.trim()).filter(Boolean));
  console.log("\u2500\u2500 侧栏 \u2500\u2500");
  ok(`${nav.length} 项：${nav.join(" / ")}`);
  for (const gone of prof.navMustNot) {
    if (nav.some((l) => l.includes(gone))) bad(`侧栏里出现了「${gone}」—— 没有对应权限点，点进去只会是一页 403`);
  }
  for (const want of prof.navMust) {
    if (!nav.some((l) => l.includes(want))) bad(`侧栏里没有「${want}」—— 这是该角色的本职页面，看不到等于用不了`);
  }

  // ── 该能用的面 ──
  console.log("\u2500\u2500 能用的面 \u2500\u2500");
  if (prof.listPage) {
    const [name, path] = prof.listPage;
    current = name;
    await page.goto(`${BASE}${path}`, { waitUntil: "networkidle" });
    await assertAppPage(page, path, API);
    const rows = await page.$$eval("tbody tr", (trs) => trs.length);
    // "页面是空的"和"库里本来就没有"必须分得开：后者不是产品问题，
    // 报成失败会让人去修一个没坏的东西。
    const visible = Number(q(`SELECT count(*) FROM ops_order o
      JOIN accounts_user cb ON cb.id = o.created_by_id
      WHERE NOT o.is_deleted AND ((SELECT r.data_scope FROM iam_role r
            JOIN iam_role_assignment ra ON ra.role_id = r.id
            JOIN accounts_user u ON u.id = ra.user_id WHERE u.username = '${prof.user}' LIMIT 1) = 'all'
        OR cb.organization_id = (SELECT organization_id FROM accounts_user WHERE username = '${prof.user}'))`) ?? -1);
    if (rows > 0) ok(`「${name}」有 ${rows} 行（库里该账号范围内共 ${visible} 单）`);
    else if (visible > 0) bad(`「${name}」一行都没有，而库里这个账号的范围内有 ${visible} 单 —— 数据被界面吞掉了`);
    else if (visible === 0) ok(`「${name}」是空的，但库里该账号范围内确实一单也没有（不是界面的问题）`);
    else ok(`「${name}」是空的；没有 DATABASE_URL，无法区分「界面吞了」和「本来就没有」`);
  }
  if (prof.openPage) {
    const [name, path] = prof.openPage;
    current = name;
    await page.goto(`${BASE}${path}`, { waitUntil: "networkidle" });
    await assertAppPage(page, path, API);
    const heads = await page.$$eval("h2, h3", (hs) => hs.map((h) => h.textContent ?? "").join(" "));
    if (/没有查看/.test(heads)) bad(`进不了「${name}」—— 权限门要的点和它实际需要的对不上`);
    else ok(`「${name}」打得开`);
  }

  // ── 只读角色不该看到写按钮 ──
  if (prof.noWriteButtons) {
    const w = prof.noWriteButtons;
    current = w.name;
    console.log("\u2500\u2500 只读角色的写按钮 \u2500\u2500");
    await page.goto(`${BASE}${w.path}`, { waitUntil: "networkidle" });
    await assertAppPage(page, w.path, API);
    if (w.tab) {
      const t = page.locator(`button:has-text("${w.tab}")`).first();
      if (await t.count()) { await t.click(); await page.waitForTimeout(1200); }
    }
    // 排除页签：「收付款核销」是一个 tab 的名字，不是一颗写按钮。
    // 不排除的话它会一直被当成"只读角色看得见核销按钮"——
    // 报错原因不对，比不报更伤：修的人会去改一个没坏的东西。
    const btns = await page.$$eval("button:not([role='tab'])",
      (bs) => bs.map((b) => (b.textContent ?? "").trim()).filter(Boolean));
    const shown = w.labels.filter((l) => btns.some((b) => b.includes(l)));
    if (shown.length) {
      bad(`「${w.name}」对只读角色仍显示写按钮：${shown.join("、")}` +
        `（都要 ${w.need}，点下去只会弹一句"无权限"）`);
    } else {
      ok(`「${w.name}」没有对只读角色显示写按钮`);
    }
  }

  // ── 该挡住的面要说人话 ──
  console.log("\u2500\u2500 挡住的面 \u2500\u2500");
  const blockedNames = new Set();
  for (const [name, path, perm] of prof.blocked) {
    current = name;
    blockedNames.add(name);
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

  // ── 能进的页面上不该有没被说明的 403 ──
  console.log("\u2500\u2500 意外的 403 \u2500\u2500");
  const unexplained = [...forbidden.entries()].filter(([pg]) => !blockedNames.has(pg));
  if (unexplained.length) {
    for (const [pg, paths] of unexplained) {
      bad(`「${pg}」上有被拒的接口，而这一页是让${prof.label}进的：${[...paths].join(" ")}`);
    }
  } else {
    ok("能进的页面上没有被拒的接口");
  }

  await ctx.close();
}

await browser.close();
console.log(fail.length
  ? `\n\u2717 受限角色走查有 ${fail.length} 处不对`
  : `\n\u2713 两个受限角色（客服 / 财务只读）该用的用得了、该挡的说清了`);
process.exit(fail.length ? 1 : 0);
