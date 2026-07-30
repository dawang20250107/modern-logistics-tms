import { describe, expect, it } from "vitest";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { EMPTY, fmtDateShort, fmtDateTime, fmtMoney, fmtMoney0, fmtNum, fmtNum0, fmtRelative, fmtWan, orEmpty } from "./format";

// 展示格式化的行为契约 + 全站一致性守卫。
//
// 这里守的是两类事，都是靠肉眼审查抓不住的：
//   1. 空 ≠ 零。fmtMoney(null) 曾返回 "¥0.00"，于是「应付未知」和「应付零元」
//      长得一模一样——在对账页面上，这两者差的是钱。
//   2. 同一种数据全站只有一种长相。空值占位符曾同时存在 `—`(77 处) 和 `-`(39 处)；
//      时间有 9 处绕过 fmtDateTime 直接 toLocaleString()，那会按浏览器时区渲染，
//      运营在境外或服务器 UTC 时看到的是错的时刻。

describe("格式化 · 空与零的区分", () => {
  it("金额：没有值给占位符，真实的零给 ¥0.00", () => {
    expect(fmtMoney(null)).toBe(EMPTY);
    expect(fmtMoney(undefined)).toBe(EMPTY);
    expect(fmtMoney("")).toBe(EMPTY);
    expect(fmtMoney("abc")).toBe(EMPTY);
    expect(fmtMoney(NaN)).toBe(EMPTY);
    expect(fmtMoney(0)).toBe("¥0.00");
    expect(fmtMoney("0")).toBe("¥0.00");
  });

  it("金额：千分位 + 恒两位小数", () => {
    expect(fmtMoney(1234567.5)).toBe("¥1,234,567.50");
    expect(fmtMoney("17620")).toBe("¥17,620.00");
    expect(fmtMoney(-500)).toBe("¥-500.00");
  });

  it("fmtMoney0 用于合计/余额：没有值也显示 ¥0.00", () => {
    // 合计位置显示破折号会像是漏算了，那里要的是「算出来是零」
    expect(fmtMoney0(null)).toBe("¥0.00");
    expect(fmtMoney0(0)).toBe("¥0.00");
    expect(fmtMoney0(88)).toBe("¥88.00");
  });

  it("数量同样区分空与零", () => {
    expect(fmtNum(null)).toBe(EMPTY);
    expect(fmtNum(0)).toBe("0");
    expect(fmtNum(12345)).toBe("12,345");
    expect(fmtNum(12.345, 2)).toBe("12.35");
    expect(fmtNum0(null)).toBe("0");
  });

  it("文本空值统一走 EMPTY，空白串也算空", () => {
    expect(orEmpty(null)).toBe(EMPTY);
    expect(orEmpty("")).toBe(EMPTY);
    expect(orEmpty("   ")).toBe(EMPTY);
    expect(orEmpty("美的集团")).toBe("美的集团");
  });

  it("大额紧凑显示", () => {
    expect(fmtWan(null)).toBe(EMPTY);
    expect(fmtWan(129413.33)).toBe("¥12.9万");
    expect(fmtWan(250000000)).toBe("¥2.50亿");
    expect(fmtWan(880)).toBe("¥880.00");
  });
});

