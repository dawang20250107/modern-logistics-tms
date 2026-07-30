import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// 设计 token 的引用完整性。
//
// 这类问题不会报错，只会静默地不生效：ProjectPicker 里
// `var(--chip-bg, #eef2ff)` 引的是一个从未定义过的变量，于是一直用写死的
// 浅蓝兜底，暗色主题下不跟随；上一轮删掉 --dt-zebra 定义时，也差点留下悬空引用。
//
// 靠肉眼审查抓不住——颜色"看着有"就以为对了。所以钉成测试。

// 从本文件位置推导 src 根：依赖 process.cwd() 的话，从别的目录启动 vitest 就会扫错地方
const SRC = join(dirname(fileURLToPath(import.meta.url)), "..");

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    // 排除测试自身：用例名与注释里会写 var(--x) 这类示意
    else if (/\.(tsx?|css)$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) out.push(p);
  }
  return out;
}

const files = walk(SRC);
// 剥掉注释再扫：注释里会引用/讨论已删除的 token（例如解释为什么删了 --dt-zebra），
// 那不是活引用
const stripComments = (t: string) => t.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
const css = stripComments(readFileSync(join(SRC, "styles.css"), "utf8"));
// 同一行里常写多个定义（--red: x; --red-weak: y;），所以不能只匹配行首
const defined = new Set([...css.matchAll(/--[a-z0-9-]+\s*:/g)].map((m) => m[0].replace(/\s*:$/, "")));

describe("设计 token", () => {
  it("每个被引用的 var(--x) 都有定义", () => {
    const dangling = new Map<string, string[]>();
    for (const f of files) {
      const text = stripComments(readFileSync(f, "utf8"));
      for (const m of text.matchAll(/var\((--[a-z0-9-]+)/g)) {
        const name = m[1];
        if (defined.has(name)) continue;
        const rel = f.slice(SRC.length);
        dangling.set(name, [...(dangling.get(name) ?? []), rel]);
      }
    }
    const report = [...dangling.entries()].map(([k, v]) => `${k} ← ${[...new Set(v)].join(", ")}`);
    expect(report, "引用了未定义的 token（会静默失效或退回写死的 fallback）").toEqual([]);
  });

  it("亮色与暗色定义同一组 token，暗色不缺项", () => {
    const block = (sel: string) => {
      const i = css.indexOf(sel);
      if (i < 0) return null;
      const open = css.indexOf("{", i);
      let depth = 0, j = open;
      for (; j < css.length; j++) {
        if (css[j] === "{") depth++;
        else if (css[j] === "}") { depth--; if (depth === 0) break; }
      }
      return css.slice(open, j);
    };
    const light = block(":root {");
    const dark = block(':root[data-theme="dark"]');
    expect(light, ":root 未找到").toBeTruthy();
    expect(dark, "暗色主题块未找到").toBeTruthy();

    const names = (s: string) => new Set([...s.matchAll(/(--[a-z0-9-]+)\s*:/g)].map((m) => m[1]));
    const lightNames = names(light!);
    const darkNames = names(dark!);

    // 暗色不必覆盖全部（间距、字号、圆角等与主题无关），只校验颜色类 token：
    // 这些一旦漏改，暗色下就会沿用亮色值——白底黑字里混进一块亮色。
    // `row` 这个词根同时命中 --dt-row-bg（颜色）和 --row-h-compact（尺寸），
    // 所以尺寸类要先排掉，否则会把行高误判成"暗色缺的颜色"。
    const sizeish = /^--row-h-/;
    const colorish = /(bg|panel|ink|line|muted|faint|surface|row|head|weak|glass|shadow|side|hero|chart|dt-)/;
    const missing = [...lightNames].filter((n) => colorish.test(n) && !sizeish.test(n) && !darkNames.has(n));
    // 别名层（值本身就是另一个 token，如 --green-bg: var(--green-weak)）不需要在
    // 暗色重定义——它跟着被引用的那个变。自动识别比维护一份手工豁免名单可靠：
    // 名单会过期，别名关系不会。
    const aliases = new Set(
      [...light!.matchAll(/(--[a-z0-9-]+)\s*:\s*var\(/g)].map((m) => m[1]),
    );
    // 语义色板（红/琥珀/绿/蓝/紫的 -weak/-line）在两个主题下共用同一组值：
    // 它们是状态语义，不随明暗改变含义。
    const semantic = /^--(red|amber|green|blue|violet)-(weak|line)$/;
    // 与明暗无关的量（阴影、模糊、图表色板、侧栏与 hero 自带深色语义）
    // 与明暗无关的量：阴影、模糊、图表色板、侧栏与 hero 自带深色语义，
    // 以及锚定在强调色上的前景色（--accent-ink：强调色两个主题下都是蓝，字都该是白）
    const themeAgnostic = /^--(shadow|glass-blur|chart|side|hero|accent-ink)/;
    const real = missing.filter((n) => !aliases.has(n) && !semantic.test(n) && !themeAgnostic.test(n));
    expect(real, "暗色主题缺少这些颜色 token（会沿用亮色值）").toEqual([]);
  });
});
