import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet } from "../api/client";
import type { DataAsset } from "../api/types";

export function DataCatalogPage() {
  const [expanded, setExpanded] = useState<string>("");
  const cat = useQuery({
    queryKey: ["catalog"],
    queryFn: () => apiGet<{ assets: DataAsset[]; total_assets: number; domains: string[] }>("/analytics/catalog?counts=true"),
  });

  const assets = cat.data?.assets ?? [];
  const domains = cat.data?.domains ?? [];
  const totalRows = assets.reduce((s, a) => s + (a.row_count ?? 0), 0);

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">
          数据资产目录 · 数据治理
          <span className="ai-pill">{cat.data?.total_assets ?? 0} 张表</span>
        </div>
        <div className="kpi-row" style={{ padding: 16, gridTemplateColumns: "repeat(3, 1fr)" }}>
          <div className="kpi kpi-blue"><div className="kpi-value">{cat.data?.total_assets ?? 0}</div><div className="kpi-label">数据资产(表)</div></div>
          <div className="kpi"><div className="kpi-value">{domains.length}</div><div className="kpi-label">业务域</div></div>
          <div className="kpi kpi-amber"><div className="kpi-value">{totalRows.toLocaleString()}</div><div className="kpi-label">记录总数</div></div>
        </div>
      </div>

      {domains.map((d) => (
        <div key={d} className="panel">
          <div className="panel-head">{d}</div>
          <table className="table">
            <thead>
              <tr><th>资产</th><th>物理表</th><th>字段数</th><th>记录数</th><th></th></tr>
            </thead>
            <tbody>
              {assets.filter((a) => a.domain === d).map((a) => (
                <tr key={a.table} style={{ cursor: "pointer" }} onClick={() => setExpanded(expanded === a.table ? "" : a.table)}>
                  <td><b>{a.verbose_name}</b> <span className="muted small">{a.model}</span></td>
                  <td className="mono small">{a.table}</td>
                  <td>{a.field_count}</td>
                  <td>{a.row_count?.toLocaleString() ?? "-"}</td>
                  <td className="muted small">{expanded === a.table ? "收起" : "字段"}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {assets.filter((a) => a.domain === d && a.table === expanded).map((a) => (
            <div key={a.table} style={{ padding: "0 16px 14px" }}>
              <div className="muted small" style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                {a.fields.map((f) => (
                  <span key={f.name} className="tag tag-none" title={f.help}>{f.name}:{f.type}</span>
                ))}
              </div>
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}
