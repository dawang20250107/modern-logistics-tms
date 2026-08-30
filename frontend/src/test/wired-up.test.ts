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

  // ── 表单字段：状态里有、界面上碰不到 ──
  //
  // 上面那条抓的是"整个 mutation 没接线"。这条抓的是更细的一层：
  // 表单状态里有这个字段、提交时也带上了，但**没有任何输入框绑到它**。
  //
  // 由来是计价规则的 tier_prices：表单默认的计费方式就是「按重量阶梯」，
  // 而页面上没有一处能填那几档价。存下来 tier_prices 是 []，算价时单价取 0，
  // 用户认真配了一条合同价，报出来是零——没有报错，只是数字不对。
  // 同一条检查还揪出另外两处：计价规则的费用科目（写死成词表里根本没有的
  // "FREIGHT"）和体积折算系数（一单一议的商务条件，全站固定 0.33），
  // 以及录单表单的 source（连每个渠道的占位符文案都写好了，就是没有输入框）。
  //
  // 判据：表单状态对象的每一个键，都必须在 JSX 里以 `<状态名>.<键>` 出现过。
  // 只在类型声明、初始值、提交 payload 里出现，说明用户碰不到它。
  const keysOf = (lit: string): string[] => {
    const keys: string[] = [];
    let depth = 0;
    for (let i = 0; i < lit.length; i++) {
      const c = lit[i];
      if ("{[(".includes(c)) depth++;
      else if ("}])".includes(c)) depth--;
      else if (depth === 1) {
        const m = /^([A-Za-z_$][\w$]*)\s*:/.exec(lit.slice(i));
        if (m && (i === 0 || /[\s,{]/.test(lit[i - 1]))) {
          keys.push(m[1]);
          i += m[0].length - 1;
        }
      }
    }
    return keys;
  };
  const literalAt = (src: string, start: number): string => {
    let d = 0;
    for (let i = start; i < src.length; i++) {
      if (src[i] === "{") d++;
      else if (src[i] === "}") {
        d--;
        if (d === 0) return src.slice(start, i + 1);
      }
    }
    return "";
  };
  const unreachableFields = (src: string): string[] => {
    const out: string[] = [];
    const jsxAt = src.indexOf("return (");
    if (jsxAt < 0) return out;
    const jsx = src.slice(jsxAt);
    for (const m of src.matchAll(/const \[(\w+), set\w+\] = useState<[^>]*>\(\s*([A-Z_][\w$]*|\{)/g)) {
      const state = m[1];
      let lit = "";
      if (m[2] === "{") lit = literalAt(src, m.index! + m[0].length - 1);
      else {
        const dm =
          new RegExp(`const ${m[2]}\\s*:[^=]*=\\s*\\{`).exec(src) ??
          new RegExp(`const ${m[2]}\\s*=\\s*\\{`).exec(src);
        if (dm) lit = literalAt(src, dm.index + dm[0].length - 1);
      }
      if (!lit) continue;
      const keys = keysOf(lit);
      // 三个键以下的多半不是表单（是个小状态包），不掺和
      if (keys.length < 3) continue;
      for (const k of keys) {
        if (!new RegExp(`\\b${state}\\.${k}\\b`).test(jsx)) out.push(`${state}.${k}`);
      }
    }
    return out;
  };

  it("没有表单字段是界面上碰不到的", () => {
    const dead: string[] = [];
    for (const f of tsxFiles(SRC)) {
      for (const k of unreachableFields(strip(readFileSync(f, "utf8")))) {
        dead.push(`${f.replace(SRC, "")} → ${k}`);
      }
    }
    expect(
      dead,
      "这些字段在表单状态里、提交时也带上了，但没有任何输入框绑到它——\n" +
        "用户改不了，它永远是那个初始值，而后端拿它当真：\n  " +
        dead.join("\n  "),
    ).toEqual([]);
  });

  it("表单字段这条检查抓得住（拿修复前的形状回测）", () => {
    // 修复前的 PricingPage 就是这个形状：tier_prices 在 EMPTY 里、在 payload 里，
    // JSX 里只有列表列上的 r.tier_prices（不是 form.tier_prices）。
    const before = `
      const EMPTY: RuleForm = { name: "", base_price: "0", tier_prices: [], priority: "0" };
      export function P() {
        const [form, setForm] = useState<RuleForm>(EMPTY);
        const payload = () => ({ name: form.name, base_price: form.base_price, tier_prices: form.tier_prices });
        return (
          <div>
            <input value={form.name} />
            <input value={form.base_price} />
            <input value={form.priority} />
            <td>{r.tier_prices.length} 级</td>
          </div>
        );
      }
    `;
    expect(unreachableFields(strip(before))).toEqual(["form.tier_prices"]);

    // 修好之后（表单里真的绑了 form.tier_prices）就不该再报
    const after = before.replace("<td>{r.tier_prices.length} 级</td>", "<td>{form.tier_prices.length} 级</td>");
    expect(unreachableFields(strip(after))).toEqual([]);
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
