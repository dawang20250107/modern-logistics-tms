import { describe, expect, it } from "vitest";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// 设计语汇守卫。
//
// 存在的理由：整套「仪器」语汇（边到边窗格、直角、无阴影、无渐变、单强调色、
// 字号下限、颜色预算）是靠一层层去掉装饰换来的。这种东西**回归起来毫无声响**——
// 下一轮有人加一张"好看的"卡片，12px 圆角 + 柔阴影 + 蓝青渐变，
// tsc 不报、vitest 不报、人也看不出这一张和别的哪里不一样，
// 半年后整站又飘回消费级 dashboard。
//
// 浏览器级的密度/对比度/颜色预算量在 scripts/dev/ui-audit.mjs 里，
// 但它需要跑起完整环境**且库里有数据**——种子数据随 Django 一起丢了
// （见 backend-go/PORTING.md 的遗留清单），空库量密度没有意义，
// 所以那把尺子暂时只能本地手跑。这里守的是不需要浏览器就能查的那部分。

const SRC = join(dirname(fileURLToPath(import.meta.url)), "..");
const CSS = readFileSync(join(SRC, "styles.css"), "utf8");

// ── 按「规则」解析，不按行 ──
// 第一版是逐行扫的，于是多行规则里的属性行归不到它的选择器上：
// `.drv-login-badge { \n width: 52px; ... border-radius: 12px \n }`
// 的圆角声明在第二行，行首不是 `.drv-`，豁免规则就漏判了。
interface Rule { sel: string; body: string; line: number }
function parseRules(css: string): Rule[] {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, " "));
  const rules: Rule[] = [];
  let i = 0;
  const lineAt = (pos: number) => stripped.slice(0, pos).split("\n").length;
  while (i < stripped.length) {
    const open = stripped.indexOf("{", i);
    if (open === -1) break;
    const sel = stripped.slice(i, open).trim();
    // @media / @supports：跳过包裹层，里面的规则会被下一轮循环单独收到
    if (sel.startsWith("@")) { i = open + 1; continue; }
    let depth = 1, j = open + 1;
    while (j < stripped.length && depth > 0) {
      if (stripped[j] === "{") depth++;
      else if (stripped[j] === "}") depth--;
      j++;
    }
    // @media 的收尾 } 会粘在下一条选择器前面，剥掉再存
    rules.push({ sel: sel.replace(/^\}+\s*/, ""), body: stripped.slice(open + 1, j - 1), line: lineAt(open) });
    i = j;
  }
  return rules.filter((r) => r.sel && !r.sel.includes("{"));
}
const RULES = parseRules(CSS);

// 司机端与公开页（登录/注册/找回/下单/查询）是**故意**不跟桌面端紧凑度的：
// 司机站在货台边、戴手套、车在震、可能大太阳底下，密度让位给抗干扰
// （16px 正文、48px 触控目标、大圆角）；公开页是一天看一次的门面。
const FIELD_USE = /(^|[\s,>~+])\.(drv|driver|public|auth|wechat|login|center-screen|bd-)/;
const isFieldUse = (sel: string) => FIELD_USE.test(sel);

// token 定义块：唯一允许出现裸色值的地方
const TOKEN_SEL = /^:root/;

