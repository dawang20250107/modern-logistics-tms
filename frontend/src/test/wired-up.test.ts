import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it } from "vitest";

// 声明了却没接到界面上的 useQuery / useMutation。
//
// 这条是三个真实缺口逼出来的：承运合同的 genContract / sendContract /
// confirmContract，以及司机报销的 submitReimb / reimbAction——五个 mutation
// 连同表单状态都写好了，后端路由也全的，但没有任何地方把它们渲染出来。
// 于是「承运合同」和「报销」两整段功能从界面上完全够不着：
// 工作流面板上那两格一直挂着，而没有任何东西能改变它们。
//
// 这种缺口特别难发现：不报错、不崩、类型检查也过（声明本身是合法的），
// 页面看起来正常——只是少了一块，而少的那块没人知道它本该在。
// 逐页点按钮也发现不了，因为那颗按钮压根不存在。
//
// 判据很朴素：一个 useMutation 的返回值如果在整个文件里只出现过一次
// （就是它自己那行声明），它一定没被用上。

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

// 注释要剥掉：不然「注释里提了一嘴这个名字」会被算成用上了。
// 这个坑在 empty-state.test.ts 上踩过一次。
const strip = (s: string) =>
  s
    .replace(/\{\s*\/\*[\s\S]*?\*\/\s*\}/g, "")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/^\s*\/\/.*$/gm, "");

describe("接线", () => {
  it("没有声明了却从未被引用的 useMutation / useQuery", () => {
    const dead: string[] = [];
    for (const f of tsxFiles(SRC)) {
      const src = strip(readFileSync(f, "utf8"));
      for (const m of src.matchAll(/const (\w+) = use(?:Mutation|Query)\(/g)) {
        const name = m[1];
        const uses = src.match(new RegExp(`\\b${name}\\b`, "g"))?.length ?? 0;
        if (uses <= 1) dead.push(`${f.replace(SRC, "")} → ${name}`);
      }
    }
    expect(
      dead,
      "这些请求声明了但没接到界面上——功能在后端是全的，用户却够不着：\n  " +
        dead.join("\n  "),
    ).toEqual([]);
  });

  it("检查本身抓得住（拿一段假源码回测）", () => {
    // 没做过回测的检查不能信：绿的可能只是因为它什么都抓不住。
    const fake = `
      const used = useMutation({ mutationFn: () => x() });
      const dead = useMutation({ mutationFn: () => y() });
      return <button onClick={() => used.mutate()}>go</button>;
    `;
    const found: string[] = [];
    for (const m of strip(fake).matchAll(/const (\w+) = use(?:Mutation|Query)\(/g)) {
      const uses = strip(fake).match(new RegExp(`\\b${m[1]}\\b`, "g"))?.length ?? 0;
      if (uses <= 1) found.push(m[1]);
    }
    expect(found).toEqual(["dead"]);
  });
});
