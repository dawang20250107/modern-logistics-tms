import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ApiError, apiGet, apiPost } from "../api/client";
import type { AgentSuggestion, Paginated } from "../api/types";

interface ToolDef {
  name: string;
  description: string;
}

interface AgentReply {
  thread_id: string;
  answer: string;
  tool_calls: Array<{ tool_name: string; summary: string }>;
  suggestions: unknown[];
}

interface ChatMsg {
  role: "user" | "assistant";
  text: string;
  tools?: string[];
}

export function AiWorkbenchPage() {
  const queryClient = useQueryClient();
  const [tool, setTool] = useState("");
  const [wbno, setWbno] = useState("");
  const [result, setResult] = useState("");

  const [thread, setThread] = useState("");
  const [input, setInput] = useState("");
  const [chat, setChat] = useState<ChatMsg[]>([]);

  const ask = useMutation({
    meta: { silent: true },
    mutationFn: (msg: string) => apiPost<AgentReply>("/agent/chat", { message: msg, thread_id: thread || undefined }),
    onSuccess: (d) => {
      setThread(d.thread_id);
      setChat((c) => [...c, { role: "assistant", text: d.answer, tools: d.tool_calls.map((t) => t.tool_name) }]);
      queryClient.invalidateQueries({ queryKey: ["suggestions"] });
    },
    onError: (e) => {
      const msg = e instanceof ApiError && e.code === "DEEPSEEK_NOT_CONFIGURED"
        ? "未配置 DEEPSEEK_API_KEY，AI 对话暂不可用（下方工具仍可直接执行）。"
        : `出错了：${String(e)}`;
      setChat((c) => [...c, { role: "assistant", text: msg }]);
    },
  });

  const send = () => {
    const m = input.trim();
    if (!m) return;
    setChat((c) => [...c, { role: "user", text: m }]);
    setInput("");
    ask.mutate(m);
  };

  const tools = useQuery({ queryKey: ["tools"], queryFn: () => apiGet<{ tools: ToolDef[] }>("/agent/tools") });
  const suggestions = useQuery({
    queryKey: ["suggestions"],
    queryFn: () => apiGet<Paginated<AgentSuggestion>>("/ai/suggestions?page_size=50"),
  });

  const run = useMutation({
    meta: { silent: true },
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
        <div className="panel-head">
          AI 智能助手
          <span className="ai-pill">LangGraph Agent</span>
        </div>
        <div style={{ padding: 16, maxHeight: 380, overflow: "auto", display: "flex", flexDirection: "column", gap: 10 }}>
          {chat.length === 0 ? (
            <div className="muted small">
              试着问：「运单 YD2606040010 有没有 ETA 风险？」「近30天准时率是多少？」「帮 WB001 生成调度建议」
            </div>
          ) : (
            chat.map((m, i) => (
              <div key={i} style={{ alignSelf: m.role === "user" ? "flex-end" : "flex-start", maxWidth: "80%" }}>
                <div style={{
                  padding: "9px 13px", borderRadius: 12,
                  background: m.role === "user" ? "var(--grad)" : "var(--panel-2)",
                  color: m.role === "user" ? "#fff" : "var(--ink)",
                  border: m.role === "user" ? "none" : "1px solid var(--line)",
                  whiteSpace: "pre-wrap", lineHeight: 1.6,
                }}>{m.text}</div>
                {m.tools && m.tools.length > 0 && (
                  <div style={{ marginTop: 4, display: "flex", gap: 6, flexWrap: "wrap" }}>
                    {m.tools.map((t, j) => <span key={j} className="tag tag-info">🔧 {t}</span>)}
                  </div>
                )}
              </div>
            ))
          )}
          {ask.isPending && <div className="muted small">思考中…</div>}
        </div>
        <div className="ai-box" style={{ borderTop: "1px solid var(--line)" }}>
          <input
            placeholder="问点什么…（多轮对话，自动调用系统工具）"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && send()}
          />
          <button className="btn-primary" disabled={ask.isPending || !input.trim()} onClick={send}>发送</button>
        </div>
      </div>

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
