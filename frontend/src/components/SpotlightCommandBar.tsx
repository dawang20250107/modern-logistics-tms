import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState, useRef } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { apiGet, apiPost } from "../api/client";
import { toast } from "../api/toast";
import type { LookupResponse, LookupResult, ReplyCardData } from "../api/types";

interface ToolCall {
  tool_name: string;
  args: unknown;
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

type CmdKind = "action" | "nav";
interface Command {
  id: string;
  label: string;
  hint?: string;
  path: string;
  kind: CmdKind;
  section: string;
  keywords: string;
}

// 全局命令目录：快捷动作 + 全站导航（keywords 支持中文/拼音/英文模糊命中）
const COMMANDS: Command[] = [
  { id: "new-order", label: "新建订单", hint: "建单录入", path: "/intake", kind: "action", section: "快捷动作", keywords: "new order intake jiandan xindan luru 建单 录入 下单 开单" },
  { id: "act-dispatch", label: "去派单", hint: "调度指挥台", path: "/dispatch-board", kind: "action", section: "快捷动作", keywords: "dispatch paidan 派单 调度 指挥" },
  { id: "act-recon", label: "去核销", hint: "对账中心", path: "/reconciliation", kind: "action", section: "快捷动作", keywords: "settle hexiao duizhang 核销 对账 收付款" },
  { id: "nav-overview", label: "运输驾驶舱", path: "/", kind: "nav", section: "导航", keywords: "overview cockpit jiashicang zonglan shouye 驾驶舱 总览 首页 概览" },
  { id: "nav-cs", label: "客服工作台", path: "/intake", kind: "nav", section: "导航", keywords: "customer service kefu jiedan jiandan 客服 接单 建单 工作台" },
  { id: "nav-dispatch", label: "调度工作台", path: "/dispatch-board", kind: "nav", section: "导航", keywords: "dispatch diaodu paidan 调度 派单 工作台" },
  { id: "nav-waybills", label: "订单管理", path: "/waybills", kind: "nav", section: "导航", keywords: "waybill order dingdan yundan chadan 订单 运单 查单 台账" },
  { id: "nav-admin", label: "管理后台", path: "/admin", kind: "nav", section: "导航", keywords: "admin guanli houtai 管理 后台 设置" },
  { id: "nav-board", label: "经营指标", path: "/", kind: "nav", section: "导航", keywords: "board kanban jingying 看板 经营 报表 数据 驾驶舱 指标" },
  { id: "nav-fleet", label: "资源库", path: "/fleet", kind: "nav", section: "导航", keywords: "fleet resource ziyuan cheliang kehu 资源 车队 客户 司机 承运商" },
  { id: "nav-pricing", label: "计价规则", path: "/pricing", kind: "nav", section: "导航", keywords: "pricing jijia baojia 计价 报价 价格 规则" },
  { id: "nav-recon", label: "对账中心", path: "/reconciliation", kind: "nav", section: "导航", keywords: "reconciliation duizhang caiwu 对账 财务 结算 账单" },
  { id: "nav-org", label: "组织与权限", path: "/org", kind: "nav", section: "导航", keywords: "org zuzhi quanxian 组织 权限 员工 角色" },
  { id: "nav-audit", label: "审计日志", path: "/audit", kind: "nav", section: "导航", keywords: "audit shenji rizhi 审计 日志" },
];

const HIT_ICON: Record<string, string> = { waybill: "运", order: "订", customer: "客", carrier: "承", statement: "账" };

export function SpotlightCommandBar() {
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [response, setResponse] = useState<AgentResponse | null>(null);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);

  const { pathname } = useLocation();
  // 上下文加权：当前所在页相关命令优先展示
  const contextKeys: Record<string, string[]> = {
    "/dispatch-board": ["nav-dispatch", "new-order", "act-recon"],
    "/reconciliation": ["act-recon", "nav-recon", "nav-waybills"],
    "/waybills": ["nav-waybills", "new-order", "act-dispatch"],
    "/intake": ["new-order", "act-dispatch", "nav-waybills"],
  };
  const boost = contextKeys[pathname] ?? [];