describe("设计语汇 · 结构面", () => {
  it("圆角不超过 8px：结构面直角，可点元素 3–4px", () => {
    const hits: string[] = [];
    for (const r of RULES) {
      if (isFieldUse(r.sel) || TOKEN_SEL.test(r.sel)) continue;
      for (const m of r.body.matchAll(/border-radius:\s*([^;}]+)/g)) {
        if (/999px|50%|var\(--radius/.test(m[1])) continue; // 药丸/圆点是形状；token 阶梯已受控
        const nums = [...m[1].matchAll(/(\d+(?:\.\d+)?)px/g)].map((x) => Number(x[1]));
        if (nums.some((v) => v > 8)) hits.push(`${r.sel} (L${r.line}) → ${m[1].trim()}`);
      }
    }
    expect(hits, "圆角 >8px 是消费级语言，且在密集表格里会切掉角上的字。改走 --radius* 阶梯").toEqual([]);
  });

  it("不引入新的渐变：渐变把装饰和状态混进同一个视觉通道", () => {
    // 允许的例外：这些渐变承载信息（进度/加载中/可拖拽），不是装饰
    const ALLOW = /skeleton|dt-resizer|^select$|ov-bar|ct-bar|progress|scrollbar|^input|^textarea|gp-bar|dt-loadbar/;
    const hits: string[] = [];
    for (const r of RULES) {
      if (isFieldUse(r.sel) || ALLOW.test(r.sel)) continue;
      if (/linear-gradient|radial-gradient|conic-gradient/.test(r.body)) {
        hits.push(`${r.sel} (L${r.line})`);
      }
    }
    expect(hits, "渐变属装饰。语义靠色点/边框/字重，不靠渐变；例外请加进本用例的 ALLOW").toEqual([]);
  });

  it("hover 不做位移：一天点几百次，光标下的目标不该会动", () => {
    const hits: string[] = [];
    for (const r of RULES) {
      if (!/:hover/.test(r.sel)) continue;
      if (/transform:\s*translate/.test(r.body)) hits.push(`${r.sel} (L${r.line})`);
    }
    expect(hits, "hover 位移在高频界面上是负担（目标会跑）。状态变化只走底色与边框").toEqual([]);
  });

  it("不用毛玻璃：半透明 + blur 在密集账本上既伤可读性又伤滚动", () => {
    const hits: string[] = [];
    for (const r of RULES) {
      for (const m of r.body.matchAll(/backdrop-filter:\s*([^;}]+)/g)) {
        if (/var\(--glass-blur\)|none/.test(m[1])) continue; // --glass-blur 已收敛为 none
        hits.push(`${r.sel} (L${r.line}) → ${m[1].trim()}`);
      }
    }
    expect(hits, "毛玻璃已从语汇中移除。需要层级请用实底 + 1px 分隔线").toEqual([]);
  });
});

