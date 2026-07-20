import { Fragment, useEffect, useMemo, useRef, useState } from "react";

// 顶尖 SaaS 表格：列显隐 / 列宽拖拽 / 固定首列 / 多字段排序 / 列内字段筛选 /
// 保存视图 / 批量 / Shift 范围选 / 行右键菜单 / 行内 / 导出
export interface DataColumn<T> {
  key: string;
  header: string;
  render: (row: T) => React.ReactNode;
  sortValue?: (row: T) => string | number;
  exportValue?: (row: T) => string | number;
  filterValue?: (row: T) => string; // 列筛选取值（缺省用 exportValue/sortValue）
  filterable?: boolean; // 开启表头字段筛选（仅客户端模式）
  sortField?: string; // 服务端排序的 ORM 字段名（server 模式下有此值才可点表头排序）
  width?: number;
  minWidth?: number;
  align?: "left" | "right";
  defaultHidden?: boolean;
  alwaysVisible?: boolean;
}

export interface ServerPage {
  serverSort: { field: string; dir: "asc" | "desc" } | null;
  onServerSort: (field: string) => void;
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
  onPageSizeChange?: (size: number) => void; // 提供则渲染「每页条数」下拉 + 跳页
  pageSizeOptions?: number[];
  loading?: boolean;
}

