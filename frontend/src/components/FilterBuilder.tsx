import { useEffect, useRef, useState } from "react";

import { FloatingLayer, anchor, point } from "./FloatingLayer";

// 多条件高级筛选器：字段类型 文本/枚举/数值/日期，条件间 AND / OR 组合。
export type FieldType = "text" | "enum" | "number" | "date";

export interface FilterFieldDef {
  key: string;
  label: string;
  type: FieldType;
  options?: { value: string; label: string }[]; // 枚举
  accessor: (row: unknown) => string | number | null | undefined;
}

export interface FilterCondition {
  id: string;
  field: string;
  op: string;
  value: unknown; // string | number | [number,number] | string[] | [string,string]
}

export interface FilterModel {
  combinator: "and" | "or";
  conditions: FilterCondition[];
}

export const EMPTY_MODEL: FilterModel = { combinator: "and", conditions: [] };

const OPS: Record<FieldType, { op: string; label: string; noValue?: boolean }[]> = {
  text: [
    { op: "contains", label: "包含" }, { op: "ncontains", label: "不包含" },
    { op: "eq", label: "等于" }, { op: "neq", label: "不等于" },
    { op: "empty", label: "为空", noValue: true }, { op: "nempty", label: "非空", noValue: true },
  ],
  enum: [{ op: "in", label: "是其一" }, { op: "nin", label: "不是" }],
  number: [
    { op: "gt", label: "大于" }, { op: "lt", label: "小于" },
    { op: "gte", label: "≥" }, { op: "lte", label: "≤" },
    { op: "eq", label: "等于" }, { op: "between", label: "区间" },
  ],
  date: [
    { op: "on", label: "当天" }, { op: "after", label: "不早于" },
    { op: "before", label: "不晚于" }, { op: "between", label: "区间" },
  ],
};

let _seq = 0;
export function newCondition(field: FilterFieldDef): FilterCondition {
  _seq += 1;
  const op = OPS[field.type][0].op;
  return { id: `c${_seq}`, field: field.key, op, value: field.type === "enum" ? [] : field.type === "number" || field.type === "date" ? (op === "between" ? ["", ""] : "") : "" };
}

// 单条件求值
function testCondition(row: unknown, cond: FilterCondition, field: FilterFieldDef): boolean {
  const raw = field.accessor(row);
  if (field.type === "text" || field.type === "enum") {
    const s = String(raw ?? "");
    switch (cond.op) {
      case "contains": return s.toLowerCase().includes(String(cond.value ?? "").toLowerCase());
      case "ncontains": return !s.toLowerCase().includes(String(cond.value ?? "").toLowerCase());
      case "eq": return s === String(cond.value ?? "");
      case "neq": return s !== String(cond.value ?? "");
      case "empty": return s === "";
      case "nempty": return s !== "";
      case "in": return Array.isArray(cond.value) ? (cond.value as string[]).includes(s) : false;
      case "nin": return Array.isArray(cond.value) ? !(cond.value as string[]).includes(s) : true;
      default: return true;
    }
  }
  if (field.type === "number") {
    const n = Number(raw ?? 0);
    const v = cond.value;
    switch (cond.op) {
      case "gt": return n > Number(v);
      case "lt": return n < Number(v);
      case "gte": return n >= Number(v);
      case "lte": return n <= Number(v);
      case "eq": return n === Number(v);
      case "between": { const [a, b] = (v as [string, string]) || ["", ""]; return (a === "" || n >= Number(a)) && (b === "" || n <= Number(b)); }
      default: return true;
    }
  }
  // date：accessor 返回 ISO 字符串
  const t = raw ? new Date(String(raw)).getTime() : NaN;
  if (Number.isNaN(t)) return false;
  const day = (d: string) => new Date(`${d}T00:00:00`).getTime();
  const dayEnd = (d: string) => new Date(`${d}T23:59:59`).getTime();
  switch (cond.op) {
    case "on": return cond.value ? (t >= day(String(cond.value)) && t <= dayEnd(String(cond.value))) : true;
    case "after": return cond.value ? t >= day(String(cond.value)) : true;
    case "before": return cond.value ? t <= dayEnd(String(cond.value)) : true;
    case "between": { const [a, b] = (cond.value as [string, string]) || ["", ""]; return (!a || t >= day(a)) && (!b || t <= dayEnd(b)); }
    default: return true;
  }
}

