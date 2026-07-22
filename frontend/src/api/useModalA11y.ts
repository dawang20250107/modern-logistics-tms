import { useEffect, useRef, type RefObject } from "react";

const FOCUSABLE =
  'a[href],button:not([disabled]),textarea:not([disabled]),input:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])';

// 弹窗/抽屉无障碍与键盘工学：打开时聚焦首个可交互元素，Tab 在弹窗内循环（焦点陷阱），
// Esc 关闭，关闭后把焦点还给打开前的元素。传入 active + 容器 ref + onClose 即可。
export function useModalA11y(active: boolean, ref: RefObject<HTMLElement | null>, onClose: () => void) {
  const closeRef = useRef(onClose);

  useEffect(() => {
    closeRef.current = onClose;
  }, [onClose]);

  useEffect(() => {
    if (!active) return;
    const restore = document.activeElement as HTMLElement | null;
    const node = ref.current;
    const focusables = () =>
      node ? Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((e) => e.offsetParent !== null) : [];

    // 打开即聚焦：若已有 autoFocus 把焦点放进弹窗内则尊重之，否则聚焦首个可交互元素
    const alreadyInside = node ? node.contains(document.activeElement) : false;
    if (!alreadyInside) {
      const first = focusables()[0];
      (first ?? node)?.focus?.();
    }

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.stopPropagation(); closeRef.current(); return; }
      if (e.key !== "Tab" || !node) return;
      const f = focusables();
      if (f.length === 0) { e.preventDefault(); return; }
      const firstEl = f[0];
      const lastEl = f[f.length - 1];
      if (e.shiftKey && document.activeElement === firstEl) { e.preventDefault(); lastEl.focus(); }
      else if (!e.shiftKey && document.activeElement === lastEl) { e.preventDefault(); firstEl.focus(); }
    };
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      restore?.focus?.();
    };
  }, [active, ref]);
}
