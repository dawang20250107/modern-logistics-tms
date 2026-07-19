import { Fragment, useEffect, useMemo, useRef, useState } from "react";

// 顶尖 SaaS 表格：列显隐 / 列宽拖拽 / 固定首列 / 多字段排序 / 保存视图 / 批量 / 行内 / 右键 / 导出
export interface DataColumn<T> {
  key: string;
  header: string;
  render: (row: T) => React.ReactNode;
  sortValue?: (row: T) => string | number;
  exportValue?: (row: T) => string | number;
  width?: number;
  minWidth?: number;
  align?: "left" | "right";
  defaultHidden?: boolean;
  alwaysVisible?: boolean;
}

interface SortState { key: string; dir: "asc" | "desc" }
interface ViewState { hidden: string[]; widths: Record<string, number>; sort: SortState | null }

function loadView(viewKey: string): ViewState | null {
  try {
    const raw = localStorage.getItem(`dt.view.${viewKey}`);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

export function DataTable<T>({
  columns, rows, rowKey, viewKey, selectable, selected, onToggle, onToggleAll,
  onRowContextMenu, onRowDoubleClick, onRowClick, rowClassName, stickyFirst, toolbarLeft, batchBar, exportName,
  expandedKey, renderExpanded,
}: {
  columns: DataColumn<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  viewKey: string;
  selectable?: boolean;
  selected?: Set<string>;
  onToggle?: (id: string) => void;
  onToggleAll?: () => void;
  onRowContextMenu?: (e: React.MouseEvent, row: T) => void;
  onRowDoubleClick?: (row: T) => void;
  onRowClick?: (row: T) => void;
  expandedKey?: string;
  renderExpanded?: (row: T) => React.ReactNode;
  rowClassName?: (row: T) => string;
  stickyFirst?: boolean;
  toolbarLeft?: React.ReactNode;
  batchBar?: React.ReactNode;
  exportName?: string;
}) {
  const saved = useMemo(() => loadView(viewKey), [viewKey]);
  const [hidden, setHidden] = useState<Set<string>>(
    () => new Set(saved?.hidden ?? columns.filter((c) => c.defaultHidden).map((c) => c.key)),
  );
  const [widths, setWidths] = useState<Record<string, number>>(saved?.widths ?? {});
  const [sort, setSort] = useState<SortState | null>(saved?.sort ?? null);
  const [colMenu, setColMenu] = useState(false);

  // 保存视图：列显隐 / 列宽 / 排序 持久化
  useEffect(() => {
    try {
      localStorage.setItem(`dt.view.${viewKey}`, JSON.stringify({ hidden: [...hidden], widths, sort }));
    } catch {
      /* ignore */
    }
  }, [viewKey, hidden, widths, sort]);

  useEffect(() => {
    const close = () => setColMenu(false);
    window.addEventListener("click", close);
    return () => window.removeEventListener("click", close);
  }, []);

  const visibleCols = columns.filter((c) => !hidden.has(c.key));

  const sortedRows = useMemo(() => {
    if (!sort) return rows;
    const col = columns.find((c) => c.key === sort.key);
    if (!col?.sortValue) return rows;
    const val = col.sortValue;
    const dir = sort.dir === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      const va = val(a), vb = val(b);
      if (va < vb) return -1 * dir;
      if (va > vb) return 1 * dir;
      return 0;
    });
  }, [rows, sort, columns]);

  const cycleSort = (key: string) => {
    setSort((s) => {
      if (!s || s.key !== key) return { key, dir: "asc" };
      if (s.dir === "asc") return { key, dir: "desc" };
      return null;
    });
  };

  // 列宽拖拽
  const dragRef = useRef<{ key: string; startX: number; startW: number } | null>(null);
  const onResizeStart = (e: React.MouseEvent, key: string, curW: number) => {
    e.preventDefault();
    e.stopPropagation();
    dragRef.current = { key, startX: e.clientX, startW: curW };
    const onMove = (ev: MouseEvent) => {
      if (!dragRef.current) return;
      const col = columns.find((c) => c.key === dragRef.current!.key);
      const min = col?.minWidth ?? 60;
      const w = Math.max(min, dragRef.current.startW + (ev.clientX - dragRef.current.startX));
      setWidths((prev) => ({ ...prev, [dragRef.current!.key]: w }));
    };
    const onUp = () => {
      dragRef.current = null;
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  };

  const colWidth = (c: DataColumn<T>) => widths[c.key] ?? c.width ?? 140;

  const exportCsv = () => {
    const cols = visibleCols;
    const esc = (v: string | number) => `"${String(v ?? "").replace(/"/g, '""')}"`;
    const head = cols.map((c) => esc(c.header)).join(",");
    const body = sortedRows.map((r) =>
      cols.map((c) => esc(c.exportValue ? c.exportValue(r) : (c.sortValue ? c.sortValue(r) : ""))).join(","),
    );
    const csv = "﻿" + [head, ...body].join("\r\n");
    const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${exportName ?? viewKey}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const allChecked = selectable && rows.length > 0 && selected && rows.every((r) => selected.has(rowKey(r)));
  const stickyOffset = selectable ? 34 : 0;

  return (
    <div className="dt">
      <div className="dt-toolbar">
        <div style={{ display: "flex", alignItems: "center", gap: 8, flex: 1, flexWrap: "wrap" }}>{toolbarLeft}</div>
        <div style={{ display: "flex", alignItems: "center", gap: 6, position: "relative" }}>
          <button className="btn-ghost" onClick={exportCsv}>导出</button>
          <button className="btn-ghost" onClick={(e) => { e.stopPropagation(); setColMenu((v) => !v); }}>列</button>
          {colMenu && (
            <div className="dt-colmenu" onClick={(e) => e.stopPropagation()}>
              <div className="muted small" style={{ padding: "2px 8px 6px" }}>显示列</div>
              {columns.map((c) => (
                <label key={c.key} className="dt-colitem">
                  <input
                    type="checkbox"
                    checked={!hidden.has(c.key)}
                    disabled={c.alwaysVisible}
                    onChange={() => setHidden((h) => { const n = new Set(h); if (n.has(c.key)) n.delete(c.key); else n.add(c.key); return n; })}
                  />
                  {c.header}
                </label>
              ))}
              <div className="context-divider" />
              <button className="dt-colreset" onClick={() => { setHidden(new Set(columns.filter((c) => c.defaultHidden).map((c) => c.key))); setWidths({}); setSort(null); }}>重置视图</button>
            </div>
          )}
        </div>
      </div>

      {batchBar}

      <div className="dt-scroll">
        <table className="table dt-table">
          <thead>
            <tr>
              {selectable && (
                <th className="cell-check dt-sticky" style={{ left: 0 }}>
                  <input type="checkbox" checked={Boolean(allChecked)} onChange={onToggleAll} aria-label="全选" />
                </th>
              )}
              {visibleCols.map((c, i) => {
                const sticky = stickyFirst && i === 0;
                const left = sticky ? stickyOffset : undefined;
                return (
                  <th
                    key={c.key}
                    className={`${c.align === "right" ? "num" : ""} ${sticky ? "dt-sticky" : ""} ${c.sortValue ? "dt-sortable" : ""}`}
                    style={{ width: colWidth(c), minWidth: colWidth(c), left }}
                    onClick={() => c.sortValue && cycleSort(c.key)}
                  >
                    <span className="dt-th">{c.header}{sort?.key === c.key && <span className="dt-sortic">{sort.dir === "asc" ? "▲" : "▼"}</span>}</span>
                    <span className="dt-resizer" onMouseDown={(e) => onResizeStart(e, c.key, colWidth(c))} />
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {sortedRows.map((r) => {
              const id = rowKey(r);
              const isSel = selected?.has(id);
              const expanded = expandedKey != null && expandedKey === id;
              const colSpan = visibleCols.length + (selectable ? 1 : 0);
              return (
                <Fragment key={id}>
                <tr
                  className={`${rowClassName?.(r) ?? ""} ${isSel ? "row-sel" : ""}${onRowClick ? " dt-clickable" : ""}`}
                  onContextMenu={onRowContextMenu ? (e) => onRowContextMenu(e, r) : undefined}
                  onDoubleClick={onRowDoubleClick ? () => onRowDoubleClick(r) : undefined}
                  onClick={onRowClick ? () => onRowClick(r) : undefined}
                >
                  {selectable && (
                    <td className="cell-check dt-sticky" style={{ left: 0 }} onClick={(e) => e.stopPropagation()}>
                      <input type="checkbox" checked={Boolean(isSel)} onChange={() => onToggle?.(id)} />
                    </td>
                  )}
                  {visibleCols.map((c, i) => {
                    const sticky = stickyFirst && i === 0;
                    return (
                      <td key={c.key} className={`${c.align === "right" ? "num" : ""} ${sticky ? "dt-sticky" : ""}`} style={{ left: sticky ? stickyOffset : undefined }}>
                        {c.render(r)}
                      </td>
                    );
                  })}
                </tr>
                {expanded && renderExpanded && (
                  <tr className="dt-expandrow">
                    <td colSpan={colSpan}>{renderExpanded(r)}</td>
                  </tr>
                )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