// 条件是否已填齐（未填的忽略，避免空条件误伤）
function condReady(cond: FilterCondition, field: FilterFieldDef): boolean {
  const noVal = OPS[field.type].find((o) => o.op === cond.op)?.noValue;
  if (noVal) return true;
  if (field.type === "enum") return Array.isArray(cond.value) && cond.value.length > 0;
  if (cond.op === "between") { const [a, b] = (cond.value as [string, string]) || ["", ""]; return Boolean(a || b); }
  return cond.value !== "" && cond.value != null;
}

export function activeConditionCount(model: FilterModel, fields: FilterFieldDef[]): number {
  return model.conditions.filter((c) => { const f = fields.find((x) => x.key === c.field); return f && condReady(c, f); }).length;
}

export function applyFilterModel<T>(rows: T[], model: FilterModel, fields: FilterFieldDef[]): T[] {
  const active = model.conditions
    .map((c) => ({ c, f: fields.find((x) => x.key === c.field) }))
    .filter((x): x is { c: FilterCondition; f: FilterFieldDef } => Boolean(x.f) && condReady(x.c, x.f!));
  if (active.length === 0) return rows;
  return rows.filter((row) => {
    const results = active.map(({ c, f }) => testCondition(row, c, f));
    return model.combinator === "and" ? results.every(Boolean) : results.some(Boolean);
  });
}

export function describeCondition(cond: FilterCondition, fields: FilterFieldDef[]): string {
  const f = fields.find((x) => x.key === cond.field);
  if (!f) return "";
  const opLabel = OPS[f.type].find((o) => o.op === cond.op)?.label ?? cond.op;
  if (OPS[f.type].find((o) => o.op === cond.op)?.noValue) return `${f.label} ${opLabel}`;
  let v = "";
  if (f.type === "enum" && Array.isArray(cond.value)) {
    v = (cond.value as string[]).map((val) => f.options?.find((o) => o.value === val)?.label ?? val).join("/");
  } else if (cond.op === "between" && Array.isArray(cond.value)) {
    v = (cond.value as string[]).join(" ~ ");
  } else {
    v = String(cond.value ?? "");
  }
  return `${f.label} ${opLabel} ${v}`;
}

