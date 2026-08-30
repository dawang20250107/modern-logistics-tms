import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it } from "vitest";

// 「请稍后重试」不能用在稍后也不会变的失败上。
//
// 这套系统栽过好几次同一件事：后端明确回报"这件事做不了/没做"，
// 前端却告诉用户"在做"或"再试试"——
//   · 回单 OCR 未接引擎时后端落 manual，界面一律显示「提取中」，永远转下去
//   · 找回密码没配下发通道，接口回 sent:false，前端照样进第二步
//   · /track 被限流时显示「未找到匹配订单」，用户越重输限流窗口越长
//   · 命令面板向未接入的 AI 提问，回「分析失败，请稍后重试」
//
// 共同点：真正的原因后端都说清楚了，是前端把它换成了一句更含糊、
// 而且**指向错误动作**的话。用户照着那句话做，只会把事情弄得更糟。
//
// 这条守的是最容易复发的那一种写法：空 catch 里直接扔一句"稍后重试"，
// 连拿到手的 error 都不看。

const SRC = resolve(process.cwd(), "src");

function tsxFiles(dir: string): string[] {
  const out: string[] = [];
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) out.push(...tsxFiles(p));
    else if (e.name.endsWith(".tsx") || e.name.endsWith(".ts")) out.push(p);
  }
  return out;
}

const strip = (s: string) =>
  s
    .replace(/\{\s*\/\*[\s\S]*?\*\/\s*\}/g, "")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/^\s*\/\/.*$/gm, "");

describe("失败要说实话", () => {
  it("没有「丢掉 error 再说一句稍后重试」的 catch", () => {
    const bad: string[] = [];
    for (const f of tsxFiles(SRC)) {
      if (f.includes("/test/")) continue;
      const src = strip(readFileSync(f, "utf8"));
      // catch 没有接收参数（catch { 或 catch () {），块里却在报错文案
      for (const m of src.matchAll(/catch\s*(?:\(\s*\))?\s*\{([\s\S]{0,240}?)\}/g)) {
        const block = m[1];
        if (/重试|失败了|稍后/.test(block) && /toast\.(error|warn)/.test(block)) {
          const line = src.slice(0, m.index).split("\n").length;
          bad.push(`${f.replace(SRC, "")}:${line}`);
        }
      }
    }
    expect(
      bad,
      "这些 catch 把后端给的原因丢掉了，只留一句「稍后重试」——" +
        "而真正的原因可能是「没配这个功能」这种稍后也不会变的事：\n  " +
        bad.join("\n  "),
    ).toEqual([]);
  });

  it("检查本身抓得住（假源码回测）", () => {
    const fake = `
      try { await x(); } catch { toast.error("分析失败，请稍后重试"); }
      try { await y(); } catch (e) { toast.error(e instanceof ApiError ? e.message : "失败"); }
    `;
    const hits: string[] = [];
    for (const m of strip(fake).matchAll(/catch\s*(?:\(\s*\))?\s*\{([\s\S]{0,240}?)\}/g)) {
      if (/重试|失败了|稍后/.test(m[1]) && /toast\.(error|warn)/.test(m[1])) hits.push(m[1].trim());
    }
    expect(hits).toHaveLength(1);
    expect(hits[0]).toContain("稍后重试");
  });
});
