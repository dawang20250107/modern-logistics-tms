import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet } from "../api/client";
import type { AuditLog, Paginated } from "../api/types";

export function AuditPage() {
  const [resource, setResource] = useState("");
  const [search, setSearch] = useState("");

  const params = new URLSearchParams({ page_size: "100", ordering: "-created_at" });
  if (resource) params.set("resource_type", resource);
  if (search) params.set("search", search);

  const logs = useQuery({
    queryKey: ["audit", resource, search],
    queryFn: () => apiGet<Paginated<AuditLog>>(`/audit-logs?${params.toString()}`),
  });
  const items = logs.data?.items ?? [];

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">审计日志 · 操作溯源</div>
        <div className="form-row">
          <input className="search" placeholder="搜索动作/路径/资源/RequestID" value={search} onChange={(e) => setSearch(e.target.value)} />
          <select value={resource} onChange={(e) => setResource(e.target.value)}>
            <option value="">全部资源</option>
            <option value="waybill">运单</option>
            <option value="order">订单</option>
            <option value="user">用户</option>
          </select>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">记录（{logs.data?.total ?? 0}）</div>
        {logs.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : logs.isError ? (
          <div className="muted" style={{ padding: 16 }}>无权限或加载失败（仅管理员可查）。</div>
        ) : items.length === 0 ? (
          <div className="muted" style={{ padding: 16 }}>暂无日志</div>
        ) : (
          <table className="table">
            <thead>
              <tr><th>时间</th><th>操作人</th><th>动作</th><th>资源</th><th>方法</th><th>状态</th><th>路径</th></tr>
            </thead>
            <tbody>
              {items.map((l) => (
                <tr key={l.id}>
                  <td className="small">{new Date(l.created_at).toLocaleString()}</td>
                  <td>{l.actor_name || "-"}</td>
                  <td className="mono small">{l.action}</td>
                  <td className="small">{l.resource_type}{l.resource_id ? `:${l.resource_id}` : ""}</td>
                  <td className="small">{l.method}</td>
                  <td>
                    <span className={`tag tag-${l.status_code && l.status_code >= 400 ? "high" : "low"}`}>{l.status_code ?? "-"}</span>
                  </td>
                  <td className="mono small" style={{ maxWidth: 280, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{l.path}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
