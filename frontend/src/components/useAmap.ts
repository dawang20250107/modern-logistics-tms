import { useEffect, useState } from "react";

export const AMAP_KEY = (import.meta.env.VITE_AMAP_KEY as string | undefined) ?? "";

/** 动态加载高德 JS API（仅在配置了 Key 时）。返回 window.AMap 构造器或 null。 */
export function useAmap(): unknown {
  const [amap, setAmap] = useState<unknown>(null);
  useEffect(() => {
    if (!AMAP_KEY) return;
    const w = window as unknown as { AMap?: unknown };
    if (w.AMap) {
      setAmap(w.AMap);
      return;
    }
    const existing = document.getElementById("amap-sdk") as HTMLScriptElement | null;
    if (existing) {
      existing.addEventListener("load", () => setAmap((window as unknown as { AMap?: unknown }).AMap ?? null));
      return;
    }
    const script = document.createElement("script");
    script.id = "amap-sdk";
    script.src = `https://webapi.amap.com/maps?v=2.0&key=${AMAP_KEY}`;
    script.async = true;
    script.onload = () => setAmap((window as unknown as { AMap?: unknown }).AMap ?? null);
    document.head.appendChild(script);
  }, []);
  return amap;
}
