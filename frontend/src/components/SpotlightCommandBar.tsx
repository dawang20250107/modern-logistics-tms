import { useEffect, useState, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { apiPost } from "../api/client";
import { toast } from "../api/toast";

interface ToolCall {
  tool_name: string;
  args: any;
  risk_detected?: boolean;
}

interface Suggestion {
  suggestion_id: string;
  title: string;
  body: string;
}

interface AgentResponse {
  answer: string;
  tool_calls: ToolCall[];
  suggestions: Suggestion[];
}

export function SpotlightCommandBar() {
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [response, setResponse] = useState<AgentResponse | null>(null);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);

  // 快捷菜单
  const navCommands = [
    { label: "订单录入", path: "/intake", cmd: "/intake" },
    { label: "调度台", path: "/dispatch-board", cmd: "/dispatch" },
    { label: "车辆监控", path: "/monitor", cmd: "/monitoring" },
    { label: "运营总览", path: "/", cmd: "/dashboard" },
  ];

  const filteredCommands = navCommands.filter((c) =>
    c.cmd.includes(query.toLowerCase()) || c.label.includes(query)
  );

  // 每次查询变动，重置选中项
  useEffect(() => {
    setSelectedIndex(0);
  }, [query]);

  // 1. 全局监听快捷键与上下箭头、回车
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // 呼出指令舱
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setIsOpen((prev) => !prev);
        return;
      }

      if (!isOpen) return;

      // 退出
      if (e.key === "Escape") {
        setIsOpen(false);
        return;
      }

      // 下箭头
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedIndex((prev) => {
          const count = filteredCommands.length;
          return count > 0 ? (prev + 1) % count : 0;
        });
      }

      // 上箭头
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedIndex((prev) => {
          const count = filteredCommands.length;
          return count > 0 ? (prev - 1 + count) % count : 0;
        });
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, filteredCommands.length]);

  // 2. 聚焦输入框
  useEffect(() => {
    if (isOpen) {
      setQuery("");
      setResponse(null);
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen]);

  if (!isOpen) return null;

  const handleCommandSelect = (path: string) => {
    navigate(path);
    setIsOpen(false);
  };

  const handleSearchSubmit = async (e?: React.FormEvent) => {
    if (e) e.preventDefault();
    const cleanQuery = query.trim();

    // 1. 如果选中了某个列表指令，直接执行跳转
    if (filteredCommands.length > 0 && selectedIndex < filteredCommands.length && selectedIndex >= 0) {
      handleCommandSelect(filteredCommands[selectedIndex].path);
      return;
    }

    if (!cleanQuery) return;

    // 2. 否则调用后端 LangGraph ReAct 智能体进行业务对话与拼单分析
    setLoading(true);
    setResponse(null);
    try {
      const data = await apiPost<AgentResponse>("/agent/chat", { message: cleanQuery });
      setResponse(data);
      toast.success("分析已完成");
    } catch (err: any) {
      toast.error("分析失败，请稍后重试");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{
        position: "fixed", top: 0, left: 0, right: 0, bottom: 0,
        background: "rgba(0, 0, 0, 0.55)", backdropFilter: "blur(8px)",
        zIndex: 999999, display: "flex", justifyContent: "center", paddingTop: "12vh",
        transition: "all 0.25s ease"
      }}
      onClick={() => setIsOpen(false)}
    >
      <div
        style={{
          width: "90%", maxWidth: 640, background: "rgba(30, 30, 30, 0.85)",
          color: "#fff", border: "1px solid rgba(255, 255, 255, 0.12)",
          borderRadius: 14, boxShadow: "0 24px 50px rgba(0,0,0,0.5)",
          backdropFilter: "blur(20px)", display: "flex", flexDirection: "column",
          overflow: "hidden", maxHeight: "70vh"
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* 输入框 */}
        <form onSubmit={handleSearchSubmit} style={{ display: "flex", borderBottom: "1px solid rgba(255, 255, 255, 0.12)", padding: 14 }}>
          <span style={{ fontSize: 20, marginRight: 10, display: "flex", alignItems: "center" }}></span>
          <input
            ref={inputRef}
            type="text"
            style={{
              flex: 1, background: "transparent", border: "none", color: "#fff",
              outline: "none", fontSize: 16, padding: "4px 0",
              fontFamily: "var(--font-sans)"
            }}
            placeholder="搜索或输入指令，↑↓ 选择，Enter 确认"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          {query && <button type="button" onClick={() => setQuery("")} style={{ background: "transparent", border: "none", color: "rgba(255,255,255,0.4)", cursor: "pointer", fontSize: 18 }}>×</button>}
        </form>

        {/* 指令与结果框滚动区 */}
        <div style={{ overflowY: "auto", flex: 1, padding: 10, display: "flex", flexDirection: "column", gap: 8 }}>
          {loading && (
            <div style={{ padding: "30px", textAlign: "center", color: "rgba(255,255,255,0.6)", fontSize: 14 }} className="stack">
              <span>分析中…</span>
              
            </div>
          )}

          {/* 1. 展现 AI 的分析应答结果 */}
          {response && (
            <div style={{ padding: 14, background: "rgba(255,255,255,0.04)", borderRadius: 8, border: "1px solid rgba(255,255,255,0.06)", display: "flex", flexDirection: "column", gap: 10 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12, color: "var(--primary)" }}>
                <span>分析结果</span>
                
              </div>
              <p style={{ fontSize: 14, lineHeight: 1.6, margin: 0, whiteSpace: "pre-wrap", color: "rgba(255,255,255,0.9)" }}>{response.answer}</p>
              
              {/* 调用的工具明细 */}
              {response.tool_calls.length > 0 && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 4 }}>
                  {response.tool_calls.map((t, idx) => (
                    <span key={idx} style={{ fontSize: 10, background: "rgba(255,255,255,0.08)", color: "rgba(255,255,255,0.7)", padding: "2px 8px", borderRadius: 4, border: "1px solid rgba(255,255,255,0.04)" }}>
                      {t.tool_name}
                    </span>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* 2. 快捷指令导航列表 */}
          {!loading && !response && (
            <div className="stack" style={{ gap: 4 }}>
              <div style={{ padding: "6px 8px", fontSize: 11, color: "rgba(255,255,255,0.35)", fontWeight: "bold" }}>快捷导航</div>
              {filteredCommands.map((c, idx) => {
                const active = selectedIndex === idx;
                return (
                  <div
                    key={c.cmd}
                    style={{
                      padding: "10px 12px", borderRadius: 8, cursor: "pointer",
                      display: "flex", justifyContent: "space-between", alignItems: "center",
                      transition: "all 0.15s ease", fontSize: 13,
                      borderLeft: active ? "3px solid var(--primary)" : "3px solid transparent",
                      background: active ? "rgba(255,255,255,0.08)" : "rgba(255,255,255,0.02)",
                    }}
                    onMouseEnter={() => setSelectedIndex(idx)}
                    onClick={() => handleCommandSelect(c.path)}
                  >
                    <span style={{ fontWeight: active ? "bold" : "normal" }}>{c.label}</span>
                    <span style={{ fontSize: 11, color: "rgba(255,255,255,0.3)", background: "rgba(255,255,255,0.06)", padding: "2px 6px", borderRadius: 4, fontFamily: "monospace" }}>{c.cmd}</span>
                  </div>
                );
              })}
              {filteredCommands.length === 0 && query.startsWith("/") && (
                <div style={{ padding: 12, textAlign: "center", fontSize: 12, color: "rgba(255,255,255,0.4)" }}>无匹配的快捷导航指令</div>
              )}
            </div>
          )}

          {/* 3. 散落问题说明 */}
          {!loading && !response && !query.startsWith("/") && query.trim() && (
            <div
              style={{
                padding: "12px", borderRadius: 8, cursor: "pointer",
                display: "flex", justifyContent: "space-between", alignItems: "center",
                background: "rgba(99, 102, 241, 0.14)", border: "1px dashed rgba(99, 102, 241, 0.3)",
                fontSize: 13
              }}
              onClick={() => handleSearchSubmit()}
            >
              <span style={{ color: "#a5b4fc", display: "flex", alignItems: "center", gap: 6 }}>
                搜索：“{query}”
              </span>
              <span style={{ fontSize: 11, color: "rgba(255,255,255,0.5)" }}>按 Enter 发送 ↵</span>
            </div>
          )}
        </div>

        {/* 底部指示 */}
        <div style={{ padding: "10px 14px", borderTop: "1px solid rgba(255, 255, 255, 0.08)", fontSize: 11, color: "rgba(255,255,255,0.3)", display: "flex", justifyContent: "space-between" }}>
          <span>按 <kbd style={{ background: "rgba(255,255,255,0.08)", padding: "2px 4px", borderRadius: 4, fontFamily: "monospace" }}>ESC</kbd> 退出</span>
          <span><kbd style={{ background: "rgba(255,255,255,0.08)", padding: "2px 4px", borderRadius: 4, fontFamily: "monospace" }}>Ctrl+K</kbd> 呼出/隐藏</span>
        </div>
      </div>
    </div>
  );
}