import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it } from "vitest";

// 凭证类文件（回单、附件、司机证件）的链接必须指向 file_display，不能指向 file_url。
//
// 这两个字段长得像，含义不同：
//   file_url      外链回单专用；后台上传的文件存的是 file，这个字段是空串
//   file_display  后端算好的"能打开的地址"——落盘的带 /media/ 前缀，外链的回落到 file_url
//
// 运单详情页的「查看原件」原先绑的是 file_url，于是上传上来的回单渲染成
// href="" 的链接：点一下只是刷新当前页。页面上有那一行、有 OCR 徽标、
// 什么都不报错，只有真去点才知道打不开——而回单是对账时要拿出来的凭证。
//
// 用扫源码而不是渲染组件，是因为这条规则跟状态无关：只要有人写了
// href={x.file_url}，不管什么数据都是错的。

// vitest 的工作目录是 frontend/（同 empty-state.test.ts）
const SRC = resolve(process.cwd(), "src");

function tsxFiles(dir: string): string[] {
  const out: string[] = [];
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) out.push(...tsxFiles(p));
    else if (e.name.endsWith(".tsx")) out.push(p);
  }
  return out;
}

describe("凭证链接", () => {
  it("没有任何 href 直接绑到 file_url", () => {
    const offenders: string[] = [];
    for (const f of tsxFiles(SRC)) {
      const src = readFileSync(f, "utf8")
        .replace(/\{\s*\/\*[\s\S]*?\*\/\s*\}/g, "")
        .replace(/\/\*[\s\S]*?\*\//g, "")
        .replace(/^\s*\/\/.*$/gm, "");
      src.split("\n").forEach((line, i) => {
        if (/href=\{[^}]*\.file_url[^}]*\}/.test(line)) {
          offenders.push(`${f.replace(SRC, "")}:${i + 1}`);
        }
      });
    }
    expect(
      offenders,
      `这些地方把链接绑到了 file_url：上传上来的文件在这里是空串，` +
        `渲染出的是 href="" 的死链。改用 file_display。\n  ${offenders.join("\n  ")}`,
    ).toEqual([]);
  });

  it("有文件字段的地方都对空值做了兜底（不渲染空链接）", () => {
    // file_display 也可能为空（既没上传、也没填外链）。直接渲染 <a href="">
    // 同样是死链，所以取值处必须有条件判断。
    const offenders: string[] = [];
    for (const f of tsxFiles(SRC)) {
      const src = readFileSync(f, "utf8");
      src.split("\n").forEach((line, i) => {
        if (!/href=\{[^}]*\.file_display[^}]*\}/.test(line)) return;
        // 同一行或紧邻两行里要能看到对该字段的判断
        const around = src.split("\n").slice(Math.max(0, i - 3), i + 1).join("\n");
        if (!/file_display\s*(\?|&&|!==|===)/.test(around)) {
          offenders.push(`${f.replace(SRC, "")}:${i + 1}`);
        }
      });
    }
    expect(offenders, `这些地方没判空就渲染了链接：\n  ${offenders.join("\n  ")}`).toEqual([]);
  });
});
