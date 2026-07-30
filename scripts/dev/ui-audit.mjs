// 界面密度与可读性量化审计：给"专业性"装一把尺子。
//
// 为什么要有这个脚本：改版好不好看是主观的，但"一屏能看几行数据"、"垂直空间
// 有多少被壳子吃掉"、"正文对比度够不够 AA"都是客观数字。没有数字，重做完只能
// 说"感觉专业了"；有数字，才能说"首屏数据行 9 → 17，壳子占比 47% → 26%"。
//
// 用法（需先起好 :8000 网关与 :5173 前端）：
//   node scripts/dev/ui-audit.mjs [baseUrl] [--shots 输出目录]
// 输出：每页每主题一行指标 + 汇总；可选写 PNG 截图供人工核对。
const { chromium } = await import(
  process.env.PLAYWRIGHT_PKG ?? "/opt/node22/lib/node_modules/playwright/index.js"
).then((m) => m.default ?? m);
import { mkdirSync } from "node:fs";

const argv = process.argv.slice(2);
const BASE = argv.find((a) => !a.startsWith("--")) ?? "http://127.0.0.1:5173";
const shotIdx = argv.indexOf("--shots");
const SHOTS = shotIdx >= 0 ? (argv[shotIdx + 1] ?? "shots") : null;
// 1680×1000 ≈ 13" 笔记本外接屏的常见工作区，调度台的真实使用环境
const VIEWPORT = { width: 1680, height: 1000 };

const PAGES = [
  ["驾驶舱", "/"],
  ["订单管理", "/waybills"],
  ["调度工作台", "/dispatch-board"],
  ["对账中心", "/reconciliation"],
  ["资源库", "/fleet"],
  ["计价规则", "/pricing"],
];

