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
  const pageSize = opts.pageSize ?? 50;
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
    loading: q.isFetching,
  };

  return {
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
