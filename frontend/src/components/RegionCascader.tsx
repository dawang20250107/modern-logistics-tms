import { useEffect, useRef, useState } from "react";

import { PROVINCES, citiesOf, districtsOf } from "../data/regions";
import { toast } from "../api/toast";

export interface RegionValue { province: string; city: string; district: string }

// 省/市/区三级级联选址（列式弹层）+ 地图选址（预留，待配置高德 key）。
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

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") { e.stopPropagation(); setOpen(false); } };
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
  };

  return (
    <div className="region-cascader" ref={ref} style={{ position: "relative", ...style }}>
      <button type="button" className="region-trigger" onClick={() => setOpen((v) => !v)}>
        <span className={label ? "" : "muted"}>{label || placeholder}</span>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M6 9l6 6 6-6" /></svg>
      </button>
      {open && (
        <div className="region-pop">
          <div className="region-cols">
            <ul className="region-col">
              {PROVINCES.map((p) => (
                <li key={p} className={p === prov ? "on" : ""} onClick={() => { setProv(p); setCity(""); }}>{p}</li>
              ))}
            </ul>
            <ul className="region-col">
              {cities.length === 0 ? <li className="muted">选省份</li> : cities.map((c) => (
                <li key={c} className={c === city ? "on" : ""} onClick={() => setCity(c)}>{c}</li>
              ))}
            </ul>
            <ul className="region-col">
              {districts.length === 0 ? <li className="muted">选城市</li> : districts.map((d) => (
                <li key={d} onClick={() => pickDistrict(d)}>{d}</li>
              ))}
            </ul>
          </div>
          <div className="region-foot">
            <button type="button" className="linkish" onClick={() => { onChange({ province: "", city: "", district: "" }); setProv(""); setCity(""); }}>清空</button>
            <button
              type="button"
              className="btn-ghost"
              style={{ padding: "4px 10px", fontSize: 12 }}
              onClick={() => toast.info("地图选址为预留能力：配置高德地图 AMAP_KEY 后可拖拽打点、逆地理编码自动回填省市区。")}
            >
              <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 10c0 6-9 12-9 12s-9-6-9-12a9 9 0 0 1 18 0z"/><circle cx="12" cy="10" r="3"/></svg>
                地图选址（预留）</span>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
