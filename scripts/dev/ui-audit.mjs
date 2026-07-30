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
console.log(`${pad("主题/页面", 22)}${padL("容量", 6)}${padL("实到", 6)}${padL("行高", 6)}${padL("壳高", 6)}${padL("壳占比", 8)}${padL("字号", 6)}   对比度`);
console.log("─".repeat(110));
for (const r of results) {
  const c = Object.entries(r.contrast).map(([k, v]) => `${k} ${v}`).join("  ");
  console.log(
    pad(`${r.theme === "light" ? "亮" : "暗"} ${r.name}`, 22) +
    padL(r.capacity, 6) + padL(r.visibleRows, 6) + padL(r.rowH, 6) + padL(r.chromeTop, 6) +
    padL(r.chromePct == null ? "—" : r.chromePct + "%", 8) + padL(r.cellFont, 6) + "   " + c
  );
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
if (SHOTS) console.log(`\n截图写入 ${SHOTS}/`);
process.exit(bad.length ? 1 : 0);
