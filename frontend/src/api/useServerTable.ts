import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";

import type { ServerPage } from "../components/DataTable";
import type { FilterModel } from "../components/FilterBuilder";
import { apiGet } from "./client";
import type { Paginated } from "./types";

export interface ServerTableOptions {
  queryKey: unknown[];
  path: string; // 列表接口，如 "/orders"
  pageSize?: number;
  defaultSort?: { field: string; dir: "asc" | "desc" } | null;
  model?: FilterModel; // FilterBuilder 模型（服务端筛选）
  search?: string; // 服务端搜索关键字
  extraParams?: Record<string, string | number | undefined>;
  enabled?: boolean;
}

// 服务端分页/排序/筛选：把 FilterBuilder 模型 + 搜索 + 排序 + 页码翻成查询参数，
// 全量数据在后端过滤+分页，前端只渲染当前页。筛选/搜索变化时自动回到第 1 页。
export function useServerTable<T>(opts: ServerTableOptions) {
  const [pageSize, setPageSize] = useState(opts.pageSize ?? 50);
  const [page, setPage] = useState(1);
  const [sort, setSort] = useState<{ field: string; dir: "asc" | "desc" } | null>(opts.defaultSort ?? null);

  const filterParam = opts.model && opts.model.conditions.length > 0 ? JSON.stringify(opts.model) : "";
  const search = (opts.search ?? "").trim();
  const extraKey = JSON.stringify(opts.extraParams ?? {});

  // 筛选/搜索/附加参数变化时回到第 1 页（跳过首次挂载）
  const prevKey = useRef(`${filterParam}|${search}|${extraKey}`);
  useEffect(() => {
    const k = `${filterParam}|${search}|${extraKey}`;
    if (k !== prevKey.current) { prevKey.current = k; setPage(1); }
  }, [filterParam, search, extraKey]);

  const buildUrl = () => {
    const params = new URLSearchParams();
    params.set("page", String(page));
    params.set("page_size", String(pageSize));
    if (sort) params.set("ordering", (sort.dir === "desc" ? "-" : "") + sort.field);
    if (filterParam) params.set("filter", filterParam);
    if (search) params.set("search", search);
    for (const [k, v] of Object.entries(opts.extraParams ?? {})) {
      if (v != null && v !== "") params.set(k, String(v));
    }
    return `${opts.path}${opts.path.includes("?") ? "&" : "?"}${params.toString()}`;
  };

  const q = useQuery({
    queryKey: [...opts.queryKey, page, pageSize, sort, filterParam, search, extraKey],
    queryFn: () => apiGet<Paginated<T>>(buildUrl()),
    placeholderData: (prev) => prev, // 翻页/筛选时保留上一页，避免闪烁
    enabled: opts.enabled ?? true,
  });

  const toggleSort = (field: string) => {
    setSort((s) => {
      if (!s || s.field !== field) return { field, dir: "asc" };
      if (s.dir === "asc") return { field, dir: "desc" };
      return null;
    });
    setPage(1);
  };

  const server: ServerPage = {
    serverSort: sort,
    onServerSort: toggleSort,
    page,
    pageSize,
    total: q.data?.total ?? 0,
    onPageChange: setPage,
    onPageSizeChange: (n: number) => { setPageSize(n); setPage(1); },
    loading: q.isFetching,
  };

  // filterQuery 是「当前这批是怎么筛出来的」——search + filter + 排序，
  // 不含 page/page_size。导出要用它：界面上「导出」按钮就在搜索框旁边，
  // 用户的预期是导出他正看着的那批，而不是另一批毫不相干的数据。
  const filterQuery = () => {
    const params = new URLSearchParams();
    if (sort) params.set("ordering", (sort.dir === "desc" ? "-" : "") + sort.field);
    if (filterParam) params.set("filter", filterParam);
    if (search) params.set("search", search);
    for (const [k, v] of Object.entries(opts.extraParams ?? {})) {
      if (v != null && v !== "") params.set(k, String(v));
    }
    return params.toString();
  };

  const fetchAll = (max = EXPORT_MAX) => fetchAllPages<T>(opts.path, filterQuery(), max);

  return {
    filterQuery,
    fetchAll,
    rows: q.data?.items ?? [],
    total: q.data?.total ?? 0,
    pages: q.data?.pages ?? 1,
    isLoading: q.isLoading,
    isError: q.isError,
    refetch: q.refetch,
    page,
    setPage,
    sort,
    setSort,
    server,
  };
}

/** EXPORT_MAX 单次导出取回的行数上限，与后端 httpx.ExportMaxRows 对齐。 */
export const EXPORT_MAX = 50000;

/** PAGE_CAP 服务端对 page_size 的上限（clampInt(..., 1, 200)）。 */
const PAGE_CAP = 200;

/**
 * fetchAllPages 按筛选条件把**全部**行逐页取回来，供「导出」用。
 *
 * 服务端分页的表格，导出如果照当前页来，用户点一下拿到的是一页——
 * 表头齐、格式对、文件打得开，只是少了绝大部分。这套系统在调度台上
 * 栽过同一条（把 8336 说成 20）。
 *
 * 终止条件只能看 total 和「这一页是不是空的」。第一版写的是
 * 「拿回来的比要的少 = 最后一页」，页大小要了 500 而服务端夹到 200，
 * 于是第一页就停——实测 8367 条只导出 200 条，按钮上什么都不说。
 * 请求超过上限的页大小时，拿回来的一定"少于要求"，那个条件天然是错的。
 *
 * 单独放在 hook 外面是为了能被用例直接调到：把翻页逻辑在测试里再抄一遍，
 * 抄本和真身会各自演化，绿的就不再说明产品是对的。
 */
export async function fetchAllPages<T>(path: string, query: string, max = EXPORT_MAX): Promise<T[]> {
  const out: T[] = [];
  for (let p = 1; out.length < max; p++) {
    const params = new URLSearchParams(query);
    params.set("page", String(p));
    params.set("page_size", String(PAGE_CAP));
    const url = `${path}${path.includes("?") ? "&" : "?"}${params.toString()}`;
    const res = await apiGet<{ items: T[]; total: number }>(url);
    const items = res.items ?? [];
    if (items.length === 0) break;
    out.push(...items);
    if (out.length >= (res.total ?? out.length)) break;
  }
  return out.slice(0, max);
}
