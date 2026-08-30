import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

// 导出的 CSV 不能让 Excel 把单元格当公式执行。
//
// 加引号只解决 CSV 的正确性，解决不了**打开它的那个程序**：
// Excel/WPS 解析 CSV 时先剥掉引号，然后把以 = + @ 开头的格子当公式跑。
// 而这套系统的导出就是给财务用 Excel 打开的，客户名、备注、地址
// 都来自用户输入——其中"自助下单"那条路还是对公网开放的。
//
// 实测（修之前，后端那条）：订单目的地填 `=1+1`，导出的 CSV 里原样是 `=1+1`。
//
// 前后端两条导出路径都要按同一套规则处理，所以这条用例把规则本身抄一份来判，
// 并且顺带确认源码里那段判断还在（改没了这条用例会红）。

// 与 DataTable.tsx 的 deFormula、以及后端 sanitizeCell 同一套规则
const deFormula = (s: string) =>
  /^[=+@\t\r]/.test(s) || (s.startsWith("-") && !Number.isFinite(Number(s))) ? "'" + s : s;

describe("导出的 CSV 不能被 Excel 当公式执行", () => {
  it("公式前缀要被中和，负数金额不能被误伤", () => {
    const cases: [string, string, string][] = [
      ["=1+1", "'=1+1", "等号开头是公式"],
      ['=HYPERLINK("http://x","点我")', '\'=HYPERLINK("http://x","点我")', "常见的钓鱼写法"],
      ["+86 13800138000", "'+86 13800138000", "加号开头——电话号码就长这样"],
      ["@sum(A1:A9)", "'@sum(A1:A9)", "@ 是 Lotus 风格的公式前缀，Excel 也认"],
      ["\tfoo", "'\tfoo", "制表符开头会被当成公式的前导空白"],
      ["-1200.50", "-1200.50", "负数金额必须原样留着，加了引号那一列就求不了和"],
      ["-1e3", "-1e3", "科学计数法也是数字"],
      ["-cmd|'/c calc'!A1", "'-cmd|'/c calc'!A1", "减号开头但不是数字，照样是注入"],
      ["上海,某某公司", "上海,某某公司", "逗号由 CSV 引号处理，不该在这里动"],
      ["", "", "空串不动"],
      ["演示·甲承运", "演示·甲承运", "中文不动"],
    ];
    for (const [input, want, why] of cases) {
      expect(deFormula(input), `${why}：deFormula(${JSON.stringify(input)})`).toBe(want);
    }
  });

  it("DataTable 的 CSV 导出确实用了这套规则", () => {
    const src = readFileSync(resolve(__dirname, "../components/DataTable.tsx"), "utf8");
    // 规则被删掉或改名时这条会红——上面那份是抄的，抄的东西不会自己失效
    expect(src, "exportCsv 里没有中和公式前缀的处理").toMatch(/deFormula/);
    expect(src, "esc 没有走 deFormula，加引号挡不住 Excel").toMatch(/deFormula\(String\(v \?\? ""\)\)/);
  });

  it("Excel(SpreadsheetML) 那条不需要处理，但也不能改成写公式", () => {
    const src = readFileSync(resolve(__dirname, "../components/DataTable.tsx"), "utf8");
    // String 类型的格子 Excel 当文本；只有 ss:Formula 才会被求值。
    expect(src).toMatch(/ss:Type="String"/);
    expect(src, "出现了 ss:Formula —— 那会让导出的内容被当公式执行").not.toMatch(/ss:Formula/);
  });
});
