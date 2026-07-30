import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { apiGet } from "../api/client";

/**
 * 建单表单里的项目选择器。
 *
 * 项目是对账用得最多的归集维度，但录单是高频动作——若要客服在几十个项目里翻找，
 * 这个字段就会被跳过，然后对账那头归不了集。所以这里做三件事：
 *   1. 打开即给推荐（按当前客户 + 起终点算的，带「为什么推它」）；
 *   2. 输入即过滤，输入没命中时直接允许「新建同名项目」，不用跳走再回来；
 *   3. 始终可留空——项目是可选项，填不上不该挡住建单。
 */

export interface ProjectSuggestion {
  id: string;
  project_no: string;
  name: string;
  customer: string | null;
  customer_name: string;
  score: number;
  reason: string;
}

interface Props {
  /** 已选项目 id；留空表示未选 */
  value: string;
  /** 已选项目名（新建时为待创建的名字，此时 value 为空） */
  valueName: string;
  onChange: (v: { id: string; name: string }) => void;
  customer?: string;
  origin?: string;
  destination?: string;
  disabled?: boolean;
}

export function ProjectPicker({ value, valueName, onChange, customer, origin, destination, disabled }: Props) {
  const [open, setOpen] = useState(false);
  const [keyword, setKeyword] = useState("");
  const boxRef = useRef<HTMLDivElement>(null);

  const params = new URLSearchParams();
  if (customer) params.set("customer", customer);
  if (origin) params.set("origin", origin);
  if (destination) params.set("destination", destination);
  if (keyword.trim()) params.set("q", keyword.trim());

  const suggest = useQuery({
    queryKey: ["project-suggest", customer, origin, destination, keyword.trim()],
    queryFn: () => apiGet<{ items: ProjectSuggestion[] }>(`/finance/projects/suggest?${params.toString()}`),
    enabled: open,
    staleTime: 30_000,
  });

  // 点击外部关闭
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  const items = suggest.data?.items ?? [];
  const typed = keyword.trim();
  // 输入的名字在候选里没有完全同名的 → 提供「新建」入口
  const canCreate = useMemo(
    () => typed.length > 0 && !items.some((i) => i.name.trim().toLowerCase() === typed.toLowerCase()),
    [typed, items],
  );

  const display = valueName || (value ? "已选项目" : "");

  return (
    <div ref={boxRef} style={{ position: "relative" }}>
      <div
        role="button"
        tabIndex={disabled ? -1 : 0}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => !disabled && setOpen((v) => !v)}
        onKeyDown={(e) => {
          if (disabled) return;
          if (e.key === "Enter" || e.key === " ") { e.preventDefault(); setOpen((v) => !v); }
          if (e.key === "Escape") setOpen(false);
        }}
        style={{
          display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8,
          padding: "8px 10px", borderRadius: "var(--radius-sm, 8px)",
          border: "1px solid var(--line)", background: "var(--input-bg, #fff)",
          cursor: disabled ? "not-allowed" : "pointer", opacity: disabled ? 0.6 : 1,
          minHeight: 36,
        }}
      >
        <span style={{ color: display ? "var(--ink)" : "var(--muted)", fontSize: 13 }}>
          {display || "选择项目（可选）"}
        </span>
        <span style={{ display: "flex", gap: 6, alignItems: "center" }}>
          {display && !disabled && (
            <button
              type="button"
              aria-label="清空项目"
              onClick={(e) => { e.stopPropagation(); onChange({ id: "", name: "" }); }}
              style={{ border: "none", background: "transparent", color: "var(--muted)", cursor: "pointer", fontSize: 14, lineHeight: 1 }}
            >×</button>
          )}
          <span style={{ color: "var(--muted)", fontSize: 11 }}>▾</span>
        </span>
      </div>

      {open && (
        <div
          role="listbox"
          style={{
            position: "absolute", zIndex: 40, top: "calc(100% + 4px)", left: 0, right: 0,
            background: "var(--surface-solid)", border: "1px solid var(--line-2)",
            borderRadius: "var(--radius-sm)", boxShadow: "var(--shadow-md)",
            maxHeight: 320, overflowY: "auto", padding: 6,
          }}
        >
          <input
            autoFocus
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder="搜索或输入新项目名"
            style={{
              width: "100%", padding: "7px 9px", marginBottom: 6, fontSize: 13,
              border: "1px solid var(--line)", borderRadius: 6, background: "var(--input-bg, #fff)",
            }}
          />

          {suggest.isLoading && <div style={{ padding: 10, fontSize: 12, color: "var(--muted)" }}>加载中…</div>}

          {!suggest.isLoading && items.length === 0 && !canCreate && (
            <div style={{ padding: 10, fontSize: 12, color: "var(--muted)" }}>
              暂无项目，输入名称可直接新建
            </div>
          )}

          {items.map((p, i) => (
            <button
              key={p.id}
              type="button"
              role="option"
              aria-selected={p.id === value}
              onClick={() => { onChange({ id: p.id, name: p.name }); setOpen(false); setKeyword(""); }}
              style={{
                display: "block", width: "100%", textAlign: "left", padding: "7px 9px",
                border: "none", borderRadius: 6, cursor: "pointer", fontSize: 13,
                // --chip-bg 从未定义过，一直走的是 fallback #eef2ff 这个写死的浅蓝，
                // 暗色主题下不跟随。选中项底色本来就有语义 token。
                background: p.id === value ? "var(--accent-weak)" : "transparent",
              }}
            >
              <div style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
                <span style={{ fontWeight: 500 }}>{p.name}</span>
                {/* 首条且确有历史依据时标「推荐」，避免把无依据的第一条也说成推荐 */}
                {i === 0 && p.score > 0 && (
                  <span style={{ fontSize: 11, color: "var(--brand)", flexShrink: 0 }}>推荐</span>
                )}
              </div>
              <div style={{ fontSize: 11, color: "var(--muted)", marginTop: 2 }}>
                {p.project_no}
                {p.customer_name ? ` · ${p.customer_name}` : ""}
                {p.reason ? ` · ${p.reason}` : ""}
              </div>
            </button>
          ))}

          {canCreate && (
            <button
              type="button"
              onClick={() => { onChange({ id: "", name: typed }); setOpen(false); setKeyword(""); }}
              style={{
                display: "block", width: "100%", textAlign: "left", padding: "7px 9px",
                marginTop: items.length ? 4 : 0, borderTop: items.length ? "1px solid var(--line)" : "none",
                border: "none", background: "transparent", cursor: "pointer",
                fontSize: 13, color: "var(--brand)",
              }}
            >
              + 新建项目「{typed}」
            </button>
          )}
        </div>
      )}

      {/* 新建（尚未落库）时给出明确提示，避免客服以为选了个已有项目 */}
      {!value && valueName && (
        <div style={{ fontSize: 11, color: "var(--muted)", marginTop: 4 }}>
          建单时将自动创建该项目
        </div>
      )}
    </div>
  );
}