export interface RowMenuItem {
  label: string;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
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
  onRowContextMenu, onRowDoubleClick, onRowClick, rowClassName, stickyFirst, toolbarLeft, toolbarRight, batchBar, exportName,
  expandedKey, renderExpanded, rowMenu, hideExport, emptyState, server, fill,
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
  toolbarRight?: React.ReactNode; // 调用方注入的操作按钮，置于内置 导出/列 之前，实现单行工具条
  batchBar?: React.ReactNode;
  exportName?: string;
  rowMenu?: (row: T) => RowMenuItem[]; // 行右键菜单项
  hideExport?: boolean; // 页面已自带导出（如服务端全量导出）时隐藏内置导出，避免重复
  emptyState?: React.ReactNode; // 无数据时展示（替代默认「暂无匹配记录」），工具条仍可见以便清除筛选
  server?: ServerPage; // 服务端模式：受控排序 + 分页；禁用客户端排序/列筛选
  fill?: boolean; // 固定高度：表体撑满视口并内部滚动，分页页脚贴近底部（页面不随行下滑）
}) {
  const saved = useMemo(() => loadView(viewKey), [viewKey]);
  const [hidden, setHidden] = useState<Set<string>>(
    () => new Set(saved?.hidden ?? columns.filter((c) => c.defaultHidden).map((c) => c.key)),
  );
  const [widths, setWidths] = useState<Record<string, number>>(saved?.widths ?? {});
  const [sort, setSort] = useState<SortState | null>(saved?.sort ?? null);
  const [colMenu, setColMenu] = useState(false);
  // 列内字段筛选：colKey -> 选中值集合（空/缺省 = 不筛）
  const [filters, setFilters] = useState<Record<string, Set<string>>>({});
  const [openFilter, setOpenFilter] = useState<string | null>(null);
  const [filterSearch, setFilterSearch] = useState("");
  // 行右键菜单
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; items: RowMenuItem[] } | null>(null);
  // Shift 范围选锚点
  const anchorRef = useRef<number>(-1);

  useEffect(() => {
    try {
      localStorage.setItem(`dt.view.${viewKey}`, JSON.stringify({ hidden: [...hidden], widths, sort }));
    } catch { /* ignore */ }
  }, [viewKey, hidden, widths, sort]);

  useEffect(() => {
    const close = () => { setColMenu(false); setOpenFilter(null); setCtxMenu(null); };
    window.addEventListener("click", close);
    return () => window.removeEventListener("click", close);
  }, []);

  const visibleCols = columns.filter((c) => !hidden.has(c.key));
  const filterText = (c: DataColumn<T>, r: T): string =>
    String((c.filterValue ? c.filterValue(r) : c.exportValue ? c.exportValue(r) : c.sortValue ? c.sortValue(r) : "") ?? "");

  // 先按列筛选，再排序
  const filteredRows = useMemo(() => {
    const active = Object.entries(filters).filter(([, v]) => v && v.size > 0);
    if (active.length === 0) return rows;
    return rows.filter((r) => active.every(([key, set]) => {
      const col = columns.find((c) => c.key === key);
      return col ? set.has(filterText(col, r)) : true;
    }));
  }, [rows, filters, columns]);

  const sortedRows = useMemo(() => {
    if (!sort) return filteredRows;
    const col = columns.find((c) => c.key === sort.key);
    if (!col?.sortValue) return filteredRows;
    const val = col.sortValue;
    const dir = sort.dir === "asc" ? 1 : -1;
    return [...filteredRows].sort((a, b) => {
      const va = val(a), vb = val(b);
      if (va < vb) return -1 * dir;
      if (va > vb) return 1 * dir;
      return 0;
    });
  }, [filteredRows, sort, columns]);

  // server 模式：服务端已排序/筛选/分页，直接渲染当页 rows
  const displayRows = server ? rows : sortedRows;

  const cycleSort = (key: string) => {
    setSort((s) => {
      if (!s || s.key !== key) return { key, dir: "asc" };
      if (s.dir === "asc") return { key, dir: "desc" };
      return null;
    });
  };

  // 某列的可选值（去重、有序）
  const distinctValues = (c: DataColumn<T>): string[] => {
    const set = new Set<string>();
    for (const r of rows) set.add(filterText(c, r));
    return [...set].sort((a, b) => a.localeCompare(b, "zh"));
  };
  const toggleFilterValue = (key: string, val: string) => setFilters((f) => {
    const next = { ...f };
    const s = new Set(next[key] ?? []);
    if (s.has(val)) s.delete(val); else s.add(val);
    next[key] = s;
    return next;
  });
  const clearFilter = (key: string) => setFilters((f) => { const n = { ...f }; delete n[key]; return n; });

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
    const body = displayRows.map((r) =>
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
  const someChecked = selectable && selected && rows.some((r) => selected.has(rowKey(r)));
  const stickyOffset = selectable ? 34 : 0;
  const activeFilterCount = Object.values(filters).filter((s) => s && s.size > 0).length;

  // 复选：支持 Shift 范围选（以上次点击行为锚点，整段设为目标态）
  const handleCheck = (e: React.MouseEvent, idx: number, id: string) => {
    const target = !(selected?.has(id));
    if (e.shiftKey && anchorRef.current >= 0 && anchorRef.current !== idx) {
      const [lo, hi] = [Math.min(anchorRef.current, idx), Math.max(anchorRef.current, idx)];
      for (let i = lo; i <= hi; i++) {
        const rid = rowKey(displayRows[i]);
        if (Boolean(selected?.has(rid)) !== target) onToggle?.(rid);
      }
    } else {
      onToggle?.(id);
    }
    anchorRef.current = idx;
  };

  const openRowMenu = (e: React.MouseEvent, row: T) => {
    if (rowMenu) {
      e.preventDefault();
      setCtxMenu({ x: e.clientX, y: e.clientY, items: rowMenu(row) });
    }
    onRowContextMenu?.(e, row);
  };

  return (
    <div className={`dt${fill ? " dt-fill" : ""}`}>
      <div className="dt-toolbar">
        <div className="dt-toolbar-main">{toolbarLeft}</div>
        <div className="dt-toolbar-actions">
          {toolbarRight}
          {activeFilterCount > 0 && (
            <button className="btn-ghost" onClick={(e) => { e.stopPropagation(); setFilters({}); }} title="清除所有列筛选">清筛 {activeFilterCount}</button>
          )}
          {!hideExport && <button className="btn-ghost" onClick={exportCsv}>导出</button>}
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
              <button className="dt-colreset" onClick={() => { setHidden(new Set(columns.filter((c) => c.defaultHidden).map((c) => c.key))); setWidths({}); setSort(null); setFilters({}); }}>重置视图</button>
            </div>
          )}
        </div>
      </div>

      {batchBar}

      {server && <div className={`dt-loadbar${server.loading ? " on" : ""}`} aria-hidden />}

      <div className={`dt-scroll${server?.loading ? " dt-busy" : ""}`}>
        <table className="table dt-table">
          <thead>
            <tr>
              {selectable && (
                <th className="cell-check dt-sticky" style={{ left: 0 }}>
                  <input type="checkbox" checked={Boolean(allChecked)}
                    ref={(el) => { if (el) el.indeterminate = Boolean(someChecked && !allChecked); }}
                    onChange={onToggleAll} aria-label={allChecked ? "取消全选本页" : "全选本页"} title="全选/取消本页" />
                </th>
              )}
              {visibleCols.map((c, i) => {
                const sticky = stickyFirst && i === 0;
                const left = sticky ? stickyOffset : undefined;
                const fActive = (filters[c.key]?.size ?? 0) > 0;
                return (
                  <th
                    key={c.key}
                    className={`${c.align === "right" ? "num" : ""} ${sticky ? "dt-sticky" : ""}`}
                    style={{ width: colWidth(c), minWidth: colWidth(c), left }}
                  >
                    {(() => {
                      const sortable = server ? Boolean(c.sortField) : Boolean(c.sortValue);
                      const activeDir = server
                        ? (c.sortField && server.serverSort?.field === c.sortField ? server.serverSort.dir : null)
                        : (sort?.key === c.key ? sort.dir : null);
                      const onSortClick = () => {
                        if (server) { if (c.sortField) server.onServerSort(c.sortField); }
                        else if (c.sortValue) cycleSort(c.key);
                      };
                      return (
                        <span className="dt-th">
                          <span className={sortable ? "dt-sortable" : ""} onClick={onSortClick} title={sortable ? "点击排序" : undefined}>
                            {c.header}
                            {activeDir ? <span className="dt-sortic">{activeDir === "asc" ? "▲" : "▼"}</span>
                              : sortable ? <span className="dt-sortic dt-sortic-idle" aria-hidden>↕</span> : null}
                          </span>
                          {!server && c.filterable && (
                            <button
                              className={`dt-filter-btn${fActive ? " on" : ""}`}
                              title="按此列筛选"
                              onClick={(e) => { e.stopPropagation(); setOpenFilter((k) => (k === c.key ? null : c.key)); setFilterSearch(""); }}
                            >
                              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4"><path d="M3 5h18l-7 8v5l-4 2v-7z" /></svg>
                            </button>
                          )}
                        </span>
                      );
                    })()}
                    {!server && c.filterable && openFilter === c.key && (
                      <div className="dt-filter-pop" onClick={(e) => e.stopPropagation()}>
                        <input className="search" autoFocus placeholder="搜索取值…" value={filterSearch} onChange={(e) => setFilterSearch(e.target.value)} style={{ width: "100%", marginBottom: 6 }} />
                        <div className="dt-filter-list">
                          {distinctValues(c).filter((v) => !filterSearch || v.toLowerCase().includes(filterSearch.toLowerCase())).map((v) => (
                            <label key={v} className="dt-colitem">
                              <input type="checkbox" checked={filters[c.key]?.has(v) ?? false} onChange={() => toggleFilterValue(c.key, v)} />
                              {v || <span className="muted">（空）</span>}
                            </label>
                          ))}
                        </div>
                        <div className="dt-filter-foot">
                          <button className="linkish small" onClick={() => clearFilter(c.key)}>清空</button>
                          <button className="btn-ghost small" onClick={() => setOpenFilter(null)}>完成</button>
                        </div>
                      </div>
                    )}
                    <span className="dt-resizer" onMouseDown={(e) => onResizeStart(e, c.key, colWidth(c))} />
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {/* 首屏加载：渲染骨架行而非单格「加载中…」，与 StateView 骨架屏体验一致 */}
            {displayRows.length === 0 && server?.loading && (
              Array.from({ length: 8 }).map((_, ri) => (
                <tr key={`sk${ri}`} className="dt-skrow">
                  {selectable && <td className="cell-check dt-sticky" style={{ left: 0 }}><span className="skeleton" style={{ height: 12, width: 12, borderRadius: 3 }} /></td>}
                  {visibleCols.map((c, ci) => (
                    <td key={c.key} className={stickyFirst && ci === 0 ? "dt-sticky" : ""} style={{ left: stickyFirst && ci === 0 ? stickyOffset : undefined }}>
                      <span className="skeleton" style={{ height: 12, width: `${55 + ((ri + ci) % 4) * 10}%`, display: "block", borderRadius: 4 }} />
                    </td>
                  ))}
                </tr>
              ))
            )}
            {displayRows.length === 0 && !server?.loading && (
              <tr>
                <td className="dt-empty" colSpan={visibleCols.length + (selectable ? 1 : 0)}>
                  {emptyState ?? "暂无匹配记录"}
                </td>
              </tr>
            )}
            {displayRows.map((r, idx) => {
              const id = rowKey(r);
              const isSel = selected?.has(id);
              const expanded = expandedKey != null && expandedKey === id;
              const colSpan = visibleCols.length + (selectable ? 1 : 0);
              return (
                <Fragment key={id}>
                <tr
                  className={`${rowClassName?.(r) ?? ""} ${isSel ? "row-sel" : ""}${onRowClick ? " dt-clickable" : ""}`}
                  onContextMenu={(rowMenu || onRowContextMenu) ? (e) => openRowMenu(e, r) : undefined}
                  onDoubleClick={onRowDoubleClick ? () => onRowDoubleClick(r) : undefined}
                  onClick={onRowClick ? () => onRowClick(r) : undefined}
                >
                  {selectable && (
                    <td className="cell-check dt-sticky" style={{ left: 0 }} onClick={(e) => { e.stopPropagation(); handleCheck(e, idx, id); }}>
                      <input type="checkbox" checked={Boolean(isSel)} readOnly tabIndex={-1} />
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

      {server && (() => {
        const pageCount = Math.max(1, Math.ceil(server.total / server.pageSize));
        const from = server.total === 0 ? 0 : (server.page - 1) * server.pageSize + 1;
        const to = Math.min(server.page * server.pageSize, server.total);
        return (
          <div className="dt-pager">
            <span className="muted small">共 <b>{server.total.toLocaleString()}</b> 条 · 第 {from}–{to} 条{server.loading ? " · 加载中…" : ""}</span>
            {server.onPageSizeChange && (
              <label className="muted small dt-pager-size">每页
                <select value={server.pageSize} onChange={(e) => server.onPageSizeChange!(Number(e.target.value))}>
                  {(server.pageSizeOptions ?? [20, 50, 100]).map((n) => <option key={n} value={n}>{n}</option>)}
                </select>
              </label>
            )}
            <div style={{ flex: 1 }} />
            <div className="dt-pager-btns">
              <button className="btn-ghost small" disabled={server.page <= 1} onClick={() => server.onPageChange(1)}>« 首页</button>
              <button className="btn-ghost small" disabled={server.page <= 1} onClick={() => server.onPageChange(server.page - 1)}>‹ 上一页</button>
              <span className="dt-pager-cur">{server.page} / {pageCount}</span>
              <button className="btn-ghost small" disabled={server.page >= pageCount} onClick={() => server.onPageChange(server.page + 1)}>下一页 ›</button>
              <button className="btn-ghost small" disabled={server.page >= pageCount} onClick={() => server.onPageChange(pageCount)}>末页 »</button>
              {pageCount > 5 && (
                <form className="dt-pager-jump" onSubmit={(e) => { e.preventDefault(); const v = Number(new FormData(e.currentTarget).get("p")); if (v >= 1 && v <= pageCount) server.onPageChange(v); }}>
                  <input name="p" inputMode="numeric" placeholder="跳页" aria-label="跳转到指定页" />
                </form>
              )}
            </div>
          </div>
        );
      })()}

      {/* 客户端模式底部计数条，与服务端分页页脚视觉一致（长表滚到底也知道总量） */}
      {!server && displayRows.length > 0 && (
        <div className="dt-pager">
          <span className="muted small">共 <b>{displayRows.length.toLocaleString()}</b> 条{activeFilterCount > 0 ? ` · 已按 ${activeFilterCount} 列筛选（原 ${rows.length.toLocaleString()} 条）` : ""}</span>
        </div>
      )}

      {ctxMenu && (
        <ul className="ctx-menu" style={{ top: ctxMenu.y, left: ctxMenu.x }} onClick={(e) => e.stopPropagation()}>
          {ctxMenu.items.map((it, i) => (
            <li key={i} className={`${it.danger ? "danger" : ""}${it.disabled ? " disabled" : ""}`} onClick={() => { if (!it.disabled) { it.onClick(); setCtxMenu(null); } }}>{it.label}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
