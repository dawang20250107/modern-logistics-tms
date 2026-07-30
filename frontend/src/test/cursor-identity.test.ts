import { describe, expect, it } from "vitest";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// 光标身份守卫：列表光标必须锚在记录 id 上，不能锚在数组下标上。
//
// 这条规则是从一个真实的派错货路径来的。调度台原来是 `useState(-1)` 的下标：
//   1. rows 每次渲染都重跑 sortUrgent（把临期/超时/VIP 顶到最前）
//   2. 三个订单池都是 refetchInterval: 15000
//   3. 某单的 SLA 翻成「临期」→ 被顶到列表最前 → 它下面所有行下移一位
//   4. focusIdx 不变 → 高亮框还停在原来那个 y，但那一行已经是另一张单
//   5. 按 Enter → 拉出的是**别人的货**的派单抽屉
//
// 像素一个都没动，所以肉眼看不出来；tsc 和现有单测也都不报。
// 唯一能防住的办法是把「光标锚 id」这件事变成可检查的约束。
//
// 这里用源码扫描而不是渲染测试：渲染测试要把三个轮询 query、拖拽、抽屉全 mock
// 出来才能触发重排，成本高且脆；而「有没有把下标存进 state」是一眼可判的。

const SRC = join(dirname(fileURLToPath(import.meta.url)), "..");

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) out.push(p);
  }
  return out;
}
const FILES = walk(SRC);
const rel = (f: string) => f.slice(SRC.length + 1);

describe("列表光标锚记录 id，不锚数组下标", () => {
  it("不把行/记录光标存成 useState 下标", () => {
    // 只拦「行光标」这一类命名。DataTable 内部的单元格选区（selA/selF）用的是
    // {r,c} 坐标，那是 Excel 语义下的正确模型——单元格坐标本身就是坐标，
    // 而且 DataTable 的行集合由调用方传入、在一次渲染内是稳定的。
    const CURSOR_STATE = /useState[^(]*\(\s*-1\s*\)/;
    const ROW_CURSOR_NAME = /\[\s*(focus|cursor|active|current|hilite|sel|selected)(Idx|Index|Row|RowIdx)\s*,/i;
    // 同时拦 useState(0) —— 命令面板原来就是 `useState(0)` 的 selectedIndex
    const CURSOR_STATE0 = /useState[^(]*\(\s*0\s*\)/;
    const hits: string[] = [];
    for (const f of FILES) {
      readFileSync(f, "utf8").split("\n").forEach((ln, i) => {
        if (ROW_CURSOR_NAME.test(ln) && (CURSOR_STATE.test(ln) || CURSOR_STATE0.test(ln))) {
          hits.push(`${rel(f)}:${i + 1} → ${ln.trim().slice(0, 90)}`);
        }
      });
    }
    expect(
      hits,
      "行光标请存记录 id（如 focusId），下标每次从当前 rows 里推。" +
        "列表一重排（轮询刷新、SLA 翻档、筛选变化），存着的下标就指向另一条记录了——" +
        "像素一个不动，但 Enter 会作用在错的行上。",
    ).toEqual([]);
  });

  it("命令面板的光标锚结果 key，不锚下标", () => {
    const src = readFileSync(join(SRC, "components", "SpotlightCommandBar.tsx"), "utf8");
    // 面板的重排是异步的：hits 由 lookupQ 拉回来后插在命令之前。
    // 用户在 hits 到达前按了两下 ↓，下标 2 指着某个命令；hits 一到，
    // 下标 2 已经是另一条记录——Enter 跳到别处。
    expect(src, "面板应持有 selectedKey（结果 key），不是 selectedIndex").toMatch(
      /const \[selectedKey, setSelectedKey\] = useState<string \| null>\(null\)/,
    );
    expect(src, "下标必须由 selectedKey 在当前 results 里 findIndex 推出").toMatch(
      /const selectedIndex = selectedKey \? results\.findIndex\(/,
    );
    expect(src, "每条结果都要带稳定 key（act:/hit:/cmd:/ai）").toMatch(/key: `act:/);
  });

  it("调度台的光标确实是 id 锚定，且下标是推导出来的", () => {
    const src = readFileSync(join(SRC, "pages", "DispatchBoardPage.tsx"), "utf8");
    expect(src, "调度台应持有 focusId（记录 id），不是 focusIdx").toMatch(
      /const \[focusId, setFocusId\] = useState<string \| null>\(null\)/,
    );
    expect(src, "focusIdx 必须由 focusId 在当前 rows 里 findIndex 推出，不能是独立 state").toMatch(
      /const focusIdx = focusId \? rows\.findIndex\(/,
    );
    // Enter 必须走 focusOrder（已确认在当前列表里的那条），不能走 rows[某个下标]
    expect(src, 'Enter 应作用于 focusOrder，而不是 rows[focusIdx] 之类的下标取值').toMatch(
      /e\.key === "Enter"[\s\S]{0,80}focusOrder\b/,
    );
  });
});