// ── 页内测量：在浏览器上下文里跑，直接读真实布局 ──
const MEASURE = () => {
  const vh = window.innerHeight;
  const px = (el, p) => parseFloat(getComputedStyle(el)[p]) || 0;

  // 数据行：表格 tbody 的 tr，或调度台的卡片列表项
  const rows = [...document.querySelectorAll(".dt-table tbody tr, .table tbody tr")];
  const visibleRows = rows.filter((r) => {
    const b = r.getBoundingClientRect();
    return b.top >= 0 && b.bottom <= vh && b.height > 0;
  });
  const rowH = rows.length ? Math.round(rows[0].getBoundingClientRect().height) : null;

  // 首个数据行的 top = 这一屏被"壳子"吃掉的垂直空间
  const firstRow = rows.find((r) => r.getBoundingClientRect().height > 0);
  const chromeTop = firstRow ? Math.round(firstRow.getBoundingClientRect().top) : null;

  // 壳子构成：逐块量高度，找出到底谁在吃空间
  const chrome = {};
  for (const [name, sel] of [
    ["topbar", ".topbar"],
    ["pageIntro", ".page-intro"],
    ["toolbar", ".dt-toolbar, .toolbar"],
    ["tableHead", ".dt-table thead, .table thead"],
    ["stats", ".om-stats, .kpi-row"],
    ["tabs", ".seg-tabs"],
  ]) {
    const el = document.querySelector(sel);
    if (el) chrome[name] = Math.round(el.getBoundingClientRect().height);
  }

  // 正文字号：取表格单元格与页面主字号
  const cell = document.querySelector(".dt-table tbody td, .table tbody td");
  const body = document.body;

  // 对比度：解析 computed color，沿祖先找第一个不透明背景
  const parse = (c) => {
    const m = c.match(/[\d.]+/g);
    if (!m) return null;
    return { r: +m[0], g: +m[1], b: +m[2], a: m[3] === undefined ? 1 : +m[3] };
  };
  const lum = ({ r, g, b }) => {
    const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
    return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
  };
  const bgOf = (el) => {
    for (let n = el; n; n = n.parentElement) {
      const c = parse(getComputedStyle(n).backgroundColor);
      if (c && c.a >= 0.95) return c;
    }
    return { r: 255, g: 255, b: 255, a: 1 };
  };
  const ratio = (el) => {
    const fg = parse(getComputedStyle(el).color);
    if (!fg) return null;
    const bg = bgOf(el);
    // 半透明前景先合成到背景上，否则算出来偏乐观
    const mix = fg.a >= 1 ? fg : {
      r: fg.r * fg.a + bg.r * (1 - fg.a),
      g: fg.g * fg.a + bg.g * (1 - fg.a),
      b: fg.b * fg.a + bg.b * (1 - fg.a),
    };
    const [a, b] = [lum(mix), lum(bg)].sort((x, y) => y - x);
    return Math.round(((a + 0.05) / (b + 0.05)) * 100) / 100;
  };

  // 彩色元素计数：颜色预算是否守住了。
  //
  // 判定用**绝对色差**（max−min 通道差），不是饱和度。
  // 第一版用了相对彩度 (mx−mn)/mx，结果暗色主题量出 197 个"彩色元素"——
  // 深色中性灰 #171a20 的相对彩度是 (32−23)/32 = 0.28，看着像"28% 饱和"，
  // 实际只差 9/255 ≈ 3.5%，肉眼就是一块中性深灰。分母太小把它放大了。
  // 绝对色差下：#171a20 → 3.5%（不算）、强调蓝 #2454c8 → 64%、
  // 红 #c62b30 → 61%、暗色蓝 #6f9cf5 → 53%，量纲在两个主题下一致。
  const chroma = (c) => {
    if (!c) return 0;
    return (Math.max(c.r, c.g, c.b) - Math.min(c.r, c.g, c.b)) / 255;
  };
  // 图表内部不计入：图表的本职就是用颜色编码，把它算进"颜色预算"没有意义。
  // 预算管的是台账/表单/导航这些地方的颜色是否泛滥。
  // 图例色块也算图表的一部分：它和条形/扇形是同一套编码，不是额外的颜色开销。
  const inChart = (el) => el.closest(".recharts-wrapper, .ct-bar, .ct-legend, svg, .kpi-spark") !== null;
  let colored = 0;
  const coloredSamples = [];
  for (const el of document.querySelectorAll("body *")) {
    const b = el.getBoundingClientRect();
    if (b.top < 0 || b.bottom > vh || b.width === 0 || b.height === 0) continue;
    // 只算叶子节点与小元素，否则一个带色背景的容器会把它所有子节点都算一遍
    if (el.children.length > 2 || b.width > 400) continue;
    if (inChart(el)) continue;
    const cs = getComputedStyle(el);
    const fg = parse(cs.color), bg = parse(cs.backgroundColor);
    const hot = chroma(fg) > 0.30 || (bg && bg.a > 0.15 && chroma(bg) > 0.22);
    if (!hot) continue;
    // 文本节点或有背景的小块才算；纯容器不算
    const hasText = [...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim());
    const hasBg = bg && bg.a > 0.15;
    if (!hasText && !hasBg) continue;
    colored += 1;
    if (coloredSamples.length < 8) coloredSamples.push(`${el.className || el.tagName}`.slice(0, 34));
  }

  // 点击目标尺寸（WCAG 2.5.8 AA：≥24×24 CSS px）。
  // 这一条我此前完全没量过——尺子只关心"看得清吗"，没关心"点得中吗"。
  // 一天点几百次的界面上，24px 是"不用瞄准"的下限。
  //
  // 量的是**有效点击区**，不是控件自己的盒子。两类要特别处理，
  // 否则会报出一堆其实合规的假阳性：
  //   1. 控件外面套着更大的可点区域（<label>、勾选列的整个单元格）——
  //      真实目标是那个外层，14px 的勾选框实际点击区是 34×32 的 td。
  //   2. WCAG 自己的 inline 例外：目标处在文本流里、高度由行高决定
  //      （表格单元格里的单号链接就是这种），不适用 24px 要求。
  const SMALL_TARGET_OK = /dt-resizer|file-input-accessible|sr-only/;
  // 这些祖先本身就是点击区，控件被它们包着时按祖先量
  const CLICK_WRAPPER = "label, .cell-check, .file-trigger, .switch-mini, .checkline, .role-pick";
  const smallTargets = [];
  for (const el of document.querySelectorAll('button, a[href], input:not([type="hidden"]), select, [role="button"], [role="tab"], [role="option"], summary')) {
    if (SMALL_TARGET_OK.test(el.className || "")) continue;
    const wrapper = el.closest(CLICK_WRAPPER);
    const target = wrapper && wrapper !== el ? wrapper : el;
    const b = target.getBoundingClientRect();
    if (b.width === 0 || b.height === 0) continue;         // 未渲染/隐藏
    if (b.top < 0 || b.bottom > vh) continue;              // 不在首屏
    // WCAG 2.5.8 的 inline 例外：目标处在文本流里、尺寸由非目标文本的行高决定。
    // 表格单元格里的单号链接正是这种——它的高度就是行高，而行高是密度档决定的。
    // （不能用 computed display 判断：外层 span 是 inline-flex 时，
    //   里面的 <a> 作为 flex item 会被 blockify 成 flex，看着就不像 inline 了。）
    if (target.tagName === "A" && target.closest("td, p, li")) continue;
    if (b.width < 24 || b.height < 24) {
      const id = `${target.tagName.toLowerCase()}.${(target.className || "").split(" ")[0] || "-"}`;
      smallTargets.push(`${id} ${Math.round(b.width)}×${Math.round(b.height)}`);
    }
  }
  // 同一种控件出现几十次，去重后只报种类
  const smallTargetKinds = [...new Set(smallTargets)];

  // 抽样：正文、次要文字、表头、标签
  const contrast = {};
  for (const [name, sel] of [
    ["cell", ".dt-table tbody td, .table tbody td"],
    ["cellSub", ".cell-sub"],
    ["tableHead", ".dt-table thead th, .table thead th"],
    ["muted", ".muted"],
    ["label", ".form-label, .field-label, label"],
  ]) {
    const el = document.querySelector(sel);
    if (el) { const r = ratio(el); if (r) contrast[name] = r; }
  }

  return {
    visibleRows: visibleRows.length,
    totalRows: rows.length,
    rowH,
    chromeTop,
    // 容量 = 这一屏最多能放几行。"实际可见行数"会被数据量拖低
    // （调度池只有 2 单、计价只有 4 条规则），量不出布局的好坏。
    capacity: rowH && chromeTop != null ? Math.floor((vh - chromeTop) / rowH) : null,
    colored,
    coloredSamples,
    smallTargetKinds,
    chromePct: chromeTop == null ? null : Math.round((chromeTop / vh) * 100),
    chrome,
    cellFont: cell ? px(cell, "fontSize") : null,
    bodyFont: px(body, "fontSize"),
    contrast,
  };
};

