import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import type { AgentSuggestion, Paginated } from "../api/types";

interface ToolDef {
  name: string;
  description: string;
}

export function AiWorkbenchPage() {
  const queryClient = useQueryClient();
  const [tool, setTool] = useState("");
  const [wbno, setWbno] = useState("");
  const [result, setResult] = useState("");

  const tools = useQuery({ queryKey: ["tools"], queryFn: () => apiGet<{ tools: ToolDef[] }>("/agent/tools") });
  const suggestions = useQuery({
    queryKey: ["suggestions"],
    queryFn: () => apiGet<Paginated<AgentSuggestion>>("/ai/suggestions?page_size=50"),
  });

  const run = useMutation({
    mutationFn: () =>
      apiPost<{ result: unknown }>("/agent/tools/execute", {
        tool_name: tool || tools.data?.tools[0]?.name,
        arguments: { waybill_no: wbno },
      }),
    onSuccess: (d) => {
      setResult(JSON.stringify(d.result, null, 2));
      queryClient.invalidateQueries({ queryKey: ["suggestions"] });
    },
    onError: (e) => setResult(String(e)),
  });

  const confirm = useMutation({
    mutationFn: (v: { id: string; status: string }) => apiPost(`/ai/suggestions/${v.id}/confirm`, { status: v.status }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["suggestions"] }),
  });

  const sugg = suggestions.data?.items ?? [];

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">Agent 工具执行</div>
        <div className="form-row">
          <select value={tool} onChange={(e) => setTool(e.target.value)}>
            {(tools.data?.tools ?? []).map((t) => (
              <option key={t.name} value={t.name}>{t.name}</option>
            ))}
          </select>
          <input placeholder="运单号，如 YD2606040010" value={wbno} onChange={(e) => setWbno(e.target.value)} style={{ flex: 1 }} />
          <button className="btn-primary" disabled={run.isPending || !wbno} onClick={() => run.mutate()}>
            执行
          </button>
        </div>
        {(tools.data?.tools ?? []).find((t) => t.name === (tool || tools.data?.tools[0]?.name)) && (
          <div className="muted small" style={{ padding: "0 18px 12px" }}>
            {(tools.data?.tools ?? []).find((t) => t.name === (tool || tools.data?.tools[0]?.name))?.description}
          </div>
        )}
        {result && <pre className="result-box">{result}</pre>}
      </div>

      <div className="panel">
        <div className="panel-head">建议中心（人工确认）</div>
        {suggestions.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>类型</th>
                <th>标题</th>
                <th>运单</th>
                <th>状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {sugg.map((s) => (
                <tr key={s.id}>
                  <td>{s.suggestion_type}</td>
                  <td>{s.title}</td>
                  <td className="mono">{s.waybill_no || "-"}</td>
                  <td>
                    <span className={`tag tag-${s.status === "accepted" ? "low" : s.status === "rejected" ? "none" : "medium"}`}>
                      {s.status}
                    </span>
                  </td>
                  <td className="row-actions">
                    {s.status === "pending" && (
                      <>
                        <button className="btn-ghost" onClick={() => confirm.mutate({ id: s.id, status: "accepted" })}>采纳</button>
                        <button className="btn-ghost" onClick={() => confirm.mutate({ id: s.id, status: "rejected" })}>驳回</button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
