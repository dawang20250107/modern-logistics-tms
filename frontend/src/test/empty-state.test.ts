import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// 空系统上的文案。
//
// 新客户上线第一天，系统里一条数据都没有。那时候界面说什么，
// 是这套产品给人的第一印象——也最容易说错话。
//
// 已经栽过两次，都是同一条原则的不同表现：
//   · 准班率/在线率显示 0.0%，其实是没有样本。「这段时间没有可统计的单」
//     和「准班率是零」是相反的两件事。
//   · 对账页在一条单据都没有的时候说「现金流向好」「账期健康」——
//     那是在没有依据的情况下下体检结论。
//
// 这组用例读源码来钉，是因为这些是**渲染分支**，不是纯函数：
// 要在组件里测就得把整页的数据依赖都搭出来，成本远大于收益，
// 而这里真正要防的是「有人顺手把无条件的结论写回去」。
// vitest 的工作目录是 frontend/，从这里定位源码最直白
const readRaw = (rel: string) => readFileSync(resolve(process.cwd(), "src", rel), "utf8");

// 扫源码之前必须把注释剥掉。
//
// 第一版没剥，结果被**我自己写的注释**骗了：那段注释里引用了「现金流向好」
// 用来解释为什么要挡它，于是用例找到的第一处是注释行，
// 判定"这句结论没有被挡住"——代码明明是对的，用例却红了。
// 反过来同样成立：注释里提一句就能让一条本该红的用例变绿。
// 源码扫描类的检查一律先剥注释，否则判的是"文件里提到过吗"而不是"代码里是怎么写的"。
const read = (rel: string) =>
  readRaw(rel)
    .replace(/\{\s*\/\*[\s\S]*?\*\/\s*\}/g, "") // JSX 注释 {/* … */}
    .replace(/\/\*[\s\S]*?\*\//g, "")               // 块注释
    .replace(/^\s*\/\/.*$/gm, "");                    // 行注释

describe("空系统的文案", () => {
  it("对账总览：没有单据时不下结论", () => {
    const src = read("pages/ReconciliationPage.tsx");
    // 剥完注释后这两句仍在 = 它们确实是代码，不是我在注释里提了一嘴
    // 前置：这两句结论必须还在（它们在有数据时是对的），否则这条用例形同虚设
    expect(src).toContain("现金流向好");
    expect(src).toContain("账期健康");
    // 而它们必须被"有没有单据"挡住
    expect(src).toContain("hasAnyStatement");
    for (const claim of ["现金流向好", "账期健康"]) {
      const line = src.split("\n").find((l) => l.includes(claim))!;
      const block = src.slice(Math.max(0, src.indexOf(line) - 260), src.indexOf(line) + line.length);
      expect(
        block.includes("hasAnyStatement"),
        `「${claim}」这句结论没有被 hasAnyStatement 挡住——` +
          `一条单据都没有的系统不该被判定为健康`,
      ).toBe(true);
    }
  });

  it("驾驶舱：period 已自带「近」字，不能再拼一个", () => {
    const src = read("components/BusinessMetrics.tsx");
    // 后端返回的是「近 N 天」。前面再写一个「近 」就会渲染成「近 近 30 天」，
    // 而这行字就在驾驶舱首屏。
    expect(
      /近\s*\{[^}]*period/.test(src),
      "又出现了「近 {period}」的拼法：period 自带「近」字，会渲染成「近 近 30 天」",
    ).toBe(false);
    // 前置：确实还在用 period，否则上面那条恒真
    expect(src).toContain("period");
  });
});
