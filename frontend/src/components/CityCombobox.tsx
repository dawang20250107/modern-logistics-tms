import { useEffect, useMemo, useRef, useState } from "react";

import { CITIES } from "../data/cities";

/**
 * 城市组合框：可下拉、可模糊检索、可自由录入（表中未含的城市直接输入即可）。
 * 值为城市中文名字符串，受控于 value/onChange。
 */
export function CityCombobox({
  value, onChange, placeholder = "城市", style, options = CITIES,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  style?: React.CSSProperties;
  options?: string[];
}) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const matches = useMemo(() => {
    const q = value.trim();
    if (!q) return options.slice(0, 30);
    return options.filter((c) => c.includes(q)).slice(0, 30);
  }, [value, options]);

  const choose = (c: string) => { onChange(c); setOpen(false); };

  return (
    <div className="combobox" ref={ref} style={{ position: "relative", ...style }}>
      <input
        value={value}
        placeholder={placeholder}
        onChange={(e) => { onChange(e.target.value); setOpen(true); setActive(0); }}
        onFocus={() => setOpen(true)}
        onKeyDown={(e) => {
          if (!open && (e.key === "ArrowDown" || e.key === "Enter")) { setOpen(true); return; }
          if (e.key === "ArrowDown") { e.preventDefault(); setActive((a) => Math.min(a + 1, matches.length - 1)); }
          else if (e.key === "ArrowUp") { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)); }
          else if (e.key === "Enter" && matches[active]) { e.preventDefault(); choose(matches[active]); }
          else if (e.key === "Escape") setOpen(false);
        }}
        style={{ width: "100%" }}
      />
      {open && matches.length > 0 && (
        <ul className="combobox-menu">
          {matches.map((c, i) => (
            <li
              key={c}
              className={i === active ? "active" : ""}
              onMouseDown={(e) => { e.preventDefault(); choose(c); }}
              onMouseEnter={() => setActive(i)}
            >
              {c}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
