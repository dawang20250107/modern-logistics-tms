import { useEffect, useId, useMemo, useRef, useState } from "react";

import { ALL_CITIES } from "../data/regions";
import { FloatingLayer, anchor } from "./FloatingLayer";

// 城市库以全国行政区（省/市/区）派生的完整市级列表为准，补全此前"城市库不全"。
const CITIES = ALL_CITIES;

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
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const listId = useId();

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (!ref.current?.contains(target) && !listRef.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const option = listRef.current?.children[active] as HTMLElement | undefined;
    option?.scrollIntoView({ block: "nearest" });
  }, [active, open]);

  const matches = useMemo(() => {
    const q = value.trim();
    if (!q) return options.slice(0, 30);
    return options.filter((c) => c.includes(q)).slice(0, 30);
  }, [value, options]);

  const choose = (c: string) => { onChange(c); setOpen(false); };

  return (
    <div className="combobox" ref={ref} style={{ position: "relative", ...style }}>
      <input
        ref={inputRef}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={listId}
        aria-activedescendant={open && matches[active] ? `${listId}-${active}` : undefined}
        aria-label={placeholder}
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
      {open && matches.length > 0 && inputRef.current && (
        <FloatingLayer
          ref={listRef}
          origin={anchor(inputRef.current, "start")}
          id={listId}
          className="combobox-menu"
          role="listbox"
          aria-label="城市建议"
          style={{ width: inputRef.current.getBoundingClientRect().width }}
        >
          {matches.map((c, i) => (
            <div
              id={`${listId}-${i}`}
              key={c}
              role="option"
              aria-selected={i === active}
              className={i === active ? "active" : ""}
              onMouseDown={(e) => { e.preventDefault(); choose(c); }}
              onMouseEnter={() => setActive(i)}
            >
              {c}
            </div>
          ))}
        </FloatingLayer>
      )}
    </div>
  );
}
