// 主题（亮/暗）：亮色为主，暗色可切换。持久化到 localStorage，
// 在 <html data-theme> 上落值，styles.css 的暗色 token 层据此级联全站。
export type Theme = "light" | "dark";

const KEY = "tms-theme";

export function getTheme(): Theme {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "dark" || v === "light") return v;
  } catch {
    /* ignore */
  }
  return "light"; // 亮为主：默认亮色（不自动跟随系统暗色）
}

export function applyTheme(t: Theme): void {
  document.documentElement.dataset.theme = t;
}

export const THEME_EVENT = "tms-theme-change";

let animTimer: ReturnType<typeof setTimeout> | undefined;

export function setTheme(t: Theme): void {
  // 切换瞬间对全元素启用短暂 crossfade，避免明暗硬切的突兀感
  const root = document.documentElement;
  root.classList.add("theme-anim");
  if (animTimer) clearTimeout(animTimer);
  animTimer = setTimeout(() => root.classList.remove("theme-anim"), 320);

  applyTheme(t);
  try {
    localStorage.setItem(KEY, t);
  } catch {
    /* ignore */
  }
  window.dispatchEvent(new CustomEvent(THEME_EVENT, { detail: t }));
}

export function toggleTheme(): Theme {
  const next: Theme = getTheme() === "dark" ? "light" : "dark";
  setTheme(next);
  return next;
}

// 渲染前调用，避免明暗闪烁
export function initTheme(): void {
  applyTheme(getTheme());
}

// 读取一组 CSS 变量的解析值；随主题切换重算（供 recharts 等需要真实色值的场景）
export function readCssVars(names: string[]): Record<string, string> {
  const cs = getComputedStyle(document.documentElement);
  const out: Record<string, string> = {};
  for (const n of names) out[n] = cs.getPropertyValue(n).trim();
  return out;
}