// ── UI ──────────────────────────────────────────────────
export function FilterBuilder({
  fields, model, onChange, onClose,
}: {
  fields: FilterFieldDef[];
  model: FilterModel;
  onChange: (m: FilterModel) => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const openerRef = useRef<HTMLElement | null>(
    typeof document !== "undefined" && document.activeElement instanceof HTMLElement ? document.activeElement : null,
  );
  const close = (restoreFocus = false) => {
    onClose();
    if (restoreFocus) requestAnimationFrame(() => openerRef.current?.focus());
  };
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (!ref.current?.contains(target) && !openerRef.current?.contains(target)) close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        close(true);
      }
    };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDown); window.removeEventListener("keydown", onKey); };
  }, [onClose]);

  useEffect(() => {
    const frame = window.requestAnimationFrame(() => {
      ref.current?.querySelector<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])')?.focus();
    });
    return () => window.cancelAnimationFrame(frame);
  }, []);

  const setCond = (id: string, patch: Partial<FilterCondition>) =>
    onChange({ ...model, conditions: model.conditions.map((c) => (c.id === id ? { ...c, ...patch } : c)) });
  const removeCond = (id: string) => onChange({ ...model, conditions: model.conditions.filter((c) => c.id !== id) });
  const addCond = () => onChange({ ...model, conditions: [...model.conditions, newCondition(fields[0])] });

  const changeField = (id: string, key: string) => {
    const f = fields.find((x) => x.key === key)!;
    const nc = newCondition(f);
    setCond(id, { field: key, op: nc.op, value: nc.value });
  };

  return (
    <FloatingLayer
      className="fb-pop"
      ref={ref}
      origin={openerRef.current ? anchor(openerRef.current, "start") : point({ x: 10, y: 10 })}
      role="dialog"
      aria-label="高级筛选"
      onClick={(e) => e.stopPropagation()}
    >
      <div className="fb-head">
        <span>高级筛选</span>
        <div className="fb-comb">
          <button type="button" className={model.combinator === "and" ? "on" : ""} onClick={() => onChange({ ...model, combinator: "and" })}>满足全部 AND</button>
          <button type="button" className={model.combinator === "or" ? "on" : ""} onClick={() => onChange({ ...model, combinator: "or" })}>满足任一 OR</button>
        </div>
      </div>
      <div className="fb-body">
        {model.conditions.length === 0 && <div className="muted small" style={{ padding: "6px 2px" }}>暂无条件，点「+ 添加条件」按字段组合筛选。</div>}
        {model.conditions.map((c) => {
          const f = fields.find((x) => x.key === c.field) ?? fields[0];
          const ops = OPS[f.type];
          const noVal = ops.find((o) => o.op === c.op)?.noValue;
          return (
            <div className="fb-row" key={c.id}>
              <select className="search" value={c.field} onChange={(e) => changeField(c.id, e.target.value)}>
                {fields.map((ff) => <option key={ff.key} value={ff.key}>{ff.label}</option>)}
              </select>
              <select className="search" value={c.op} onChange={(e) => setCond(c.id, { op: e.target.value, value: e.target.value === "between" ? ["", ""] : f.type === "enum" ? c.value : "" })}>
                {ops.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
              </select>
              <div className="fb-val">
                {noVal ? <span className="muted small">（无需取值）</span>
                  : f.type === "enum" ? (
                    <div className="fb-enum">
                      {(f.options ?? []).map((o) => {
                        const arr = (c.value as string[]) || [];
                        const on = arr.includes(o.value);
                        return <button type="button" key={o.value} className={`chip${on ? " chip-on" : ""}`} onClick={() => setCond(c.id, { value: on ? arr.filter((v) => v !== o.value) : [...arr, o.value] })}>{o.label}</button>;
                      })}
                    </div>
                  ) : c.op === "between" ? (
                    <span className="fb-between">
                      <input className="search" type={f.type === "date" ? "date" : "number"} value={((c.value as string[]) || ["", ""])[0]} onChange={(e) => setCond(c.id, { value: [e.target.value, ((c.value as string[]) || ["", ""])[1]] })} />
                      <span className="muted">~</span>
                      <input className="search" type={f.type === "date" ? "date" : "number"} value={((c.value as string[]) || ["", ""])[1]} onChange={(e) => setCond(c.id, { value: [((c.value as string[]) || ["", ""])[0], e.target.value] })} />
                    </span>
                  ) : (
                    <input className="search" type={f.type === "date" ? "date" : f.type === "number" ? "number" : "text"} value={String(c.value ?? "")} placeholder="取值" onChange={(e) => setCond(c.id, { value: e.target.value })} />
                  )}
              </div>
              <button type="button" className="fb-del" title="删除条件" onClick={() => removeCond(c.id)}>×</button>
            </div>
          );
        })}
      </div>
      <div className="fb-foot">
        <button type="button" className="linkish small" onClick={addCond}>+ 添加条件</button>
        <div style={{ flex: 1 }} />
        <button type="button" className="linkish small" onClick={() => onChange(EMPTY_MODEL)}>清空</button>
        <button type="button" className="btn-ghost small" onClick={() => close(true)}>完成</button>
      </div>
    </FloatingLayer>
  );
}