if (SHOTS) mkdirSync(SHOTS, { recursive: true });

const browser = await chromium.launch({ executablePath: "/opt/pw-browsers/chromium", args: ["--no-sandbox"] });
const ctx = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: 2 });
const page = await ctx.newPage();

await page.goto(`${BASE}/login`, { waitUntil: "networkidle" });
await page.locator("input").nth(0).fill("admin");
await page.locator("input").nth(1).fill("Admin12345!");
await page.keyboard.press("Enter");
await page.waitForTimeout(3000);

const results = [];
for (const theme of ["light", "dark"]) {
  await page.evaluate((t) => {
    document.documentElement.dataset.theme = t;
    localStorage.setItem("tms-theme", t); // 键名必须和 api/theme.ts 的 KEY 一致，否则导航后被读回覆盖
  }, theme);
  for (const [name, path] of PAGES) {
    await page.goto(BASE + path, { waitUntil: "networkidle" }).catch(() => {});
    await page.waitForTimeout(1800);
    const m = await page.evaluate(MEASURE);
    results.push({ theme, name, path, ...m });
    if (SHOTS) {
      const file = `${SHOTS}/${theme}-${path.replace(/\W+/g, "_") || "home"}.png`;
      await page.screenshot({ path: file });
    }
  }
}
await browser.close();

