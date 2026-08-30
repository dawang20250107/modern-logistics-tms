// 分页口径走查：找出「把一页当全量」的地方。
//
//   node scripts/dev/paging-audit.mjs
//
// 由来：调度工作台、登录审计、待核销队列，三处都犯了同一个错——
// 把分页接口取回来的那一页的 items.length 当成总数显示给用户。
// 演示数据下一页装得下全部，数出来正好是对的，所以三处都活了很久；
// 真实数据量下「待派 20」其实是 8336、「失败 80」其实是 402。
//
// 这三处是人一个一个看出来的。这个脚本把那次人工走查固化下来：
// 变量若来自 `.data?.items`（分页响应），它的 .length 就只是当前页的条数，
// 不该被当成总数渲染进界面——总数在同一个响应的 `total` 字段里。
//
// 只报「渲染进 JSX 的」。同一个 .length 用来判空（=== 0）、算下标、
// 做循环边界都是正当的，报出来只会淹没真问题。
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

// 可传目录，便于对着历史版本自检：
//   git worktree add /tmp/old <旧提交> && node scripts/dev/paging-audit.mjs /tmp/old/frontend/src
const ROOT = process.argv[2] ?? new URL("../../frontend/src", import.meta.url).pathname;

function walk(dir) {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    return statSync(p).isDirectory() ? walk(p) : p.endsWith(".tsx") ? [p] : [];
  });
}

// 变量名 ← 取自分页响应的 items
const FROM_ITEMS = /(?:const|let)\s+([A-Za-z_$][\w$]*)\s*=\s*[^;\n]*\.data\?\?\?\.items|(?:const|let)\s+([A-Za-z_$][\w$]*)\s*=\s*[^;\n]*\.data\?\.items/g;
// 分页变量的 .length。
//
// 口径特意放宽到"任何用到"，而不只是"直接渲染进 JSX"：
// 第一版只找 `{xxx.length}` 这种直接渲染，结果**漏掉了最严重的那一处**——
// 调度工作台是先把 .length 塞进一个中间对象（`poolCounts = { unassigned: freeOrders.length }`），
// 再渲染 `{poolCounts.unassigned}`，按行匹配跟不过去。
// 拿修复前的代码回测：窄口径只报出 3 处中的 2 处，宽口径 4 处全中且零误报。
//
// 后面紧跟比较/算术运算符的不算——判空、算下标、做边界都是正当用法。
const USED = (name) =>
  new RegExp(
    // 空白要写在前瞻**里面**：写成 `\\.length\\s*(?!…)` 的话，
    // \\s* 会回退成空串，前瞻就落在空格上、永远成立，等于没排除。
    `\\b${name}\\s*(?:\\.filter\\([^)]*\\))?\\.length\\b(?!\\s*[=!<>+\\-*/?&|.])`,
    "g",
  );

// 这些字样说明界面已经讲明白"这只是一页"，是有意为之
const DELIBERATE = /最近|本页|当前页|已选|下表|\?\?/;

let findings = 0;
let scanned = 0;    // 扫过的 .tsx 个数
let pagedVars = 0;  // 认出来的分页变量个数——正则还活着的证据
for (const file of walk(ROOT)) {
  scanned++;
  const src = readFileSync(file, "utf8");
  const paged = new Set();
  for (const m of src.matchAll(FROM_ITEMS)) paged.add(m[1] ?? m[2]);
  pagedVars += paged.size;
  if (paged.size === 0) continue;

  const lines = src.split("\n");
  for (const name of paged) {
    const re = USED(name);
    lines.forEach((line, i) => {
      const t = line.trim();
      // 注释里提到 .length 不算——这份文件自己的说明、以及被修复处留下的
      // 「原先是 rows.length」这类注释都会被误报。JSX 注释是 `{/*` 开头，
      // 别漏了那个花括号。
      if (/^(\/\/|\*|\/\*|\{\/\*)/.test(t)) return;
      re.lastIndex = 0;
      const m = re.exec(line);
      if (!m) return;
      if (DELIBERATE.test(line)) return;
      // 作为比较式右操作数的不算：`total > rows.length` 是在判断"是否被截断"，
      // 正是修好之后该有的写法，不该反过来被报。
      // 前瞻只挡得住 .length **后面**跟运算符的情况，挡不住它在运算符右边。
      if (/[=!<>]=?\s*$/.test(line.slice(0, m.index))) return;
      findings++;
      const rel = file.slice(ROOT.length + 1);
      console.log(`  ✗ ${rel}:${i + 1}`);
      console.log(`      ${t.slice(0, 120)}`);
      console.log(`      \`${name}\` 来自分页响应的 items，.length 只是当前页条数。`);
      console.log(`      总数取同一响应的 total；跨页统计向服务端要（page_size=1 读 total）。`);
    });
  }
}

// 防空转。这个脚本"没发现"和"没扫到"打印的是同一句话，而它靠两条正则活着：
// 目录扫空（传错路径）、`.tsx` 改了后缀、或者取分页数据的写法从
// `.data?.items` 换成别的 hook —— 任何一条断了，它都会一直报绿。
// 实测：拿一个空目录跑，它输出「✓ 没有把当前页条数当总数渲染的地方」、退出 0。
// 一条永远绿的检查比没有这条检查更坏：它会让人以为这一类问题被盯着。
// 退出码 2 = 检查自己空转了，结论不作数（对齐 route-match / env-match）。
if (scanned === 0 || pagedVars === 0) {
  console.error(
    `\n✗ 这条检查正在空转：扫了 ${scanned} 个 .tsx，识别出 ${pagedVars} 个分页变量。\n` +
    `  两条正则（取 items 的赋值、.length 的用法）之一已经失效，或者路径不对。\n` +
    `  目录：${ROOT}\n` +
    `  这不是「没发现问题」，是「没在检查」——请先修脚本再看结论。`,
  );
  process.exit(2);
}

if (findings === 0) {
  console.log(`✓ 没有把「当前页条数」当总数渲染的地方（扫 ${scanned} 个 .tsx，${pagedVars} 个分页变量）`);
  process.exit(0);
}
console.log(`\n共 ${findings} 处。若确属有意（界面已写明"最近 N 条"之类），在同一行注明即可。`);
process.exit(1);
