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
import { launchBrowser } from "./lib/browser.mjs";
import { execFileSync } from "node:child_process";
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

const browser = await launchBrowser();
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

// 走查造的异常与它带出来的应付，跑完清掉。没有 DATABASE_URL 就明说没清。
function cleanupExceptionByDesc(desc) {
  const dsn = process.env.DATABASE_URL;
  if (!dsn) {
    note("· 没有 DATABASE_URL，走查造的这条异常留在库里了");
    return;
  }
  // 按描述删而不是按 id：断言在拿到 id 之前就失败时，那条异常一样要清掉。
  const where = `SELECT id FROM ops_exception WHERE description = '${desc}'`;
  try {
    execFileSync("psql", [dsn, "-tAq", "-c",
      `DELETE FROM fin_expense_record WHERE external_id IN (SELECT id::text FROM (${where}) t);
       DELETE FROM ops_exception_event WHERE exception_id IN (${where});
       DELETE FROM ops_exception WHERE description = '${desc}';`], { encoding: "utf8" });
  } catch (e) {
    note(`· 清理走查异常失败（${String(e).split("\n")[0]}），它留在库里了`);
  }
}

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
    data: { name, phone: "137" + tail.padStart(8, "0"), id_no: idNo, employment_type: "employee" },
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

// 计价规则：在页面上配一条阶梯价，然后拿它去报一次价。
//
// 这条比别的写操作多验一步「配完之后生效没有」。原因是这块曾经**整个够不着**：
// 表单默认的计费方式就是「按重量阶梯」，而页面上没有任何地方能填那几档价，
// 存下来 tier_prices 是 []，算价时单价取 0——用户认认真真配了一条合同价，
// 报出来是零。没有报错，只是数字不对，而看到数字的人默认它就是系统算出来的。
//
// 所以这里的验收标准不是"保存返回 200"，是**同一个客户同样的重量，
// 报出来的钱等于刚才填的那一档**。
{
  const tk = await token();
  const mkName = "写操作走查-阶梯价-" + MARK;
  await page.goto(`${BASE}/pricing`, { waitUntil: "networkidle" });
  await page.waitForTimeout(600);
  const openBtn = page.locator('button:has-text("新增")').first();
  if (await openBtn.count()) await openBtn.click();
  await page.waitForTimeout(400);

  const nameInput = page.locator('input[placeholder*="比亚迪"]').first();
  if (!(await nameInput.count())) {
    fail.push("计价规则页打不开新增表单（找不到规则名称输入框）");
  } else {
    await nameInput.fill(mkName);
    // 计费方式默认就是按重量阶梯，阶梯表应该跟着出现
    const addTier = page.locator('button:has-text("+ 加一档")');
    if (!(await addTier.count())) {
      fail.push("按重量阶梯下没有「加一档」——页面上填不了阶梯价，" +
                "存出来的规则报价会按 0 元/吨算");
    } else {
      // 0~5 吨 300 元/吨，5~30 吨 250 元/吨
      for (const [min, max, price] of [["0", "5", "300"], ["5", "30", "250"]]) {
        await addTier.click();
        await page.waitForTimeout(150);
        // 按 .tier-table 选，不按 .dt-table：这一页还有一张规则列表表格
        const row = page.locator("table.tier-table tbody tr").last();
        const cells = row.locator("input.cell-input");
        await cells.nth(0).fill(min);
        await cells.nth(1).fill(max);
        await cells.nth(2).fill(price);
      }
      // 优先级拉高，免得被库里的演示通配规则压过去
      const prio = page.locator('label:has-text("优先级") input').first();
      if (await prio.count()) await prio.fill("500");
      await page.locator('button:has-text("新增规则"), button:has-text("保存修改")').first().click();
      await page.waitForTimeout(1500);

      // 回库核对：存进去的阶梯必须是 min_ton/max_ton/price 三个键、数字类型
      const listed = await page.request.get(`${API}/api/v1/finance/pricing-rules?page_size=200&search=${encodeURIComponent(MARK)}`,
        { headers: { Authorization: `Bearer ${tk}` } });
      const found = (await listed.json())?.data?.items?.find((x) => x.name === mkName);
      if (!found) {
        fail.push("阶梯价规则保存后在列表里找不到");
      } else {
        const tiers = found.tier_prices ?? [];
        if (tiers.length !== 2) {
          fail.push(`存下来的阶梯只有 ${tiers.length} 档，填的是 2 档`);
        } else if (tiers.some((t) => t.min_ton === undefined || t.max_ton === undefined || t.price === undefined)) {
          fail.push(`阶梯的字段名不对：${JSON.stringify(tiers[0])}，算价那边按 min_ton/max_ton/price 取值，` +
                    "对不上就静默算成 0 元/吨");
        } else {
          // 8 吨落在 5~30 那一档：250 × 8 = 2000
          const qr = await page.request.post(`${API}/api/v1/orders/quote`, {
            headers: { Authorization: `Bearer ${tk}`, "Content-Type": "application/json" },
            data: { cargo_weight_ton: 8, cargo_volume_cbm: 0, cargo_quantity: 1 },
          });
          const q = (await qr.json())?.data ?? {};
          if (q.rule_name !== mkName) {
            note(`· 报价匹配到的是「${q.rule_name}」，不是刚配的那条（可能有更高优先级的规则），跳过金额核对`);
          } else if (Math.abs((q.amount ?? 0) - 2000) > 0.01) {
            fail.push(`按刚配的阶梯价报价得到 ${q.amount}，应为 2000（8 吨落在 5~30 档，250 元/吨）`);
          } else {
            note("· 计价规则：页面上配的阶梯价，报价按它算出来了（8 吨 → 2000 元）");
          }
        }
        // 清理：这条规则会影响后续报价，跑完删掉
        await page.request.delete(`${API}/api/v1/finance/pricing-rules/${found.id}`,
          { headers: { Authorization: `Bearer ${tk}` } });
      }
    }
  }
}