describe("设计语汇 · 颜色", () => {
  it("token 定义之外不写死 hex 颜色", () => {
    // 深色门面上的前景允许写死：它不随主题变——深底上的字永远是浅色。
    const ALLOW = /on-dark|hero|side-|cmdk-overlay|modal-overlay|wb-overlay|brand-mark|topbar-avatar|profile-avatar|\bkbd\b/i;
    const hits: string[] = [];
    for (const r of RULES) {
      if (TOKEN_SEL.test(r.sel) || isFieldUse(r.sel) || ALLOW.test(r.sel)) continue;
      for (const m of r.body.matchAll(/#[0-9a-fA-F]{3,8}\b/g)) {
        if (/^#f{3,8}$/i.test(m[0])) continue;   // 深底上的白字
        if (/^#0{3,8}$/i.test(m[0])) continue;   // color-mix 压暗时的黑锚点，不是主题色
        hits.push(`${r.sel} (L${r.line}) → ${m[0]}`);
      }
    }
    expect(hits, "色值一律走 token：写死的 hex 不会跟主题切换，暗色下就会露馅").toEqual([]);
  });

  it("语义色板固定为 红/琥珀/绿/蓝/紫，不新增第六种色相", () => {
    // 新增色相会稀释「颜色即信号」：多一种色，每一种就少一分注意力
    const extra = [...CSS.matchAll(/^\s*--(pink|orange|teal|lime|indigo|fuchsia|magenta|brown|olive|cyan)[-a-z0-9]*\s*:/gm)];
    expect(extra.map((m) => m[1]), "语义色板固定为 红/琥珀/绿/蓝/紫；新增色相会稀释「颜色即信号」").toEqual([]);
  });
});

describe("设计语汇 · 密度与可读性", () => {
  // 字号下限 12px。11px 在 24" 1080p @60cm 只有 critical print size 的 77%，
  // 已进入阅读速度陡降区。密度从壳子（间距/圆角/hero）里抠，不从字上抠。
  //
  // 例外只给两类，每一条都写明理由：
  //   纯符号 —— 它不是"读"的，是"看形状"的；
  //   1–3 字符的计数/角标 —— 有形状与位置双重冗余，且外框限死了尺寸。
  const SMALL_OK: Record<string, string> = {
    ".doc-order::before": "▸ 订单前缀符号，非文字",
    ".doc-waybill::before": "▪ 运单前缀符号，非文字",
    ".dt-sortic": "排序箭头符号",
    ".dt-sortord": "多列排序的序号上标，1 字符",
    ".dt-pinbtn": "固定列图钉符号",
    ".recon-badge": "待办计数，1–2 字符，外框 15px 限死",
    ".top-rank": "Top N 排名数字，1–2 字符，外框 22px 限死",
    ".profile-avatar .profile-avatar-edit": "头像 hover 时才出现的浮层微标，非常驻",
    ".topbar-avatar": "用户名首字，1 字符，外框 24px 限死",
    ".lvl": "客户等级 S/A/B/C/D，1 字符，外框 16px 限死",
  };

  it("字号不低于 12px（纯符号与短计数除外，见 SMALL_OK）", () => {
    const hits: string[] = [];
    for (const r of RULES) {
      if (isFieldUse(r.sel) || TOKEN_SEL.test(r.sel)) continue;
      if (SMALL_OK[r.sel]) continue;
      for (const m of r.body.matchAll(/font-size:\s*(\d+(?:\.\d+)?)px/g)) {
        if (Number(m[1]) < 12) hits.push(`${r.sel} (L${r.line}) → ${m[1]}px`);
      }
    }
    expect(hits, "字号 <12px 不可用于长时间阅读。要么提到 12px，要么进 SMALL_OK 并写明它不是文字").toEqual([]);
  });

  it("SMALL_OK 里的例外都还在用（名单不留过期项）", () => {
    const dead = Object.keys(SMALL_OK).filter((sel) => !RULES.some((r) => r.sel === sel));
    expect(dead, "这些选择器已不存在，从 SMALL_OK 里删掉——豁免名单过期就等于悄悄放宽了规则").toEqual([]);
  });

  it("三档密度都定义了行高与字号，且行高 ≥ 2.2 × 字号", () => {
    for (const tier of ["compact", "standard", "relaxed"]) {
      const rowM = CSS.match(new RegExp(`--row-h-${tier}:\\s*(\\d+)px`));
      const cellRule = RULES.find((r) => r.sel.includes(`.dt-den-${tier}`) && /\.dt-table td/.test(r.sel));
      const fontM = cellRule?.body.match(/font-size:\s*(\d+(?:\.\d+)?)px/);
      expect(rowM, `.dt-den-${tier} 缺行高 token --row-h-${tier}`).toBeTruthy();
      expect(fontM, `.dt-den-${tier} 的单元格缺 font-size——三档必须同时缩放行高与字号`).toBeTruthy();
      const row = Number(rowM![1]), font = Number(fontM![1]);
      expect(row / font, `${tier} 档行高/字号 = ${row}/${font}，低于 2.2 会挤`).toBeGreaterThanOrEqual(2.2);
    }
  });
});

// ── tsx 层：别把刚拆掉的装饰用内联 style 加回来 ──
function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (/\.tsx$/.test(e.name) && !/\.test\.tsx$/.test(e.name)) out.push(p);
  }
  return out;
}
const FIELD_USE_FILE = /Driver|Public|Auth|Login|Register|Forgot|Tracking|CustomerOrder/;
const TSX = walk(SRC).filter((f) => !FIELD_USE_FILE.test(f));
const rel = (f: string) => f.slice(SRC.length + 1);

describe("设计语汇 · 内联样式", () => {
  it("内联 style 里不写死 hex 颜色（引用 var(--*) 的才跟主题）", () => {
    const hits: string[] = [];
    for (const f of TSX) {
      readFileSync(f, "utf8").split("\n").forEach((ln, i) => {
        if (!/style=\{/.test(ln)) return;
        for (const m of ln.matchAll(/#[0-9a-fA-F]{3,8}\b/g)) {
          if (/^#f{3,8}$/i.test(m[0])) continue; // 深底上的白字
          hits.push(`${rel(f)}:${i + 1} → ${m[0]}`);
        }
      });
    }
    expect(hits, "内联样式请用 var(--*)：写死的 hex 不跟主题切换，暗色下会露馅").toEqual([]);
  });

  it("内联 style 里不加圆角 >8px、不加自定义阴影", () => {
    const hits: string[] = [];
    for (const f of TSX) {
      readFileSync(f, "utf8").split("\n").forEach((ln, i) => {
        for (const m of ln.matchAll(/borderRadius:\s*(\d+)/g)) {
          if (Number(m[1]) > 8 && Number(m[1]) !== 999) hits.push(`${rel(f)}:${i + 1} → borderRadius ${m[1]}`);
        }
        // 阴影一律走 --shadow-*：内联手写的那些在暗色下都是错的
        for (const m of ln.matchAll(/boxShadow:\s*"(?!var\(--|inset 0 0 0 1px var|none)([^"]{4,})"/g)) {
          hits.push(`${rel(f)}:${i + 1} → boxShadow ${m[1].slice(0, 40)}`);
        }
      });
    }
    expect(hits, "圆角走 --radius*，阴影走 --shadow-*；结构面本来就不该有阴影").toEqual([]);
  });
});