// ── 报告 ──
const pad = (s, n) => String(s ?? "—").padEnd(n, " ");
const padL = (s, n) => String(s ?? "—").padStart(n, " ");
console.log(`\n视口 ${VIEWPORT.width}×${VIEWPORT.height}\n`);
console.log(`${pad("主题/页面", 22)}${padL("容量", 6)}${padL("实到", 6)}${padL("行高", 6)}${padL("壳高", 6)}${padL("壳占比", 8)}${padL("字号", 6)}${padL("彩色", 6)}   对比度`);
console.log("─".repeat(110));
for (const r of results) {
  const c = Object.entries(r.contrast).map(([k, v]) => `${k} ${v}`).join("  ");
  console.log(
    pad(`${r.theme === "light" ? "亮" : "暗"} ${r.name}`, 22) +
    padL(r.capacity, 6) + padL(r.visibleRows, 6) + padL(r.rowH, 6) + padL(r.chromeTop, 6) +
    padL(r.chromePct == null ? "—" : r.chromePct + "%", 8) + padL(r.cellFont, 6) + padL(r.colored, 6) + "   " + c
  );
}

// 点击目标：同一种控件在多页重复，汇总成一张种类表
const allSmall = new Map();
for (const r of results) for (const k of r.smallTargetKinds) {
  if (!allSmall.has(k)) allSmall.set(k, new Set());
  allSmall.get(k).add(r.name);
}
if (allSmall.size) {
  console.log(`\n✗ 点击目标小于 24×24（WCAG 2.5.8 AA）的 ${allSmall.size} 种：`);
  for (const [k, pages] of [...allSmall].sort()) console.log(`  ${k}   ← ${[...pages].join("、")}`);
} else {
  console.log("\n✓ 首屏点击目标均 ≥ 24×24");
}

console.log("\n壳子构成（亮色，谁在吃垂直空间）：");
for (const r of results.filter((x) => x.theme === "light")) {
  const parts = Object.entries(r.chrome).map(([k, v]) => `${k}=${v}`).join(" ");
  console.log(`  ${pad(r.name, 14)} ${parts}`);
}

// AA 底线：正文 ≥4.5，把跌破的挑出来
const AA = 4.5;
const bad = [];
for (const r of results) {
  for (const [k, v] of Object.entries(r.contrast)) if (v < AA) bad.push(`${r.theme} ${r.name} ${k}=${v}`);
}
const withRows = results.filter((r) => r.capacity != null);
const avgCap = withRows.length ? (withRows.reduce((s, r) => s + r.capacity, 0) / withRows.length).toFixed(1) : "—";
console.log(`\n首屏平均容量（行）：${avgCap}`);
if (bad.length) {
  console.log(`\n✗ 对比度低于 AA(${AA}) 的 ${bad.length} 处：`);
  for (const b of bad) console.log(`  ${b}`);
} else {
  console.log(`\n✓ 抽样文本对比度均 ≥ AA(${AA})`);
}

// 颜色预算：一屏彩色元素上限。颜色只有稀缺时才是信号——
// 改版前订单管理首屏有 51 个（17 个蓝单号 + 17 个彩色状态药丸 + 17 个蓝徽标），
// 到那个密度，真正要紧的东西（逾期、超时、异常）就淹在里面了。
//
// 按页面角色分档，不用一个数管到底：
//   作业面（台账/工作台/表单）——一天盯八小时，颜色必须稀缺，上限 12。
//   纵览面（驾驶舱）——管理者看几分钟，它的本职就是"哪里不对"，
//     告警色与趋势色都是信息，上限放到 24。
// 图表内部与图例不计入（见 inChart）。
const BUDGET = { 驾驶舱: 24 };
const BUDGET_DEFAULT = 12;
const budgetOf = (name) => BUDGET[name] ?? BUDGET_DEFAULT;
const overBudget = results.filter((r) => r.colored > budgetOf(r.name));
console.log("\n一屏彩色元素（上限）：");
for (const r of results.filter((x) => x.theme === "light")) {
  console.log(`  ${pad(r.name, 14)} ${padL(r.colored, 3)} / ${padL(budgetOf(r.name), 2)}   ${r.coloredSamples.join(", ")}`);
}
if (overBudget.length) {
  console.log(`\n✗ 超出颜色预算的 ${overBudget.length} 处：`);
  for (const r of overBudget) console.log(`  ${r.theme} ${r.name} = ${r.colored} > ${budgetOf(r.name)}`);
} else {
  console.log("\n✓ 各页彩色元素均在预算内");
}
process.exit(bad.length || overBudget.length || allSmall.size ? 1 : 0);
