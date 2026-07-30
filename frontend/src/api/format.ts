// 展示格式化契约。
//
// 这个模块存在的意义是：**同一种数据在全站只有一种长相**。
// 判断散出去就必然长出分歧——空值占位符曾经同时存在 `—`（77 处）和 `-`（39 处），
// 资源库用半角、订单/对账用全角，用户在相邻功能间切换就能看见两种符号。
//
// 另一条更要紧：**空 ≠ 零**。fmtMoney(null) 原先返回 "¥0.00"，在对账场景里
// 「应付未知」和「应付零元」显示成同一个东西——前者是待确认，后者是不用付钱。
// 于是 8 个调用点各自在外面补 `Number(x) > 0 ? fmtMoney(x) : "—"`，判断重复 8 遍，
// 还顺带带出了那 2 种破折号。现在由这里统一区分。

/** 空值占位符。全角破折号（U+2014）：半角 `-` 在等宽数字列里会跟负号混淆。 */
export const EMPTY = "—";

/** null / undefined / 空串 / 非有限数 都算「没有值」；0 是真实的零，不算空。 */
function toNum(value: number | string | null | undefined): number | null {
  if (value === null || value === undefined || value === "") return null;
  const n = typeof value === "string" ? Number(value) : value;
  return Number.isFinite(n) ? n : null;
}

/**
 * 金额：¥ + 千分位 + 两位小数。
 * 没有值 → EMPTY（不是 ¥0.00）。真实的 0 → ¥0.00。
 */
export function fmtMoney(value: number | string | null | undefined): string {
  const n = toNum(value);
  if (n === null) return EMPTY;
  return "¥" + n.toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/**
 * 金额，但「没有值」时也要显示 ¥0.00 —— 用于合计、余额这类
 * 「算出来确实是零」的位置：那里显示破折号反而像是漏算了。
 */
export function fmtMoney0(value: number | string | null | undefined): string {
  const n = toNum(value);
  return "¥" + (n ?? 0).toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** 数量：千分位，位数可指定。没有值 → EMPTY。 */
export function fmtNum(value: number | string | null | undefined, digits = 0): string {
  const n = toNum(value);
  if (n === null) return EMPTY;
  return n.toLocaleString("zh-CN", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

/** 数量，没有值时按 0 显示（计数、合计用）。 */
export function fmtNum0(value: number | string | null | undefined, digits = 0): string {
  const n = toNum(value) ?? 0;
  return n.toLocaleString("zh-CN", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

/** 任意文本的空值兜底：统一走 EMPTY，不要在调用点写 `|| "-"`。 */
export function orEmpty(value: string | null | undefined): string {
  const s = (value ?? "").trim();
  return s === "" ? EMPTY : s;
}

/**
 * 大额紧凑显示（¥万/¥亿）。
 *
 * 这一个函数此前在三处各写了一遍，而且三份规则都不一样：驾驶舱磁贴的「亿」保留
 * 2 位、图表轴标签保留 1 位；「万」一处 1 位、一处 0 位。同一笔钱在驾驶舱和图表
 * 里长相不同——看的人会以为是两个数。位数改成参数，规则只有这一份。
 *
 * compact=true 用于图表坐标轴：那里空间窄，且轴标签只需给量级。
 */
export function fmtWan(value: number | string | null | undefined, compact = false): string {
  const n = toNum(value);
  if (n === null) return EMPTY;
  const abs = Math.abs(n);
  if (abs >= 1e8) return `¥${(n / 1e8).toFixed(compact ? 1 : 2)}亿`;
  if (abs >= 1e4) return `¥${(n / 1e4).toFixed(compact ? 0 : 1)}万`;
  return compact ? `¥${fmtNum0(n)}` : fmtMoney(n);
}

export function fmtDateTime(iso: string | null | undefined): string {
  if (!iso) return EMPTY;
  const d = new Date(iso);
  if (!Number.isFinite(d.getTime())) return EMPTY;
  return d.toLocaleString("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit",
  });
}

/** 紧凑绝对时间：`07-19 14:32`，跨年时带年 `25-12-31 09:00`。
 *
 * 台账里「11天前」是不够的：对账、查单、追责都要能说出具体是哪天几点。
 * 相对时间只在**时效本身就是判断对象**的地方才合适（调度池"进池多久了"），
 * 在需要识别一笔业务的地方它既不精确又不好算。
 *
 * 省掉年份是因为绝大多数在看的单都是本年的；跨年时自动补上，
 * 不会出现"12-31 是哪一年的 12-31"这种歧义。
 */
export function fmtDateShort(iso: string | null | undefined): string {
  if (!iso) return EMPTY;
  const d = new Date(iso);
  if (!Number.isFinite(d.getTime())) return EMPTY;
  const opts: Intl.DateTimeFormatOptions = {
    timeZone: "Asia/Shanghai",
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false,
  };
  // 年份判断也必须在 Asia/Shanghai 下做，否则跨年夜前后本地时区会算错一年
  const yearOf = (t: Date) =>
    new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Shanghai", year: "numeric" }).format(t);
  if (yearOf(d) !== yearOf(new Date())) opts.year = "2-digit";
  // en-CA 给出 `MM-DD, HH:mm` 这种稳定的数字格式，去掉逗号即得 `07-19 14:32`
  return new Intl.DateTimeFormat("en-CA", opts).format(d).replace(",", "");
}

export function fmtRelative(iso: string | null | undefined): string {
  if (!iso) return "";
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return "";
  const diff = Date.now() - t;
  // 服务端时钟略微超前时 diff 会是负的。原先会算出「0分钟前」甚至负数分钟；
  // 未来的时间点就照实说「即将」，不要伪装成刚刚发生。
  if (diff < -60000) return "稍后";
  const min = Math.floor(Math.max(diff, 0) / 60000);
  if (min < 1) return "刚刚";
  if (min < 60) return `${min}分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}小时前`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day}天前`;
  return new Date(t).toLocaleDateString("zh-CN", { timeZone: "Asia/Shanghai" });
}
