import { useQuery } from "@tanstack/react-query";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { apiGet } from "../api/client";
import type { Paginated, Waybill } from "../api/types";

const RISK_LABEL: Record<string, string> = { high: "高", medium: "中", low: "低", none: "无" };

export function WaybillsPage() {
  const [filter, setFilter] = useState("");
  const query = useQuery({
    queryKey: ["waybills", "table"],
    queryFn: () => apiGet<Paginated<Waybill>>("/waybills?page_size=100"),
  });

  const columns = useMemo<ColumnDef<Waybill>[]>(
    () => [
      {
        accessorKey: "waybill_no",
        header: "运单号",
        cell: (ctx) => (
          <Link className="link mono" to={`/waybills/${ctx.getValue<string>()}`}>
            {ctx.getValue<string>()}
          </Link>
        ),
      },
      { accessorKey: "route_name", header: "线路" },
      { accessorKey: "status", header: "状态" },
      {
        accessorKey: "risk_level",
        header: "风险",
        cell: (ctx) => {
          const v = ctx.getValue<string>();
          return <span className={`tag tag-${v}`}>{RISK_LABEL[v] ?? v}</span>;
        },
      },
      { accessorKey: "eta_drift_minutes", header: "ETA偏移(分)" },
      { accessorKey: "receipt_status", header: "回单" },
      { accessorKey: "customer_name", header: "客户" },
      { accessorKey: "vehicle_plate", header: "车牌" },
    ],
    [],
  );

  const table = useReactTable({
    data: query.data?.items ?? [],
    columns,
    state: { globalFilter: filter },
    onGlobalFilterChange: setFilter,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
  });

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">
          运单台账
          <input
            className="search"
            placeholder="搜索运单号 / 线路 / 客户 / 车牌"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        {query.isLoading ? (
          <div className="muted">加载中…</div>
        ) : (
          <table className="table">
            <thead>
              {table.getHeaderGroups().map((hg) => (
                <tr key={hg.id}>
                  {hg.headers.map((h) => (
                    <th key={h.id}>
                      {flexRender(h.column.columnDef.header, h.getContext())}
                    </th>
                  ))}
                </tr>
              ))}
            </thead>
            <tbody>
              {table.getRowModel().rows.map((row) => (
                <tr key={row.id}>
                  {row.getVisibleCells().map((cell) => (
                    <td key={cell.id} className={cell.column.id === "waybill_no" ? "mono" : ""}>
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <div className="muted small">共 {query.data?.total ?? 0} 条</div>
      </div>
    </div>
  );
}
