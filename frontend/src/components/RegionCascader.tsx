import { useEffect, useId, useRef, useState } from "react";

import { PROVINCES, citiesOf, districtsOf } from "../data/regions";
import { FloatingLayer, anchor } from "./FloatingLayer";

export interface RegionValue { province: string; city: string; district: string }

// 省/市/区三级级联选址（列式弹层），与订单详细地址字段组合使用。
export function RegionCascader({
  value, onChange, placeholder = "选择 省 / 市 / 区", style,
}: {
  value: RegionValue;
  onChange: (v: RegionValue) => void;
  placeholder?: string;
  style?: React.CSSProperties;
}) {
  const [open, setOpen] = useState(false);
  const [prov, setProv] = useState(value.province || "");
  const [city, setCity] = useState(value.city || "");
  const ref = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const popRef = useRef<HTMLDivElement>(null);
  const popId = useId();

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (!ref.current?.contains(target) && !popRef.current?.contains(target)) setOpen(false);
    };
    popRef.current?.querySelector<HTMLButtonElement>("button")?.focus();
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") { e.stopPropagation(); setOpen(false); triggerRef.current?.focus(); } };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey, true);
    return () => { document.removeEventListener("mousedown", onDown); document.removeEventListener("keydown", onKey, true); };
  }, [open]);

  const label = [value.province, value.city, value.district].filter((x) => x && x !== "市辖区").join(" ");
  const cities = prov ? citiesOf(prov) : [];
  const districts = prov && city ? districtsOf(prov, city) : [];

  const pickDistrict = (d: string) => {
    onChange({ province: prov, city, district: d });
    setOpen(false);
    requestAnimationFrame(() => triggerRef.current?.focus());
  };

  return (
    <div className="region-cascader" ref={ref} style={{ position: "relative", ...style }}>
      <button
        ref={triggerRef}
        type="button"
        className="region-trigger"
        aria-expanded={open}
        aria-controls={popId}
        aria-haspopup="dialog"
        onClick={() => setOpen((v) => !v)}
        title={label || placeholder}
      >
        <span className={label ? "" : "muted"}>{label || placeholder}</span>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M6 9l6 6 6-6" /></svg>
      </button>
      {open && triggerRef.current && (
        <FloatingLayer ref={popRef} origin={anchor(triggerRef.current, "start")} id={popId} className="region-pop" role="dialog" aria-label="选择省市区">
          <div className="region-cols">
            <ul className="region-col" aria-label="省份">
              {PROVINCES.map((p) => (
                <li key={p} className={p === prov ? "on" : ""}>
                  <button type="button" aria-pressed={p === prov} onClick={() => { setProv(p); setCity(""); }}>{p}</button>
                </li>
              ))}
            </ul>
            <ul className="region-col" aria-label="城市">
              {cities.length === 0 ? <li className="muted">选省份</li> : cities.map((c) => (
                <li key={c} className={c === city ? "on" : ""}>
                  <button type="button" aria-pressed={c === city} onClick={() => setCity(c)}>{c}</button>
                </li>
              ))}
            </ul>
            <ul className="region-col" aria-label="区县">
              {districts.length === 0 ? <li className="muted">选城市</li> : districts.map((d) => (
                <li key={d}><button type="button" onClick={() => pickDistrict(d)}>{d}</button></li>
              ))}
            </ul>
          </div>
          <div className="region-foot">
            <button type="button" className="linkish" onClick={() => { onChange({ province: "", city: "", district: "" }); setProv(""); setCity(""); }}>清空</button>
          </div>
        </FloatingLayer>
      )}
    </div>
  );
}
