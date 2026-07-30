import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { fmtNum0 } from "../api/format";

// Excel 级数据表格：
// 列显隐 / 列宽拖拽 / 双击自适宽 / 列拖拽重排 / 列固定(pin) / 固定表头
// 单列+Shift 多列排序 / 列内字段筛选 / 命名视图 / 密度切换 / 全屏 / 行号
// 单元格选区(点选/拖选/Shift 扩展/键盘导航) / Ctrl+C 复制 TSV / 选区统计(求和/均值/计数)
// 批量勾选 + Shift 范围选 / 行右键菜单 / 行展开 / CSV + Excel 导出 / 服务端分页
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
  sticky?: "left" | "right"; // 声明式固定：left 并入默认 pin，right 常驻右侧（操作列）
  collapseUniform?: false;   // 关掉「整页同值自动折叠」（该列即使全同值也要占位）
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
type Density = "compact" | "standard" | "relaxed";
interface ViewState {
  hidden: string[];
  widths: Record<string, number>;
  sort: SortState | null;      // 旧版单列排序（向后兼容读取）
  sorts?: SortState[];         // 多列排序（Shift+点击追加）
  order?: string[];            // 列顺序（拖拽重排）
  pinned?: string[];           // 固定列 key（含 stickyFirst 迁移）
  density?: Density;
  rowNums?: boolean;
  totals?: boolean;          // 合计行（当页数字列求和）
}

