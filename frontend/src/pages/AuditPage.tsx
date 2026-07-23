import { useState } from "react";

import { fmtDateTime } from "../api/format";
import type { AuditLog } from "../api/types";
import { auditActionLabel, resourceTypeLabel } from "../api/types";
import { useServerTable } from "../api/useServerTable";
import { DataTable, type DataColumn } from "../components/DataTable";
import { StateView } from "../components/StateView";

export function AuditPage() {
  const [resource, setResource] = useState("");
  const [search, setSearch] = useState("");

  const t = useServerTable<AuditLog>({
    queryKey: ["audit"],
    path: "/audit-logs",
    pageSize: 50,
    defaultSort: { field: "created_at", dir: "desc" },
    search,
    extraParams: { resource_type: resource || undefined },
  });

  const columns: DataColumn<AuditLog>[] = [
    { key: "created_at", header: "时间", width: 160, sortField: "created_at", exportValue: (l) => l.created_at, render: (l) => <span className="small">{fmtDateTime(l.created_at)}</span> },
    { key: "actor", header: "操作人", width: 120, exportValue: (l) => l.actor_name || "—", render: (l) => l.actor_name || <span className="muted">—</span> },
    { key: "action", header: "动作", width: 150, exportValue: (l) => auditActionLabel(l.action), render: (l) => <span className="small">{auditActionLabel(l.action)}</span> },
    { key: "resource", header: "资源", width: 130, exportValue: (l) => resourceTypeLabel(l.resource_type) + (l.resource_id ? `:${l.resource_id}` : ""), render: (l) => <span className="small">{resourceTypeLabel(l.resource_type)}{l.resource_id ? <span className="muted mono">:{l.resource_id.slice(0, 8)}</span> : ""}</span> },
    { key: "method", header: "请求方法", width: 90, exportValue: (l) => l.method, render: (l) => <span className="mono small" title="HTTP 请求方法">{l.method}</span> },
    { key: "status", header: "响应", width: 80, align: "right", sortField: "status_code", exportValue: (l) => l.status_code ?? "", render: (l) => <span className={`tag tag-${l.status_code && l.status_code >= 400 ? "high" : "low"}`} title="HTTP 响应状态码">{l.status_code ?? "—"}</span> },
    { key: "path", header: "接口路径", width: 300, exportValue: (l) => l.path, render: (l) => <span className="mono small" title={l.path} style={{ display: "block", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{l.path}</span> },
  ];

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">审计日志</div>
        <div className="form-row">
          <input className="search" placeholder="搜索操作人 / 路径 / 资源 / RequestID" value={search} onChange={(e) => setSearch(e.target.value)} />
          <select value={resource} onChange={(e) => setResource(e.target.value)}>
            <option value="">全部资源</option>
            <option value="waybill">运单</option>
            <option value="order">订单</option>
            <option value="user">用户</option>
          </select>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">操作记录（{t.total.toLocaleString()}）</div>
        {t.isError ? (
          <StateView kind="error" hint="无权限或加载失败（仅管理员可查）。" onRetry={() => t.refetch()} />
        ) : (
          <DataTable<AuditLog>
            viewKey="audit"
            columns={columns}
            rows={t.rows}
            rowKey={(l) => l.id}
            server={t.server}
            exportName="审计日志"
            emptyState={<StateView kind="empty" title="暂无日志" hint="尚无匹配的审计记录。" compact />}
          />
        )}
      </div>
    </div>
  );
}