  const q = query.trim().toLowerCase().replace(/^\//, "");
  const matched = useMemo(() => {
    const list = COMMANDS.filter((c) => !q || c.label.toLowerCase().includes(q) || c.keywords.toLowerCase().includes(q));
    if (q) return list;
    // 无查询时按上下文加权排序，本页相关命令置顶
    return [...list].sort((a, b) => {
      const ai = boost.indexOf(a.id), bi = boost.indexOf(b.id);
      return (ai === -1 ? 99 : ai) - (bi === -1 ? 99 : bi);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, pathname]);

  // 搜索即工作台：输入车牌/电话/单号/客户/承运商 → 精确答案卡 + 跨实体可跳转结果
  const lookup = query.trim();
  const lookupOn = lookup.length >= 2 && !lookup.startsWith("/") && !loading && !response;
  const lookupQ = useQuery({
    queryKey: ["cmdk-lookup", lookup],
    queryFn: () => apiGet<LookupResponse>(`/lookup?q=${encodeURIComponent(lookup)}`),
    enabled: lookupOn,
    staleTime: 10_000,
  });
  const answer = lookupOn ? lookupQ.data?.answer : undefined;
  const hits = lookupOn ? (lookupQ.data?.results ?? []) : [];
  const hasAnswer = Boolean(answer && answer.kind !== "none");

  const copyReply = async (waybillNo: string) => {
    try {
      const card = await apiGet<ReplyCardData>(`/waybills/${waybillNo}/reply-card`);
      await navigator.clipboard.writeText(card.copy_text);
      toast.success("已复制客户回复文案");
      setIsOpen(false);
    } catch {
      toast.error("复制失败");
    }
  };
  // 答案卡的可执行动作（可键盘选中）
  const answerActions: { label: string; run: () => void }[] = [];
  if (hasAnswer && answer) {
    for (const a of answer.actions ?? []) {
      if (a === "view_waybill" && answer.waybill_no) answerActions.push({ label: `查看运单 ${answer.waybill_no}`, run: () => { navigate(`/waybills/${answer.waybill_no}`); setIsOpen(false); } });
      if (a === "view_order" && answer.order_no) answerActions.push({ label: `查看订单 ${answer.order_no}`, run: () => { navigate(`/orders/${answer.order_no}`); setIsOpen(false); } });
      if (a === "call_driver" && answer.driver_phone) answerActions.push({ label: `联系司机 ${answer.driver_phone}`, run: () => { window.location.href = `tel:${answer.driver_phone}`; } });
      if (a === "copy_reply" && answer.waybill_no) answerActions.push({ label: "复制客户回复", run: () => copyReply(answer.waybill_no!) });
    }
  }

  const showAi = query.trim().length > 0 && !query.trim().startsWith("/");
  // 可选中的扁平结果：答案动作 + 命中记录（可跳转）+ 命令 + （可选）AI 分析行
  const results = useMemo(
    () => [
      ...answerActions.map((a) => ({ kind: "answer" as string, cmd: null, hit: null as LookupResult | null, act: a })),
      ...hits.map((h) => ({ kind: "hit" as string, cmd: null, hit: h, act: null })),
      ...matched.map((c) => ({ kind: c.kind as string, cmd: c, hit: null as LookupResult | null, act: null })),
      ...(showAi ? [{ kind: "ai" as string, cmd: null, hit: null as LookupResult | null, act: null }] : []),
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [matched, showAi, lookup, hasAnswer, answerActions.length, hits.length],
  );

  useEffect(() => {
    setSelectedIndex(0);
  }, [query]);

  const run = (idx: number) => {
    const item = results[idx];
    if (!item) return;
    if (item.kind === "ai") {
      handleSearchSubmit();
      return;
    }
    if (item.kind === "answer" && item.act) {
      item.act.run();
      return;
    }
    if (item.kind === "hit" && item.hit) {
      navigate(item.hit.path);
      setIsOpen(false);
      return;
    }
    if (item.cmd) {
      navigate(item.cmd.path);
      setIsOpen(false);
    }
  };

  // 全局快捷键 + 方向键 + 回车
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setIsOpen((prev) => !prev);
        return;
      }
      if (!isOpen) return;
      if (e.key === "Escape") {
        setIsOpen(false);
        return;
      }
      // 焦点陷阱：命令面板仅单输入框驱动，Tab 不应把焦点带到背景
      if (e.key === "Tab") {
        e.preventDefault();
        inputRef.current?.focus();
        return;
      }
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedIndex((prev) => (results.length ? (prev + 1) % results.length : 0));
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedIndex((prev) => (results.length ? (prev - 1 + results.length) % results.length : 0));
      }
      if (e.key === "Enter") {
        e.preventDefault();
        run(selectedIndex);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, results, selectedIndex]);

  useEffect(() => {
    if (isOpen) {
      setQuery("");
      setResponse(null);
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen]);

  if (!isOpen) return null;

  const handleSearchSubmit = async (e?: React.FormEvent) => {
    if (e) e.preventDefault();
    const cleanQuery = query.trim();
    // 焦点落在某个命令/记录/动作上 → 直接执行（回车已走 run，此处兜底表单提交）
    const cur = results[selectedIndex];
    if (cur && cur.kind !== "ai") {
      run(selectedIndex);
      return;
    }
    if (!cleanQuery) return;
    // 交给后端 Agent 做业务对话/拼单分析
    setLoading(true);
    setResponse(null);
    try {
      const data = await apiPost<AgentResponse>("/agent/chat", { message: cleanQuery });
      setResponse(data);
      toast.success("分析已完成");
    } catch {
      toast.error("分析失败，请稍后重试");
    } finally {
      setLoading(false);
    }
  };

  let renderIdx = -1;
  let lastSection = "";

  return (
    <div className="cmdk-overlay" onClick={() => setIsOpen(false)}>
      <div className="cmdk-panel" onClick={(e) => e.stopPropagation()}>
        <form onSubmit={handleSearchSubmit} className="cmdk-input-row">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" style={{ opacity: 0.55 }}>
            <circle cx="11" cy="11" r="7" /><path d="M21 21l-4.3-4.3" />
          </svg>
          <input
            ref={inputRef}
            type="text"
            className="cmdk-input"
            aria-label="全局搜索与命令：搜索页面、执行动作，或向 AI 提问"
            placeholder="搜索 单号/客户/承运商/车牌/电话，或页面与动作 —— ↑↓ 选择，Enter 直达"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          {query && <button type="button" className="cmdk-clear" onClick={() => setQuery("")}>×</button>}
        </form>

        <div className="cmdk-body">
          {loading && <div className="cmdk-loading">分析中…</div>}

          {response && (
            <div className="cmdk-answer">
              <div className="cmdk-answer-head">AI 分析结果</div>
              <p>{response.answer}</p>
              {response.tool_calls.length > 0 && (
                <div className="cmdk-tools">
                  {response.tool_calls.map((t, idx) => (
                    <span key={idx} className="cmdk-tool">{t.tool_name}</span>
                  ))}
                </div>
              )}
            </div>
          )}

          {!loading && !response && (
            <div className="cmdk-list">
              {/* 富答案卡：搜索结果本身成为工作台 */}
              {hasAnswer && answer && (
                <div className="cmdk-answercard">
                  <div className="cmdk-answercard-title">{answer.title}</div>
                  <div className="cmdk-answercard-fields">
                    {(answer.fields ?? []).map((f, i) => (
                      <div key={i}><span>{f.label}</span><b>{f.value}</b></div>
                    ))}
                  </div>
                </div>
              )}
              {results.length === 0 && !hasAnswer && <div className="cmdk-empty">无匹配的命令</div>}
              {results.map((item) => {
                renderIdx += 1;
                const idx = renderIdx;
                const active = selectedIndex === idx;
                if (item.kind === "ai") {
                  return (
                    <div key="ai" className={`cmdk-item cmdk-ai${active ? " active" : ""}`} onMouseEnter={() => setSelectedIndex(idx)} onClick={() => run(idx)}>
                      <span>用 AI 分析：“{query.trim()}”</span>
                      <span className="cmdk-kbd">Enter ↵</span>
                    </div>
                  );
                }
                if (item.kind === "answer" && item.act) {
                  const header = lastSection !== "操作" ? "操作" : null;
                  lastSection = "操作";
                  return (
                    <div key={`act-${item.act.label}`}>
                      {header && <div className="cmdk-section">操作</div>}
                      <div className={`cmdk-item${active ? " active" : ""}`} onMouseEnter={() => setSelectedIndex(idx)} onClick={() => run(idx)}>
                        <span className="cmdk-item-main"><span className="cmdk-badge">直达</span><span className="cmdk-item-label">{item.act.label}</span></span>
                        <span className="cmdk-item-path">↵</span>
                      </div>
                    </div>
                  );
                }
                if (item.kind === "hit" && item.hit) {
                  const h = item.hit;
                  const header = lastSection !== "记录" ? "记录" : null;
                  lastSection = "记录";
                  return (
                    <div key={`hit-${h.kind}-${h.title}`}>
                      {header && <div className="cmdk-section">记录</div>}
                      <div className={`cmdk-item${active ? " active" : ""}`} onMouseEnter={() => setSelectedIndex(idx)} onClick={() => run(idx)}>
                        <span className="cmdk-item-main">
                          <span className="cmdk-badge cmdk-badge-hit">{HIT_ICON[h.kind] ?? "•"}</span>
                          <span className="cmdk-item-label">{h.title}</span>
                          <span className="cmdk-item-hint">{h.subtitle}</span>
                        </span>
                        <span className="cmdk-item-path">↵</span>
                      </div>
                    </div>
                  );
                }
                const c = item.cmd!;
                const header = c.section !== lastSection ? c.section : null;
                lastSection = c.section;
                return (
                  <div key={c.id}>
                    {header && <div className="cmdk-section">{header}</div>}
                    <div className={`cmdk-item${active ? " active" : ""}`} onMouseEnter={() => setSelectedIndex(idx)} onClick={() => run(idx)}>
                      <span className="cmdk-item-main">
                        {c.kind === "action" && <span className="cmdk-badge">动作</span>}
                        <span className="cmdk-item-label">{c.label}</span>
                        {c.hint && <span className="cmdk-item-hint">{c.hint}</span>}
                      </span>
                      <span className="cmdk-item-path">{c.path}</span>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <div className="cmdk-foot">
          <span><kbd>↑</kbd><kbd>↓</kbd> 选择 · <kbd>↵</kbd> 确认</span>
          <span><kbd>Esc</kbd> 退出 · <kbd>Ctrl</kbd>+<kbd>K</kbd> 呼出</span>
        </div>
      </div>
    </div>
  );
}