describe("格式化 · 时间", () => {
  it("固定 Asia/Shanghai，不跟浏览器时区跑", () => {
    // UTC 2026-07-30T00:30 → 北京时间当天 08:30
    const s = fmtDateTime("2026-07-30T00:30:00Z");
    expect(s).toContain("08:30");
    expect(s).toContain("2026");
  });

  it("空值与坏值都给占位符，不给 Invalid Date", () => {
    expect(fmtDateTime(null)).toBe(EMPTY);
    expect(fmtDateTime("")).toBe(EMPTY);
    expect(fmtDateTime("not-a-date")).toBe(EMPTY);
  });

  it("相对时间：未来时刻不伪装成刚刚发生", () => {
    // 服务端时钟略微超前时，原实现会算出「0分钟前」甚至负数分钟
    const future = new Date(Date.now() + 10 * 60000).toISOString();
    expect(fmtRelative(future)).toBe("稍后");
    // 时钟微小漂移（<1分钟）仍按「刚刚」处理，不必大惊小怪
    expect(fmtRelative(new Date(Date.now() + 5_000).toISOString())).toBe("刚刚");
    expect(fmtRelative(new Date(Date.now() - 3 * 60000).toISOString())).toBe("3分钟前");
    expect(fmtRelative(new Date(Date.now() - 5 * 3600_000).toISOString())).toBe("5小时前");
  });

  it("紧凑绝对时间：本年省年份、跨年补年份，一律 Asia/Shanghai", () => {
    // 2026-07-19T06:32Z = 北京时间 14:32
    expect(fmtDateShort("2026-07-19T06:32:00Z")).toBe("07-19 14:32");
    // 跨年的单必须带年份，否则「12-31」是哪一年的 12-31 说不清
    const otherYear = new Date().getUTCFullYear() === 2025 ? "2024" : "2025";
    expect(fmtDateShort(`${otherYear}-12-31T01:00:00Z`)).toMatch(/^\d\d-12-31 09:00$/);
    expect(fmtDateShort(null)).toBe(EMPTY);
    expect(fmtDateShort("not-a-date")).toBe(EMPTY);
  });
});

// ── 全站一致性：把「同一种数据只有一种长相」钉住 ──
// 从本文件位置推导 src 根：依赖 process.cwd() 的话，从别的目录启动 vitest 就会扫错地方
const SRC = join(dirname(fileURLToPath(import.meta.url)), "..");
function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) out.push(p);
  }
  return out;
}
const files = walk(SRC).filter((f) => !f.endsWith(join("api", "format.ts")));
const read = (f: string) => readFileSync(f, "utf8");
const rel = (f: string) => f.slice(SRC.length + 1);

describe("格式化 · 全站一致性", () => {
  it("没有裸的半角占位符（一律用 EMPTY）", () => {
    const hits: string[] = [];
    for (const f of files) {
      read(f).split("\n").forEach((ln, i) => {
        // 空值兜底的三种写法：|| "-"、?? "-"、? x : "-"
        if (/(\|\||\?\?)\s*"-"/.test(ln) || /\?[^?:]{1,60}:\s*"-"[\s,)}\]]/.test(ln)) {
          hits.push(`${rel(f)}:${i + 1}`);
        }
      });
    }
    expect(hits, "空值占位符请用 EMPTY（全角 —）；半角 - 在数字列里会跟负号混淆").toEqual([]);
  });

  it("时间不绕过 fmtDateTime 直接 toLocaleString（那会跟浏览器时区跑）", () => {
    const hits: string[] = [];
    for (const f of files) {
      read(f).split("\n").forEach((ln, i) => {
        if (/new Date\([^)]*\)\.toLocale(String|DateString|TimeString)\(\s*\)/.test(ln)) {
          hits.push(`${rel(f)}:${i + 1}`);
        }
      });
    }
    expect(hits, "时间请走 fmtDateTime / fmtRelative：它们锁定 Asia/Shanghai").toEqual([]);
  });

  it("金额不手工拼 ¥（一律走 fmtMoney 系列）", () => {
    const hits: string[] = [];
    for (const f of files) {
      read(f).split("\n").forEach((ln, i) => {
        // 模板串里 `¥${...}` 或字符串加法 "¥" + x
        if (/`[^`]*¥\$\{/.test(ln) || /"¥"\s*\+/.test(ln)) hits.push(`${rel(f)}:${i + 1}`);
      });
    }
    expect(hits, "金额请走 fmtMoney/fmtMoney0/fmtWan，保证千分位与小数位全站一致").toEqual([]);
  });
});
