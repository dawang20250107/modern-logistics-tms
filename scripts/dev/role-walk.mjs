// 用受限角色真的登进去走一遍。
//
// 存在的理由：其余五条走查全用超管跑（admin 带 `*` 权限），
// 而超管**永远看不到 403 长什么样**。这一轮补的全是受限角色的体验：
// 订单读补闸、客服拿到建单权限、三条路由挂上权限门——
// 这些改动的好坏，只有拿一个受限账号真的点一遍才知道。
//
// 三个演示角色各走一遍：
//   客服（seed_cs）        waybill.view + waybill.create + masterdata.view，范围本网点
//   调度员（seed_dispatcher） 加 waybill.manage + carrier.view + telematics.view，范围子树
//   财务只读（seed_finance）  finance.view + waybill.view + analytics.view，范围全部
//
// 调度员那一档是**反方向**的：断言该有的写按钮必须在。
// 前面几轮给全站加了大量权限门，而没有一条检查在防"加多了"——
// 少给一个门只是多一次 403，多给一个门是让能干活的人干不了活，
// 而且没人会来报（他会以为这功能就是这样）。
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
// 对照组：超管。它带 `*` 权限，所有按钮都该看得到。
const SU_USER = process.env.TMS_USER ?? "admin";
const SU_PASS = process.env.TMS_PASS ?? "Admin12345!";

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
    // 真填表单、真点提交、再回库核对。
    //
    // waybill.create 那个权限点是为这一步加的（第七节验收链第一步就是
    // 「客服工作台建一单」，而客服原先被 403 挡住）。但那次只用 Go 用例
    // 验过——发的是 JSON。真实表单是另一条路：它自己拼 payload、
    // 自己做必填校验，权限对了而字段拼错一样建不出单。
    submitOrder: { name: "客服工作台建单", path: "/intake" },
  },
  {
    user: "seed_dispatcher",
    label: "调度员",
    perms: "carrier.view,masterdata.view,telematics.view,waybill.manage,waybill.view",
    navMust: ["调度工作台", "订单管理", "资源库"],
    navMustNot: ["对账中心", "计价规则", "组织与权限"],
    blocked: [["对账中心", "/reconciliation", "finance.view"]],
    listPage: ["订单管理", "/waybills"],
    // 反方向：这个角色**必须**看得到这些写按钮。
    // 前面几轮给全站加了大量权限门，而没有一条检查在防"加多了"——
    // 少给一个门只是多一次 403，多给一个门是让能干活的人干不了活，
    // 而且没人会来报（他会以为这功能就是这样）。
    // 跟超管在**同一张运单**上对照，而不是手写一串按钮名。
    // 手写的第一版就写错了：那张运单的合同已是「已确认」，
    // 「生成合同」对所有人都不显示，超管也没有——
    // 检查报的原因是假的，而假红比不报更伤。
    // 对照法还自带空转防护：超管都没有按钮时说明这一页没打开。
    mustMatchSuperuser: {
      name: "运单详情",
      path: "/waybills/LT-000000001",
      // 这些是超管有、这个角色本就不该有的，按权限点逐条写明
      allowedMissing: {
        "风险分析": "要 ai.use",
        "生成费用": "要 finance.manage",
      },
    },
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

async function buttonsOn(page, path) {
  await page.goto(`${BASE}${path}`, { waitUntil: "networkidle" });
  await assertAppPage(page, path, API);
  await page.waitForTimeout(1200);
  return page.$$eval("button:not([role='tab'])",
    (bs) => [...new Set(bs.map((b) => (b.textContent ?? "").trim()).filter(Boolean))]);
}

// 超管那一份单开一个浏览器上下文取，取完就关：对照组不能和被测角色共用会话。
async function superuserButtonsOn(path) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  try {
    await login(page, BASE, { user: SU_USER, pass: SU_PASS, api: API });
    return await buttonsOn(page, path);
  } finally {
    await ctx.close();
  }
}

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

  // ── 令牌到期时不能把人踢下线 ──
  //
  // 刷新是轮换的：后端签发新券的同时作废旧券。而驾驶舱一进来就并发发
  // 五个请求，令牌到期时它们**同时** 401 —— 五个刷新拿着同一张 refresh
  // 打出去，实测 3 个 200、2 个 401，而拿到 401 的会清空令牌把人踢走。
  // 刷新其实成功了，人却掉线；只在"页面开着好几个查询 + 恰好赶上到期"
  // 时出现，用户报上来也复现不了。
  //
  // 只在第一个档案上跑一次：这条验的是客户端的刷新逻辑，和角色无关。
  if (prof === PROFILES[0]) {
    current = "令牌到期";
    let refreshes = 0;
    const countRefresh = (r) => { if (r.url().includes("/auth/token/refresh")) refreshes++; };
    page.on("request", countRefresh);
    // 把 access 换成一张过期的，refresh 留着
    await page.evaluate(() => localStorage.setItem("access",
      "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
      "eyJ0b2tlbl90eXBlIjoiYWNjZXNzIiwidXNlcl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImV4cCI6MTAwMDAwMDAwMH0.bad"));
    refreshes = 0;
    await page.goto(`${BASE}/`, { waitUntil: "networkidle" });
    await page.waitForTimeout(2000);
    page.off("request", countRefresh);
    console.log("\u2500\u2500 令牌到期 \u2500\u2500");
    if (page.url().includes("/login")) {
      bad(`令牌到期后被踢回登录页（发了 ${refreshes} 次刷新）—— ` +
        `刷新是轮换的，并发刷新会有几个拿到 401，而拿到 401 就清空令牌`);
    } else if (refreshes > 1) {
      bad(`令牌到期时发了 ${refreshes} 次刷新 —— 旧券只能用一次，` +
        `多出来的那几次会拿到 401，迟早把人踢下线（这次侥幸没踢）`);
    } else {
      ok(`令牌到期后自动续上（刷新 ${refreshes} 次），人留在页面上`);
    }
  }

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

  // ── 真下一单 ──
  if (prof.submitOrder) {
    const so = prof.submitOrder;
    current = so.name;
    console.log("\u2500\u2500 真下一单 \u2500\u2500");
    const tag = "走查" + Date.now().toString(36).slice(-6);
    // 按**货品明细**上的标记找这一单，不是订单表头。
    // 第一版查的是 ops_order.cargo_desc，而表单把货品名放进 cargo_items——
    // 于是它报「客服点了提交，库里没有多出这一单」，而实测那次提交
    // 返回的是 201、页面上也弹了「建单成功」。**假红**。
    const found = () => `SELECT count(*) FROM ops_order_cargo_item WHERE name LIKE '${tag}%'`;
    const before = Number(q(found()) ?? -1);
    await page.goto(`${BASE}${so.path}`, { waitUntil: "networkidle" });
    await assertAppPage(page, so.path, API);
    await page.waitForTimeout(1000);
    const fill = async (ph, val) => {
      const el = page.locator(`input[placeholder="${ph}"]`).first();
      if (!(await el.count())) { bad(`建单表单上找不到「${ph}」输入框`); return false; }
      await el.fill(val);
      return true;
    };
    // 必填三项：始发、目的、货品名称（trySubmit 里逐条校验的就是这三个）
    // 注意占位符：写着「如 上海」的那个是**目的地**，「如 无锡」的是始发地。
    const okFill = (await fill("输入或选择，如 无锡", "上海"))
      && (await fill("输入或选择，如 上海", "杭州"))
      && (await fill("货品名称 *", tag + " 日用品"));
    if (okFill) {
      await page.locator('button:has-text("确认提交")').first().click();
      await page.waitForTimeout(2500);
      const after = Number(q(found()) ?? -1);
      const toast = (await page.textContent("body")) ?? "";
      if (after > before) {
        // 建单人必须是这个账号本人，而不是别人——否则数据范围会算错
        const who = q(`SELECT COALESCE(u.username,'(空)') FROM ops_order o
          JOIN ops_order_cargo_item ci ON ci.order_id = o.id
          LEFT JOIN accounts_user u ON u.id = o.created_by_id
          WHERE ci.name LIKE '${tag}%' ORDER BY o.created_at DESC LIMIT 1`);
        if (who !== prof.user) {
          bad(`单建出来了，但建单人记成了「${who}」而不是 ${prof.user} —— 数据范围按这一列算`);
        } else {
          ok(`${prof.label}在真实表单上建出了一单（建单人 ${who}）`);
        }
        // 清理：这是演示库，别留垃圾
        const mine = `SELECT order_id FROM ops_order_cargo_item WHERE name LIKE '${tag}%'`;
        q(`CREATE TEMP TABLE _walk_o AS ${mine};
           DELETE FROM ops_order_event WHERE order_id IN (SELECT order_id FROM _walk_o);
           DELETE FROM ops_order_cargo_item WHERE order_id IN (SELECT order_id FROM _walk_o);
           DELETE FROM ops_order_stop WHERE order_id IN (SELECT order_id FROM _walk_o);
           DELETE FROM ops_order WHERE id IN (SELECT order_id FROM _walk_o);`);
      } else if (before < 0) {
        ok("提交了，但没有 DATABASE_URL，无法回库核对");
      } else {
        const why = /缺少所需权限|权限|失败|错误/.exec(toast);
        bad(`${prof.label}点了「确认提交」，库里没有多出这一单` +
          (why ? `（页面上出现了「${why[0]}」）` : "（页面上也没有任何提示）"));
      }
    }
  }

  // ── 该有的写按钮不能被门挡掉 ──
  if (prof.mustMatchSuperuser) {
    const m = prof.mustMatchSuperuser;
    current = m.name;
    console.log("\u2500\u2500 该有的写按钮（跟超管对照）\u2500\u2500");
    const mine = await buttonsOn(page, m.path);
    const su = await superuserButtonsOn(m.path);
    // 空转防护：超管都没有按钮，说明这一页压根没打开（或者选错了运单），
    // 那时候"没有缺失"和"根本没比较"长得一模一样。
    if (su.length < 3) {
      bad(`超管在「${m.name}」上只有 ${su.length} 个按钮 —— 这一页多半没打开，本次对照不作数`);
    } else {
      const missing = su.filter((b) => !mine.includes(b) && !(b in m.allowedMissing));
      const unexpected = Object.keys(m.allowedMissing).filter((b) => mine.includes(b));
      if (missing.length) {
        bad(`「${m.name}」上这些按钮超管有、${prof.label}没有：${missing.join("、")}` +
          `（这个角色有 waybill.manage，本该看得到——门加多了）`);
      } else {
        ok(`跟超管对照：${su.length} 个按钮里少了 ${su.length - mine.length} 个，` +
          `都是写明理由的（${Object.entries(m.allowedMissing).map(([k, v]) => k + " " + v).join("、")}）`);
      }
      if (unexpected.length) {
        bad(`「${m.name}」上这些按钮${prof.label}不该看到：${unexpected.join("、")}`);
      }
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
  : `\n\u2713 三个受限角色（客服 / 调度员 / 财务只读）该用的用得了、该挡的说清了、该有的没被挡掉`);
process.exit(fail.length ? 1 : 0);