function loadView(viewKey: string): ViewState | null {
  try {
    const raw = localStorage.getItem(`dt.view.${viewKey}`);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}
function loadNamedViews(viewKey: string): Record<string, ViewState> {
  try {
    return JSON.parse(localStorage.getItem(`dt.namedviews.${viewKey}`) ?? "{}");
  } catch {
    return {};
  }
}

const DENSITY_NEXT: Record<Density, Density> = { compact: "standard", standard: "relaxed", relaxed: "compact" };
const DENSITY_LABEL: Record<Density, string> = { compact: "紧凑", standard: "标准", relaxed: "宽松" };

// 数字解析（求和/均值/Excel 数字单元格）：容忍 ¥、千分位、单位后缀
function parseNum(v: string | number): number | null {
  if (typeof v === "number") return Number.isFinite(v) ? v : null;
  const s = String(v).replace(/[¥,￥\s]/g, "").replace(/(吨|方|件|公里|%|元)$/u, "");
  if (s === "" || !/^-?\d+(\.\d+)?$/.test(s)) return null;
  return Number(s);
}
// 选区统计沿用全站同一套千分位规则，不另起一套
const fmtStat = (n: number) => fmtNum0(n, 0) === String(n) ? fmtNum0(n) : n.toLocaleString("zh-CN", { maximumFractionDigits: 2 });

// 中文列排序此前用裸 `<` 比较，那是 UTF-16 码点序：点「客户」升序，第一名是
// 「宁德时代」（宁 U+5B81）而不是「比亚迪」（比 U+6BD4）——按 Unicode 编号排，
// 对看表的人没有任何意义。中文必须走 localeCompare 的拼音序。
//
// numeric:true 顺带修掉另一处：单号 DD10 在纯字典序里排在 DD9 前面，
// 开了自然数序才是 DD9 → DD10。
//
// 服务端排序的列不受影响（Postgres 有自己的 collation），这里只管客户端排序。
const collator = new Intl.Collator("zh-CN", { numeric: true, sensitivity: "base" });

function compareValues(a: string | number, b: string | number): number {
  if (typeof a === "number" && typeof b === "number") {
    return a === b ? 0 : a < b ? -1 : 1;
  }
  return collator.compare(String(a ?? ""), String(b ?? ""));
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
  toolbarRight?: React.ReactNode;
  batchBar?: React.ReactNode;
  exportName?: string;
  rowMenu?: (row: T) => RowMenuItem[];
  hideExport?: boolean;
  emptyState?: React.ReactNode;
  server?: ServerPage;
  fill?: boolean;
}) {
  const saved = useMemo(() => loadView(viewKey), [viewKey]);
  const [hidden, setHidden] = useState<Set<string>>(
    () => new Set(saved?.hidden ?? columns.filter((c) => c.defaultHidden).map((c) => c.key)),
  );
  const [widths, setWidths] = useState<Record<string, number>>(saved?.widths ?? {});
  const [sorts, setSorts] = useState<SortState[]>(() => saved?.sorts ?? (saved?.sort ? [saved.sort] : []));
  const [order, setOrder] = useState<string[]>(saved?.order ?? []);
  const [pinned, setPinned] = useState<Set<string>>(() => new Set(saved?.pinned ?? []));
  // 默认紧凑：这是一天看八小时的台账，不是偶尔瞥一眼的小组件。
  // 32px 行高比 40px 一屏多出 6 行，而字号不变（见 .dt-den-* 说明）。
  const [density, setDensity] = useState<Density>(saved?.density ?? "compact");
  const [rowNums, setRowNums] = useState<boolean>(saved?.rowNums ?? false);
  const [totals, setTotals] = useState<boolean>(saved?.totals ?? false);
  // 用户手动展开的单值列（不持久化：数据一变，该不该折叠也就变了）
  const [unfolded, setUnfolded] = useState<Set<string>>(new Set());
  const [fullscreen, setFullscreen] = useState(false);
  const [colMenu, setColMenu] = useState(false);
  const [filters, setFilters] = useState<Record<string, Set<string>>>({});
  const [openFilter, setOpenFilter] = useState<string | null>(null);
  const [filterSearch, setFilterSearch] = useState("");
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; items: RowMenuItem[] } | null>(null);
  const [namedViews, setNamedViews] = useState<Record<string, ViewState>>(() => loadNamedViews(viewKey));
  const [viewName, setViewName] = useState("");
  const anchorRef = useRef<number>(-1);

  // ── 单元格选区（Excel 语义）──
  // r/c 为 displayRows 行号 × visibleCols 列号；anchor=起点，focus=活动单元格
  const [selA, setSelA] = useState<{ r: number; c: number } | null>(null);
  const [selF, setSelF] = useState<{ r: number; c: number } | null>(null);
  // 选区矩形用 {r,c} 是对的——它本来就是一个视觉范围，复制与统计要的就是坐标。
  // 但"光标这一行是哪条记录"必须单独锚 id：行级动作（Enter/Space）按 id 找回，
  // 否则轮询刷新插进来一条，动作就落到错的行上（见 test/cursor-identity.test.ts）。
  const [cursorRowId, setCursorRowId] = useState<string | null>(null);
  const draggingRef = useRef(false);
  const movedRef = useRef(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    try {
      localStorage.setItem(`dt.view.${viewKey}`, JSON.stringify({
        hidden: [...hidden], widths, sort: sorts[0] ?? null, sorts,
        order, pinned: [...pinned], density, rowNums, totals,
      } satisfies ViewState));
    } catch { /* ignore */ }
  }, [viewKey, hidden, widths, sorts, order, pinned, density, rowNums, totals]);

  useEffect(() => {
    const close = () => { setColMenu(false); setOpenFilter(null); setCtxMenu(null); };
    window.addEventListener("click", close);
    const up = () => { draggingRef.current = false; };
    window.addEventListener("mouseup", up);
    return () => { window.removeEventListener("click", close); window.removeEventListener("mouseup", up); };
  }, []);

  // 全屏时锁定页面滚动，Esc 退出（键盘处理见 onGridKeyDown）
  useEffect(() => {
    if (!fullscreen) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => { document.body.style.overflow = prev; };
  }, [fullscreen]);

  // 列顺序：已保存顺序优先，新列自动接尾
  const orderedCols = useMemo(() => {
    if (order.length === 0) return columns;
    const byKey = new Map(columns.map((c) => [c.key, c]));
    const out: DataColumn<T>[] = [];
    for (const k of order) { const c = byKey.get(k); if (c) { out.push(c); byKey.delete(k); } }
    for (const c of columns) if (byKey.has(c.key)) out.push(c);
    return out;
  }, [columns, order]);

  const shownCols = orderedCols.filter((c) => !hidden.has(c.key));

  const filterText = (c: DataColumn<T>, r: T): string =>
    String((c.filterValue ? c.filterValue(r) : c.exportValue ? c.exportValue(r) : c.sortValue ? c.sortValue(r) : "") ?? "");
  const cellValue = (c: DataColumn<T>, r: T): string | number =>
    (c.exportValue ? c.exportValue(r) : c.sortValue ? c.sortValue(r) : "") ?? "";

  const filteredRows = useMemo(() => {
    const active = Object.entries(filters).filter(([, v]) => v && v.size > 0);
    if (active.length === 0) return rows;
    return rows.filter((r) => active.every(([key, set]) => {
      const col = columns.find((c) => c.key === key);
      return col ? set.has(filterText(col, r)) : true;
    }));
  }, [rows, filters, columns]);

  // 多列排序：依次比较（Shift+点击追加次级排序键）
  const sortedRows = useMemo(() => {
    if (sorts.length === 0) return filteredRows;
    const chain = sorts
      .map((s) => ({ s, col: columns.find((c) => c.key === s.key) }))
      .filter((x) => x.col?.sortValue);
    if (chain.length === 0) return filteredRows;
    return [...filteredRows].sort((a, b) => {
      for (const { s, col } of chain) {
        const va = col!.sortValue!(a), vb = col!.sortValue!(b);
        const d = compareValues(va, vb);
        if (d !== 0) return s.dir === "asc" ? d : -d;
      }
      return 0;
    });
  }, [filteredRows, sorts, columns]);

  const displayRows = server ? rows : sortedRows;

  // 单值列折叠：整页只有一个取值的列不占横向空间，取值收进工具栏的标注 chip。
  // 运营台上「优先级」整列"普通"、「业务」整列"整车"是常态，三列这样的就吃掉
  // 近 300px——而横向空间正是密集账本最缺的东西。信息没丢：chip 上写着值，
  // 点一下就能把列放回来。
  const uniformCols = useMemo(() => {
    const m = new Map<string, string>();
    if (displayRows.length < 3) return m;
    for (const c of shownCols) {
      if (c.alwaysVisible || c.sticky || c.collapseUniform === false) continue;
      const seen = new Set<string>();
      for (const r of displayRows) {
        seen.add(String(cellValue(c, r) ?? ""));
        if (seen.size > 1) break;
      }
      const only = seen.size === 1 ? [...seen][0] : "";
      if (only) m.set(c.key, only);
    }
    return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [displayRows, shownCols]);

  const visibleCols = shownCols.filter((c) => !uniformCols.has(c.key) || unfolded.has(c.key));

  const cycleSort = (key: string, additive: boolean) => {
    setSorts((prev) => {
      const idx = prev.findIndex((s) => s.key === key);
      if (!additive) {
        if (idx === -1) return [{ key, dir: "asc" }];
        if (prev[idx].dir === "asc") return [{ key, dir: "desc" }];
        return [];
      }
      const next = [...prev];
      if (idx === -1) next.push({ key, dir: "asc" });
      else if (next[idx].dir === "asc") next[idx] = { key, dir: "desc" };
      else next.splice(idx, 1);
      return next;
    });
  };

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

  // 列宽拖拽 + 双击自适宽
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
  // Excel 双击列边界自适应：取该列可见单元格内容最大宽度
  const autoFit = (key: string) => {
    const root = scrollRef.current;
    if (!root) return;
    const ci = visibleCols.findIndex((c) => c.key === key);
    if (ci === -1) return;
    const nth = ci + 1 + (selectable ? 1 : 0) + (rowNums ? 1 : 0);
    let maxW = 0;
    root.querySelectorAll<HTMLElement>(`tr > *:nth-child(${nth})`).forEach((cell) => {
      const probe = document.createElement("span");
      probe.style.cssText = "position:absolute;visibility:hidden;white-space:nowrap;font:inherit";
      probe.textContent = cell.innerText;
      cell.appendChild(probe);
      maxW = Math.max(maxW, probe.offsetWidth);
      probe.remove();
    });
    const col = columns.find((c) => c.key === key);
    setWidths((prev) => ({ ...prev, [key]: Math.max(col?.minWidth ?? 60, Math.min(480, maxW + 28)) }));
  };

  // 列拖拽重排（HTML5 DnD，表头把手）
  const dragColRef = useRef<string | null>(null);
  const [dragOverCol, setDragOverCol] = useState<string | null>(null);
  const onColDrop = (targetKey: string) => {
    const src = dragColRef.current;
    dragColRef.current = null;
    setDragOverCol(null);
    if (!src || src === targetKey) return;
    const keys = orderedCols.map((c) => c.key);
    const from = keys.indexOf(src), to = keys.indexOf(targetKey);
    if (from === -1 || to === -1) return;
    keys.splice(to, 0, ...keys.splice(from, 1));
    setOrder(keys);
  };

  const colWidth = (c: DataColumn<T>) => widths[c.key] ?? c.width ?? 140;


  // ── 固定列偏移：勾选列 + 行号列 + 依序累计的 pin 列 ──
  const checkW = selectable ? 34 : 0;
  const numW = rowNums ? 46 : 0;
  const pinnedKeys = useMemo(() => {
    const set = new Set(pinned);
    if (pinned.size === 0) {
      // 未手工 pin 时的声明式默认：stickyFirst 首列 + 列定义里的 sticky:"left"
      if (stickyFirst && visibleCols[0]) set.add(visibleCols[0].key);
      for (const c of visibleCols) if (c.sticky === "left") set.add(c.key);
    }
    return set;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pinned, stickyFirst, visibleCols.map((c) => c.key).join(",")]);
  // 已知偏差（实测，暂不修）：这里累加的是**声明**宽度，而 table-layout:fixed
  // + width:100% 在表格装得下时会把富余按比例分给所有列。
  // 实测勾选列渲染宽：1366 → 34（偏差 0）、1680 → 37.2（3.2）、2560 → 59.2（25.2），
  // 而第二个固定列的 left 一直写着 34px。
  //
  // 不修的理由：这个偏差**看不见**，因为两个条件互斥——
  // 列被拉伸 ⇔ 表格装得下 ⇔ 从不横滚 ⇔ 固定列从不真正贴边生效。
  // 试过让一列吸收富余（其余列严格按声明宽渲染），偏移确实全准了，
  // 但代价是那一列在 1366 下塌成 0px、在 2560 下涨到 1132px —— 拿一个可见的坏
  // 换一个不可见的坏，不划算。真要修得改成按**实测**宽度累加（ResizeObserver），
  // 等固定列在宽视口下真的会生效时再说。
  const pinLeft = useMemo(() => {
    const m = new Map<string, number>();
    let acc = checkW + numW;
    for (const c of visibleCols) {
      if (pinnedKeys.has(c.key)) { m.set(c.key, acc); acc += colWidth(c); }
    }
    return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visibleCols, pinnedKeys, widths, checkW, numW]);
  const isPinned = (key: string) => pinLeft.has(key);
  // 右固定（操作列）：从最右列向左累计 right 偏移
  const pinRight = useMemo(() => {
    const m = new Map<string, number>();
    let acc = 0;
    for (let i = visibleCols.length - 1; i >= 0; i--) {
      const c = visibleCols[i];
      if (c.sticky === "right") { m.set(c.key, acc); acc += colWidth(c); }
    }
    return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visibleCols, widths]);
  const isPinnedR = (key: string) => pinRight.has(key);


  const exportRows = () => displayRows.map((r) => visibleCols.map((c) => cellValue(c, r)));

  const exportCsv = () => {
    const esc = (v: string | number) => `"${String(v ?? "").replace(/"/g, '""')}"`;
    const head = visibleCols.map((c) => esc(c.header)).join(",");
    const body = exportRows().map((cells) => cells.map(esc).join(","));
    download("﻿" + [head, ...body].join("\r\n"), `${exportName ?? viewKey}.csv`, "text/csv;charset=utf-8");
  };
  // Excel 导出：SpreadsheetML（数字列落 Number 类型，Excel 打开即可求和）
  const exportXls = () => {
    const xmlEsc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
    const cell = (v: string | number) => {
      const n = parseNum(v);
      return n !== null
        ? `<Cell><Data ss:Type="Number">${n}</Data></Cell>`
        : `<Cell><Data ss:Type="String">${xmlEsc(String(v ?? ""))}</Data></Cell>`;
    };
    const head = `<Row>${visibleCols.map((c) => `<Cell><Data ss:Type="String">${xmlEsc(c.header)}</Data></Cell>`).join("")}</Row>`;
    const body = exportRows().map((cells) => `<Row>${cells.map(cell).join("")}</Row>`).join("");
    const xml = `<?xml version="1.0"?><?mso-application progid="Excel.Sheet"?>
<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet">
<Worksheet ss:Name="${xmlEsc(exportName ?? viewKey)}"><Table>${head}${body}</Table></Worksheet></Workbook>`;
    download(xml, `${exportName ?? viewKey}.xls`, "application/vnd.ms-excel");
  };
  const download = (content: string, name: string, mime: string) => {
    const url = URL.createObjectURL(new Blob([content], { type: mime }));
    const a = document.createElement("a");
    a.href = url;
    a.download = name;
    a.click();
    URL.revokeObjectURL(url);
  };

  const allChecked = selectable && rows.length > 0 && selected && rows.every((r) => selected.has(rowKey(r)));
  const someChecked = selectable && selected && rows.some((r) => selected.has(rowKey(r)));
  const activeFilterCount = Object.values(filters).filter((s) => s && s.size > 0).length;

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

  // ── 选区工具 ──
  const selRange = useMemo(() => {
    if (!selA || !selF) return null;
    return {
      r1: Math.min(selA.r, selF.r), r2: Math.max(selA.r, selF.r),
      c1: Math.min(selA.c, selF.c), c2: Math.max(selA.c, selF.c),
    };
  }, [selA, selF]);
  const inSel = (r: number, c: number) =>
    !!selRange && r >= selRange.r1 && r <= selRange.r2 && c >= selRange.c1 && c <= selRange.c2;

  const selStats = useMemo(() => {
    if (!selRange) return null;
    const cells = (selRange.r2 - selRange.r1 + 1) * (selRange.c2 - selRange.c1 + 1);
    if (cells < 2) return null;
    let count = 0, numCount = 0, sum = 0;
    for (let r = selRange.r1; r <= selRange.r2 && r < displayRows.length; r++) {
      for (let c = selRange.c1; c <= selRange.c2 && c < visibleCols.length; c++) {
        count++;
        const n = parseNum(cellValue(visibleCols[c], displayRows[r]));
        if (n !== null) { numCount++; sum += n; }
      }
    }
    return { count, numCount, sum, avg: numCount ? sum / numCount : 0 };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selRange, displayRows, visibleCols]);

  const copySelection = () => {
    if (!selRange) return;
    const lines: string[] = [];
    for (let r = selRange.r1; r <= selRange.r2 && r < displayRows.length; r++) {
      const cells: string[] = [];
      for (let c = selRange.c1; c <= selRange.c2 && c < visibleCols.length; c++) {
        cells.push(String(cellValue(visibleCols[c], displayRows[r]) ?? ""));
      }
      lines.push(cells.join("\t"));
    }
    void navigator.clipboard?.writeText(lines.join("\n"));
  };

  const startCellSel = (e: React.MouseEvent, r: number, c: number) => {
    if ((e.target as HTMLElement).closest("a,button,input,select,textarea,label")) return;
    if (e.button !== 0) return;
    movedRef.current = false;
    draggingRef.current = true;
    if (e.shiftKey && selA) setSelF({ r, c });
    else { setSelA({ r, c }); setSelF({ r, c }); }
    if (displayRows[r]) setCursorRowId(rowKey(displayRows[r]));
  };
  const dragCellSel = (r: number, c: number) => {
    if (!draggingRef.current) return;
    setSelF((prev) => {
      if (prev && prev.r === r && prev.c === c) return prev;
      movedRef.current = true;
      return { r, c };
    });
  };

  // 网格键盘：方向键/Home/End/PageUp/Down 导航，Shift 扩展，Ctrl+C 复制，Ctrl+A 全选，Esc 清除/退全屏
  const onGridKeyDown = (e: React.KeyboardEvent) => {
    const tag = (e.target as HTMLElement).tagName;
    if (tag === "INPUT" || tag === "SELECT" || tag === "TEXTAREA") return;
    if (e.key === "Escape") {
      if (fullscreen) { setFullscreen(false); return; }
      setSelA(null); setSelF(null);
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "c") { copySelection(); e.preventDefault(); return; }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "a") {
      if (displayRows.length) {
        setSelA({ r: 0, c: 0 });
        setSelF({ r: displayRows.length - 1, c: visibleCols.length - 1 });
      }
      e.preventDefault();
      return;
    }
    const maxR = displayRows.length - 1, maxC = visibleCols.length - 1;

    // 行级动作（Enter 打开 / Space 勾选）必须按**记录 id** 找回那一行，
    // 不能直接取 displayRows[selF.r]。运单页 30s、调度池 15s 都在轮询，
    // 光标停在第 4 行时刷新插进来一条，第 4 行已经是另一条记录了。
    // 找不到（那条记录离开了列表）就什么都不做——宁可这一下没反应，
    // 也不能对错的行动手。
    const cursorRow = cursorRowId ? displayRows.find((row) => rowKey(row) === cursorRowId) : null;
    if (e.key === "Enter" && cursorRow) {
      e.preventDefault();
      if (onRowClick) onRowClick(cursorRow);
      else if (onRowDoubleClick) onRowDoubleClick(cursorRow);
      return;
    }
    if (e.key === " " && cursorRow && selectable && onToggle) {
      // 表格内的 Space 归勾选，不再滚页——Excel 与 Gmail 都是这个约定
      e.preventDefault();
      onToggle(rowKey(cursorRow));
      return;
    }

    // 没有光标时按方向键 → 从左上角起步。
    // 原来这里是 `if (!selF) return`：Tab 进表格再按 ↓ 毫无反应，
    // 必须先用鼠标点一个单元格。一个号称键盘优先的台面，键盘通路却只能靠鼠标进入。
    if (!selF) {
      if (!["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown"].includes(e.key)) return;
      if (!displayRows.length) return;
      e.preventDefault();
      setSelA({ r: 0, c: 0 });
      setSelF({ r: 0, c: 0 });
      setCursorRowId(rowKey(displayRows[0]));
      return;
    }
    let { r, c } = selF;
    switch (e.key) {
      case "ArrowUp": r = Math.max(0, r - 1); break;
      case "ArrowDown": r = Math.min(maxR, r + 1); break;
      case "ArrowLeft": c = Math.max(0, c - 1); break;
      case "ArrowRight": c = Math.min(maxC, c + 1); break;
      case "Home": c = 0; if (e.ctrlKey) r = 0; break;
      case "End": c = maxC; if (e.ctrlKey) r = maxR; break;
      case "PageUp": r = Math.max(0, r - 12); break;
      case "PageDown": r = Math.min(maxR, r + 12); break;
      default: return;
    }
    e.preventDefault();
    setSelF({ r, c });
    if (displayRows[r]) setCursorRowId(rowKey(displayRows[r]));
    if (!e.shiftKey) setSelA({ r, c });
    // 活动单元格滚动可见
    scrollRef.current?.querySelector(`[data-cell="${r}-${c}"]`)?.scrollIntoView({ block: "nearest", inline: "nearest" });
  };

  const applyView = (v: ViewState) => {
    setHidden(new Set(v.hidden));
    setWidths(v.widths ?? {});
    setSorts(v.sorts ?? (v.sort ? [v.sort] : []));
    setOrder(v.order ?? []);
    setPinned(new Set(v.pinned ?? []));
    setDensity(v.density ?? "standard");
    setRowNums(v.rowNums ?? false);
    setTotals(v.totals ?? false);
  };
  const saveNamedView = () => {
    const name = viewName.trim();
    if (!name) return;
    const next = {
      ...namedViews,
      [name]: { hidden: [...hidden], widths, sort: sorts[0] ?? null, sorts, order, pinned: [...pinned], density, rowNums, totals } satisfies ViewState,
    };
    setNamedViews(next);
    setViewName("");
    try { localStorage.setItem(`dt.namedviews.${viewKey}`, JSON.stringify(next)); } catch { /* ignore */ }
  };
  const deleteNamedView = (name: string) => {
    const next = { ...namedViews };
    delete next[name];
    setNamedViews(next);
    try { localStorage.setItem(`dt.namedviews.${viewKey}`, JSON.stringify(next)); } catch { /* ignore */ }
  };

  const rowNumBase = server ? (server.page - 1) * server.pageSize : 0;
  const stickyTd = (key: string): React.CSSProperties | undefined =>
    isPinned(key) ? { left: pinLeft.get(key) } : isPinnedR(key) ? { right: pinRight.get(key) } : undefined;
  const stickyCls = (key: string) => (isPinned(key) ? "dt-fixed" : isPinnedR(key) ? "dt-fixed dt-fixed-r" : "");

  // 单元格选区的事件委托。
  //
  // 这三个处理器原先挂在每一个 <td> 上：100 行 × 13 列 = 1300 个单元格，
  // 每次重渲染就要新建 3900 个箭头函数，React 再逐个 diff。实测 100 行时
  // 点一次排序要 500ms —— 而 500ms 的响应已经是明显的卡顿。
  //
  // 委托到 tbody 只需 3 个处理器，坐标从 data-cell 属性读。这个属性本来就有
  // （键盘导航靠它定位单元格），基础设施是现成的。
  const cellCoord = (e: React.MouseEvent): { r: number; c: number } | null => {
    const td = (e.target as HTMLElement).closest?.("[data-cell]");
    if (!td) return null;
    const [r, c] = (td.getAttribute("data-cell") ?? "").split("-").map(Number);
    return Number.isFinite(r) && Number.isFinite(c) ? { r, c } : null;
  };
  const onBodyMouseDown = (e: React.MouseEvent) => {
    const p = cellCoord(e);
    if (p) startCellSel(e, p.r, p.c);
  };
  const onBodyMouseOver = (e: React.MouseEvent) => {
    if (!draggingRef.current) return; // 非拖拽状态直接返回，不做 closest 查询
    const p = cellCoord(e);
    if (p) dragCellSel(p.r, p.c);
  };
  const onBodyClickCapture = (e: React.MouseEvent) => {
    if (movedRef.current) {
      e.stopPropagation();
      movedRef.current = false;
    }
  };

  return (
    <div className={`dt dt-den-${density}${fill ? " dt-fill" : ""}${fullscreen ? " dt-fs" : ""}`}>
      {/* 批量条**替换**工具条，而不是插在它下面。
          原来是两条并存：勾选一行，整张表向下跳 55px（实测首行 y 从 171 → 226）。
          一天勾几百次，每次都丢一遍视线位置——这是坐标恒定原则最贵的一处违反。
          同位同高换内容之后，勾选只改这条的底色和内容，下面一个像素都不动。
          选中期间工具条上的筛选被遮住是有意的：拿着一批选中项去改筛选，
          会让选区底下的行集合变掉，本来就该拦住。 */}
      {batchBar ? (
        <div className="dt-toolbar dt-toolbar-batch">{batchBar}</div>
      ) : (
      <div className="dt-toolbar">
        <div className="dt-toolbar-main">{toolbarLeft}</div>
        <div className="dt-toolbar-actions">
          {[...uniformCols].filter(([k]) => !unfolded.has(k)).map(([k, v]) => {
            const col = shownCols.find((c) => c.key === k);
            return (
              <button
                key={k}
                className="dt-uniform"
                title={`本页「${col?.header}」全部为「${v}」，已折叠该列以腾出横向空间。点击展开`}
                onClick={(e) => { e.stopPropagation(); setUnfolded((s) => new Set(s).add(k)); }}
              >
                <span className="dt-uniform-k">{col?.header}</span>
                <span className="dt-uniform-v">{v}</span>
              </button>
            );
          })}
          {toolbarRight}
          {activeFilterCount > 0 && (
            <button className="btn-ghost" onClick={(e) => { e.stopPropagation(); setFilters({}); }} title="清除所有列筛选">清筛 {activeFilterCount}</button>
          )}
          {!hideExport && <button className="btn-ghost" onClick={exportCsv} title="导出 CSV（当前可见列）">CSV</button>}
          <button className="btn-ghost" onClick={exportXls} title="导出 Excel（数字列可直接求和）">Excel</button>
          <button className="btn-ghost" onClick={() => setDensity(DENSITY_NEXT[density])} title={`行密度：${DENSITY_LABEL[density]}（点击切换）`}>密度</button>
          <button className="btn-ghost" onClick={() => setFullscreen((v) => !v)} title={fullscreen ? "退出全屏 (Esc)" : "全屏表格"}>{fullscreen ? "退出全屏" : "全屏"}</button>
          <button className="btn-ghost" onClick={(e) => { e.stopPropagation(); setColMenu((v) => !v); }}>列 / 视图</button>
          {colMenu && (
            <div className="dt-colmenu" onClick={(e) => e.stopPropagation()}>
              <div className="muted small" style={{ padding: "2px 8px 6px" }}>显示列（拖表头可重排 / 📌 固定）</div>
              <div className="dt-colmenu-list">
                {orderedCols.map((c) => (
                  <label key={c.key} className="dt-colitem">
                    <input
                      type="checkbox"
                      checked={!hidden.has(c.key)}
                      disabled={c.alwaysVisible}
                      onChange={() => setHidden((h) => { const n = new Set(h); if (n.has(c.key)) n.delete(c.key); else n.add(c.key); return n; })}
                    />
                    <span style={{ flex: 1 }}>{c.header}</span>
                    <button
                      className={`dt-pinbtn${pinned.has(c.key) ? " on" : ""}`}
                      title={pinned.has(c.key) ? "取消固定" : "固定到左侧"}
                      onClick={(e) => { e.preventDefault(); setPinned((p) => { const n = new Set(p); if (n.has(c.key)) n.delete(c.key); else n.add(c.key); return n; }); }}
                    >📌</button>
                  </label>
                ))}
              </div>
              <label className="dt-colitem" style={{ borderTop: "1px solid var(--line)", marginTop: 4, paddingTop: 6 }}>
                <input type="checkbox" checked={rowNums} onChange={() => setRowNums((v) => !v)} />
                显示行号
              </label>
              <label className="dt-colitem">
                <input type="checkbox" checked={totals} onChange={() => setTotals((v) => !v)} />
                Σ 合计行（当页数字列求和）
              </label>
              <div className="context-divider" />
              <div className="muted small" style={{ padding: "2px 8px 4px" }}>命名视图</div>
              {Object.keys(namedViews).map((name) => (
                <div key={name} className="dt-viewitem">
                  <button className="linkish small" style={{ flex: 1, textAlign: "left" }} onClick={() => applyView(namedViews[name])}>{name}</button>
                  <button className="dt-viewdel" title="删除视图" onClick={() => deleteNamedView(name)}>×</button>
                </div>
              ))}
              <div className="dt-viewsave">
                <input className="search" placeholder="视图名…" value={viewName} onChange={(e) => setViewName(e.target.value)}
                  onKeyDown={(e) => { if (e.key === "Enter") saveNamedView(); }} />
                <button className="btn-ghost small" disabled={!viewName.trim()} onClick={saveNamedView}>保存</button>
              </div>
              <div className="context-divider" />
              <button className="dt-colreset" onClick={() => { setHidden(new Set(columns.filter((c) => c.defaultHidden).map((c) => c.key))); setWidths({}); setSorts([]); setFilters({}); setOrder([]); setPinned(new Set()); setDensity("standard"); setRowNums(false); setTotals(false); }}>重置视图</button>
            </div>
          )}
        </div>
      </div>
      )}

      {server && <div className={`dt-loadbar${server.loading ? " on" : ""}`} aria-hidden />}

      <div ref={scrollRef} className={`dt-scroll${server?.loading ? " dt-busy" : ""}`} tabIndex={0} onKeyDown={onGridKeyDown}>
        <table className="table dt-table">
          <thead>
            <tr>
              {selectable && (
                <th className="cell-check dt-fixed" style={{ left: 0 }}>
                  <input type="checkbox" checked={Boolean(allChecked)}
                    ref={(el) => { if (el) el.indeterminate = Boolean(someChecked && !allChecked); }}
                    onChange={onToggleAll} aria-label={allChecked ? "取消全选本页" : "全选本页"} title="全选/取消本页" />
                </th>
              )}
              {rowNums && <th className="dt-rownum dt-fixed" style={{ left: checkW }} aria-label="行号">#</th>}
              {visibleCols.map((c) => {
                const fActive = (filters[c.key]?.size ?? 0) > 0;
                const sIdx = sorts.findIndex((s) => s.key === c.key);
                return (
                  <th
                    key={c.key}
                    className={`${c.align === "right" ? "num" : ""} ${stickyCls(c.key)} ${dragOverCol === c.key ? "dt-dragover" : ""}`}
                    style={{ width: colWidth(c), minWidth: colWidth(c), ...(stickyTd(c.key) ?? {}) }}
                    draggable
                    onDragStart={(e) => { dragColRef.current = c.key; e.dataTransfer.effectAllowed = "move"; }}
                    onDragOver={(e) => { e.preventDefault(); if (dragColRef.current && dragColRef.current !== c.key) setDragOverCol(c.key); }}
                    onDragLeave={() => setDragOverCol((k) => (k === c.key ? null : k))}
                    onDrop={(e) => { e.preventDefault(); onColDrop(c.key); }}
                    onDragEnd={() => { dragColRef.current = null; setDragOverCol(null); }}
                  >
                    {(() => {
                      const sortable = server ? Boolean(c.sortField) : Boolean(c.sortValue);
                      const activeDir = server
                        ? (c.sortField && server.serverSort?.field === c.sortField ? server.serverSort.dir : null)
                        : (sIdx >= 0 ? sorts[sIdx].dir : null);
                      const onSortClick = (e: React.MouseEvent) => {
                        if (server) { if (c.sortField) server.onServerSort(c.sortField); }
                        else if (c.sortValue) cycleSort(c.key, e.shiftKey);
                      };
                      return (
                        <span className="dt-th">
                          <span className={sortable ? "dt-sortable" : ""} onClick={onSortClick} title={sortable ? (server ? "点击排序" : "点击排序 · Shift+点击多列排序") : undefined}>
                            {c.header}
                            {activeDir ? (
                              <span className="dt-sortic">
                                {activeDir === "asc" ? "▲" : "▼"}
                                {!server && sorts.length > 1 && sIdx >= 0 && <sup className="dt-sortord">{sIdx + 1}</sup>}
                              </span>
                            ) : sortable ? <span className="dt-sortic dt-sortic-idle" aria-hidden>↕</span> : null}
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
                    <span className="dt-resizer" onMouseDown={(e) => onResizeStart(e, c.key, colWidth(c))} onDoubleClick={(e) => { e.stopPropagation(); autoFit(c.key); }} title="拖拽调宽 · 双击自适应" />
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody onMouseDown={onBodyMouseDown} onMouseOver={onBodyMouseOver} onClickCapture={onBodyClickCapture}>
            {displayRows.length === 0 && server?.loading && (
              Array.from({ length: 8 }).map((_, ri) => (
                <tr key={`sk${ri}`} className="dt-skrow">
                  {selectable && <td className="cell-check dt-fixed" style={{ left: 0 }}><span className="skeleton" style={{ height: 12, width: 12, borderRadius: 3 }} /></td>}
                  {rowNums && <td className="dt-rownum dt-fixed" style={{ left: checkW }} />}
                  {visibleCols.map((c, ci) => (
                    <td key={c.key} className={stickyCls(c.key)} style={stickyTd(c.key)}>
                      <span className="skeleton" style={{ height: 12, width: `${55 + ((ri + ci) % 4) * 10}%`, display: "block", borderRadius: 4 }} />
                    </td>
                  ))}
                </tr>
              ))
            )}
            {displayRows.length === 0 && !server?.loading && (
              <tr>
                <td className="dt-empty" colSpan={visibleCols.length + (selectable ? 1 : 0) + (rowNums ? 1 : 0)}>
                  {emptyState ?? "暂无匹配记录"}
                </td>
              </tr>
            )}
            {displayRows.map((r, idx) => {
              const id = rowKey(r);
              const isSel = selected?.has(id);
              const expanded = expandedKey != null && expandedKey === id;
              const colSpan = visibleCols.length + (selectable ? 1 : 0) + (rowNums ? 1 : 0);
              return (
                <Fragment key={id}>
                <tr
                  className={`${rowClassName?.(r) ?? ""} ${isSel ? "row-sel" : ""}${onRowClick ? " dt-clickable" : ""}`}
                  onContextMenu={(rowMenu || onRowContextMenu) ? (e) => openRowMenu(e, r) : undefined}
                  onDoubleClick={onRowDoubleClick ? () => onRowDoubleClick(r) : undefined}
                  onClick={onRowClick ? () => onRowClick(r) : undefined}
                >
                  {selectable && (
                    <td className="cell-check dt-fixed" style={{ left: 0 }} onClick={(e) => { e.stopPropagation(); handleCheck(e, idx, id); }}>
                      <input type="checkbox" checked={Boolean(isSel)} readOnly tabIndex={-1} />
                    </td>
                  )}
                  {rowNums && <td className="dt-rownum dt-fixed" style={{ left: checkW }}>{rowNumBase + idx + 1}</td>}
                  {visibleCols.map((c, ci) => {
                    const selCls = inSel(idx, ci) ? " dt-cellsel" : "";
                    const focusCls = selF && selF.r === idx && selF.c === ci ? " dt-cellfocus" : "";
                    return (
                      <td
                        key={c.key}
                        data-cell={`${idx}-${ci}`}
                        className={`${c.align === "right" ? "num" : ""} ${stickyCls(c.key)}${selCls}${focusCls}`}
                        style={stickyTd(c.key)}
                      >
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
          {totals && displayRows.length > 0 && (
            <tfoot>
              <tr className="dt-totals">
                {selectable && <td className="cell-check dt-fixed" style={{ left: 0 }} />}
                {rowNums && <td className="dt-rownum dt-fixed" style={{ left: checkW }}>Σ</td>}
                {visibleCols.map((c, ci) => {
                  let sum = 0, hit = 0;
                  for (const r of displayRows) {
                    const n = parseNum(cellValue(c, r));
                    if (n !== null) { sum += n; hit++; }
                  }
                  const show = hit > 0 && hit >= displayRows.length / 2; // 该列过半可解析为数字才视为数字列
                  return (
                    <td key={c.key} className={`${c.align === "right" ? "num" : ""} ${stickyCls(c.key)}`} style={stickyTd(c.key)} title={show ? `当页合计（${hit} 行）` : undefined}>
                      {ci === 0 && !show ? <span className="muted small">当页合计</span> : show ? <b>{fmtStat(sum)}</b> : ""}
                    </td>
                  );
                })}
              </tr>
            </tfoot>
          )}
        </table>
      </div>

      {server && (() => {
        const pageCount = Math.max(1, Math.ceil(server.total / server.pageSize));
        const from = server.total === 0 ? 0 : (server.page - 1) * server.pageSize + 1;
        const to = Math.min(server.page * server.pageSize, server.total);
        return (
          <div className="dt-pager">
            <span className="muted small">共 <b>{fmtNum0(server.total)}</b> 条 · 第 {from}–{to} 条{server.loading ? " · 加载中…" : ""}</span>
            {server.onPageSizeChange && (
              <label className="muted small dt-pager-size">每页
                <select value={server.pageSize} onChange={(e) => server.onPageSizeChange!(Number(e.target.value))}>
                  {(server.pageSizeOptions ?? [20, 50, 100]).map((n) => <option key={n} value={n}>{n}</option>)}
                </select>
              </label>
            )}
            {selStats && (
              <span className="dt-selstats" title="选区统计（Ctrl+C 复制选区）">
                计数 {selStats.count}{selStats.numCount > 0 && <> · 求和 <b>{fmtStat(selStats.sum)}</b> · 均值 {fmtStat(selStats.avg)}</>}
              </span>
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

      {!server && displayRows.length > 0 && (
        <div className="dt-pager">
          <span className="muted small">共 <b>{fmtNum0(displayRows.length)}</b> 条{activeFilterCount > 0 ? ` · 已按 ${activeFilterCount} 列筛选（原 ${fmtNum0(rows.length)} 条）` : ""}</span>
          {selStats && (
            <span className="dt-selstats" title="选区统计（Ctrl+C 复制选区）">
              计数 {selStats.count}{selStats.numCount > 0 && <> · 求和 <b>{fmtStat(selStats.sum)}</b> · 均值 {fmtStat(selStats.avg)}</>}
            </span>
          )}
        </div>
      )}

      {ctxMenu && (
        <ul className="ctx-menu" style={{ top: ctxMenu.y, left: ctxMenu.x }} onClick={(e) => e.stopPropagation()}>
          {selRange && (
            <li onClick={() => { copySelection(); setCtxMenu(null); }}>复制选区 (Ctrl+C)</li>
          )}
          {ctxMenu.items.map((it, i) => (
            <li key={i} className={`${it.danger ? "danger" : ""}${it.disabled ? " disabled" : ""}`} onClick={() => { if (!it.disabled) { it.onClick(); setCtxMenu(null); } }}>{it.label}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