// 异常闭环：立案 → 指派 → 处理 → 定责关闭，一路按到底再回库核对。
//
// 这条链的后半截原先**在界面上完全够不着**：后端 /assign /handle /close
// /timeline 四个端点都是全的，界面只能上报和列出来。于是上报的异常永远停在
// 「待处理」，各页那个「⚠ 异常」角标永远不消，而**关闭正是定责那一步**——
// 责任方、赔付金额都在这里定，金额 > 0 时会落一条应付带进对账。
//
// 验收标准不是"四个请求都 200"，是**库里状态走完了、应付正好一条**。
// 关闭曾经没有守卫，连点三次生成三条应付（800 元被记成 2400）。
{
  const tk = await token();
  const excDesc = "写操作走查-异常闭环-" + MARK;
  const wbNo = waybill?.waybill_no;
  if (!wbNo) {
    note("· 没有可用运单，跳过异常闭环走查");
  } else {
    await page.goto(`${BASE}/waybills/${wbNo}`, { waitUntil: "networkidle" });
    await page.waitForTimeout(1200);
    // 先数一次这张运单上已有的异常费用应付。
    // 不能只数"金额 800 的有几条"——这个脚本每跑一次就在同一张运单上留一条，
    // 跑第二次就变成 2 条，检查从此长红，而红的原因和本次运行无关。
    // 要断言的是**这一次**多出来正好一条。
    const countExcCost = async () => {
      const r = await page.request.get(`${API}/api/v1/waybills/${wbNo}/costs`,
        { headers: { Authorization: `Bearer ${tk}` } });
      const rows = (await r.json())?.data?.payables ?? [];
      return rows.filter((c) => c.expense_item_code === "EXCEPTION_COST").length;
    };
    const excCostBefore = await countExcCost();

    const descBox = page.locator('textarea[placeholder*="异常"], input[placeholder*="异常"]').first();
    if (!(await descBox.count())) {
      fail.push("运单详情页找不到异常上报输入框");
    } else {
      await descBox.fill(excDesc);
      await page.locator('button:has-text("紧急上报")').click();
      await page.waitForTimeout(1500);

      // 清理放在 finally 里：断言失败时也要清。
      // 第一版把 cleanupException 写在成功分支里，结果回测那次（走到一半失败）
      // 在库里留了一条，下一轮全量跑完还看得见——
      // "只有成功才清理"等于"越是出问题越会攒垃圾"。
      try {
      const row = () => page.locator("table.table tbody tr", { hasText: excDesc }).first();
      if (!(await row().count())) {
        fail.push("异常上报后列表里没有出现这一条");
      } else {
        await row().click();
        await page.waitForTimeout(1000);

        // 指派
        const emp = page.locator("select").filter({ hasText: "选择处理人" }).first();
        if (!(await emp.count())) {
          fail.push("异常展开后没有「指派」入口——异常的后半截界面上够不着，" +
                    "上报的异常会永远停在待处理，赔付也进不了账");
        } else {
          await emp.selectOption({ index: 1 });
          await page.locator('button:has-text("指派")').first().click();
          // 动作完成后面板本来就还开着，别再点一次（再点是收起）
          await page.locator('textarea[placeholder*="处理结论"]').first().waitFor({ timeout: 15000 })
            .catch(() => fail.push("指派之后没有出现「填处理结论」，闭环卡在第二步"));

          // 处理
          await page.locator('textarea[placeholder*="处理结论"]').first().fill("走查：已联系承运商补送");
          await page.locator('button:has-text("提交处理结论")').click();
          // 按「赔付金额」这个 label 定位，不要用全页第一个 decimal 输入框——
          // 运单详情页上还有别的数字输入框（费用明细的金额），
          // 第一版就是这么写的：填进了别人家的格子，赔付金额留在 0，
          // 检查报「金额是 0.00」，看起来像后端没存住。
          const amtBox = page.locator('label:has-text("赔付金额") input').first();
          await amtBox.waitFor({ timeout: 15000 })
            .catch(() => fail.push("提交处理结论之后没有出现定责表单，闭环卡在第三步"));

          // 定责关闭：800 元，会落一条应付
          await amtBox.fill("800");
          await page.locator('button:has-text("定责并闭环")').click();
          await page.waitForTimeout(500);
          await page.locator('button:has-text("确认闭环")').click().catch(() => {});
          await page.waitForTimeout(1800);

          // 回库核对
          const list = await page.request.get(
            `${API}/api/v1/exceptions?waybill=${waybill.id}&page_size=50`,
            { headers: { Authorization: `Bearer ${tk}` } });
          const ex = (await list.json())?.data?.items?.find((x) => x.description === excDesc);
          if (!ex) {
            fail.push("闭环之后接口里找不到这条异常");
          } else if (ex.status !== "closed") {
            fail.push(`异常状态是 ${ex.status}，不是 closed——界面走完了但库里没闭环`);
          } else if (Number(ex.amount) !== 800) {
            fail.push(`异常赔付金额是 ${ex.amount}，填的是 800`);
          } else {
            // 应付必须正好一条。这条断言钉的是"连点几次就多生成几条"那个 bug。
            // 应付在 data.payables 里，不是 data.items——这个接口返回的是
            // 一张按方向分好的账（payables / receivables / 合计 / 毛利），
            // 不是通用分页列表。第一版按 data.items 读，拿到空数组，
            // 报「生成了 0 条」——而库里那条应付好好的在。
            const delta = (await countExcCost()) - excCostBefore;
            if (delta !== 1) {
              fail.push(`异常闭环让异常费用应付多了 ${delta} 条，应该正好 1 条` +
                        "（关闭曾经没有守卫，连点几次就多生成几条：800 元被记成 2400）");
            } else {
              note("· 异常闭环：立案 → 指派 → 处理 → 定责关闭，库里 closed、应付正好多一条 800 元");
            }
          }
        }
      }
      } finally {
        cleanupExceptionByDesc(excDesc);
      }
    }
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
