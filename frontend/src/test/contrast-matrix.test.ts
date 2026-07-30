import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// 对比度矩阵：把「每个前景 token × 每种可能的背景」全枚举出来算一遍。
//
// 为什么需要这个而不是只靠 scripts/dev/ui-audit.mjs：
// 那个脚本在浏览器里取样，量的是"这个元素此刻所处的背景"。而一行数据会经历
// 六种底色（常态 / 表头 / 三级面 / hover / **选中** / 异常 tint），
// 取样只会碰到当时渲染出来的那一种。
// 结果就是 `--muted` 在选中行底上只有 4.62（AA 线上剩 0.12）这种事，
// 取样式的尺子一直量不到——它每次都恰好量在白底上，报 5.81。
//
// 另一个漏点是**非文本对比**：WCAG 1.4.11 要求 UI 组件边界 ≥3:1，
// 而输入框边框原来走 --line-2，在白底上只有 1.50。
// 取样脚本只算 color/background 的文字对比，根本没查边框。
//
// 这里直接读 styles.css 的 token 值做纯计算，不需要浏览器，跑在 CI 里。

const SRC = join(dirname(fileURLToPath(import.meta.url)), "..");
const CSS = readFileSync(join(SRC, "styles.css"), "utf8");

/** 取某个主题块里的 token 值（只认十六进制字面量；var() 别名不参与矩阵）。 */
function tokens(block: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const m of block.matchAll(/(--[a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{3,8})\s*;/g)) {
    out[m[1]] = m[2];
  }
  return out;
}
const lightBlock = CSS.slice(CSS.indexOf(":root {"), CSS.indexOf(':root[data-theme="dark"]'));
const darkBlock = CSS.slice(CSS.indexOf(':root[data-theme="dark"]'), CSS.indexOf("* { box-sizing: border-box; }"));
const LIGHT = tokens(lightBlock);
const DARK = { ...LIGHT, ...tokens(darkBlock) }; // 暗色只覆盖一部分，其余继承亮色

function toRgb(hex: string): [number, number, number] {
  let h = hex.replace("#", "");
  if (h.length === 3) h = h.split("").map((c) => c + c).join("");
  return [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16)) as [number, number, number];
}
function lum(hex: string): number {
  const f = (v: number) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
  const [r, g, b] = toRgb(hex).map((v) => f(v / 255));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
function ratio(a: string, b: string): number {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return Math.round(((hi + 0.05) / (lo + 0.05)) * 100) / 100;
}

// 一行数据在真实使用中会落到的全部底色
const BACKGROUNDS = ["--panel", "--panel-2", "--panel-3", "--dt-row-bg", "--dt-row-hover", "--dt-row-sel", "--dt-head-bg", "--red-weak", "--amber-weak", "--accent-weak"];
// 承载信息的前景（--faint 不在内：它是纯装饰，见 token 注释）
const FOREGROUNDS = ["--ink", "--ink-2", "--ink-3", "--muted", "--accent", "--red", "--amber", "--green", "--blue"];
// 非文本：可交互控件的边界、状态点、强调色轨
const NON_TEXT = ["--ctl-line", "--accent", "--dot-red", "--dot-amber", "--dot-green", "--dot-blue", "--dot-neutral"];

const AA_TEXT = 4.5;   // 1.4.3 正文
const AA_UI = 3.0;     // 1.4.11 非文本（UI 组件边界与状态图形）

function report(theme: string, T: Record<string, string>, fgs: string[], min: number) {
  const bad: string[] = [];
  for (const fg of fgs) {
    for (const bg of BACKGROUNDS) {
      if (!T[fg] || !T[bg]) continue;
      const r = ratio(T[fg], T[bg]);
      if (r < min) bad.push(`${theme} ${fg}(${T[fg]}) on ${bg}(${T[bg]}) = ${r}`);
    }
  }
  return bad;
}

describe("对比度矩阵 · 文字 ≥ AA(4.5)", () => {
  it("亮色：承载信息的前景 × 全部行底", () => {
    expect(report("light", LIGHT, FOREGROUNDS, AA_TEXT), "跌破 AA 的组合").toEqual([]);
  });
  it("暗色：承载信息的前景 × 全部行底", () => {
    expect(report("dark", DARK, FOREGROUNDS, AA_TEXT), "跌破 AA 的组合").toEqual([]);
  });
});

describe("对比度矩阵 · 非文本 ≥ 3:1（WCAG 1.4.11）", () => {
  // 状态色点带 1px 同语义色描边，所以点自身的底噪不必过 3:1——
  // 但描边色（= --red/--amber/... 前景色）必须过，那一条已在文字矩阵里覆盖。
  // 这里查的是控件边界与强调色轨：它们没有描边兜底。
  const UI_ONLY = ["--ctl-line", "--accent"];
  it("亮色：控件边界与强调色轨 × 全部底色", () => {
    expect(report("light", LIGHT, UI_ONLY, AA_UI), "UI 边界低于 3:1 的组合").toEqual([]);
  });
  it("暗色：控件边界与强调色轨 × 全部底色", () => {
    expect(report("dark", DARK, UI_ONLY, AA_UI), "UI 边界低于 3:1 的组合").toEqual([]);
  });
  it("状态点全部带同语义色描边（点自身对比不足时靠描边兜底）", () => {
    // 只要 .tag-dot 的 ::before 声明里带 box-shadow 或 border 就算有描边
    const dotRule = CSS.slice(CSS.indexOf(".tag-dot::before"), CSS.indexOf(".tag-dot::before") + 400);
    expect(dotRule, "状态点需要 1px 同语义色描边：灰点在浅底上只有 2.1:1，会被读成「没数据」").toMatch(
      /box-shadow:[^;]*inset|border:\s*1px|outline:/,
    );
    void NON_TEXT;
  });
});
