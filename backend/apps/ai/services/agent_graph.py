"""LangGraph ReAct Agent：物流 TMS 智能助手编排。

拓扑：START → agent(LLM) ⇄ tools(ToolNode) → END
- agent 节点：DeepSeek（OpenAI 兼容）绑定业务工具，决定是否调用工具或给出最终答复。
- tools 节点：执行命中的业务工具（证据链 + AgentSuggestion 落库，人工确认闭环不变）。
- 状态持久化：Postgres checkpointer（按 thread_id 维护多轮对话与断点续跑）；
  未启用或无 PG 时回退内存 saver（本地/测试）。

模块级单例：编译一次复用，连接池长驻，契合高并发实时场景。
"""

import threading

from django.conf import settings
from langchain_core.messages import SystemMessage
from langgraph.checkpoint.memory import MemorySaver
from langgraph.graph import END, START, MessagesState, StateGraph
from langgraph.prebuilt import ToolNode, tools_condition

from apps.core.exceptions import AppError

from .langchain_tools import build_langchain_tools

SYSTEM_PROMPT = (
    "你是现代化物流 TMS 平台的智能助手，服务于控制塔与运营/财务/客服团队。"
    "你可以调用工具分析运单的 ETA 风险、回单催收、费用风控、调度建议、异常归因，并草拟客服话术。"
    "原则："
    "1) 高风险动作（派车、放款、对外承诺等）只给建议、绝不自动执行，需人工确认；"
    "2) 结论必须基于工具返回的 evidence（证据链），不要臆测数据；"
    "3) 用简洁专业的中文回复，必要时给出明确的下一步建议（next_actions）。"
    "当用户提供运单号时，优先调用相应工具获取实据再作答。"
)

_lock = threading.Lock()
_graph = None
_pool = None


def _build_llm():
    from langchain_openai import ChatOpenAI

    if not settings.DEEPSEEK_API_KEY:
        raise AppError("DEEPSEEK_NOT_CONFIGURED", "DEEPSEEK_API_KEY 未配置，无法启动 Agent。", status=503)
    return ChatOpenAI(
        model=settings.DEEPSEEK_MODEL,
        base_url=settings.DEEPSEEK_BASE_URL,
        api_key=settings.DEEPSEEK_API_KEY,
        temperature=settings.AGENT_LLM_TEMPERATURE,
        timeout=settings.DEEPSEEK_TIMEOUT_SECONDS,
        max_retries=2,
    )


def _build_checkpointer():
    """优先 Postgres，连接失败或未启用则回退内存 saver。"""
    global _pool
    if not getattr(settings, "AGENT_CHECKPOINT_ENABLED", True):
        return MemorySaver()
    try:
        from langgraph.checkpoint.postgres import PostgresSaver
        from psycopg_pool import ConnectionPool

        d = settings.DATABASES["default"]
        conninfo = (
            f"host={d['HOST']} port={d['PORT'] or 5432} dbname={d['NAME']} "
            f"user={d['USER']} password={d['PASSWORD']}"
        )
        _pool = ConnectionPool(
            conninfo=conninfo,
            max_size=settings.AGENT_CHECKPOINT_POOL_MAX,
            kwargs={"autocommit": True, "prepare_threshold": 0},
            open=True,
        )
        checkpointer = PostgresSaver(_pool)
        checkpointer.setup()  # 幂等：创建 checkpoint 相关表
        return checkpointer
    except Exception:  # noqa: BLE001 - 持久化不可用时降级而非整体不可用
        if _pool is not None:
            _pool.close()
            _pool = None
        return MemorySaver()


def _build_graph(checkpointer):
    llm = _build_llm()
    tools = build_langchain_tools()
    llm_with_tools = llm.bind_tools(tools)
    system = SystemMessage(content=SYSTEM_PROMPT)

    def agent_node(state: MessagesState) -> dict:
        response = llm_with_tools.invoke([system, *state["messages"]])
        return {"messages": [response]}

    builder = StateGraph(MessagesState)
    builder.add_node("agent", agent_node)
    builder.add_node("tools", ToolNode(tools))
    builder.add_edge(START, "agent")
    builder.add_conditional_edges("agent", tools_condition, {"tools": "tools", END: END})
    builder.add_edge("tools", "agent")
    return builder.compile(checkpointer=checkpointer)


def get_agent_graph():
    """返回编译后的图（线程安全单例）。首次调用会初始化 LLM 与 checkpointer。"""
    global _graph
    if _graph is None:
        with _lock:
            if _graph is None:
                _graph = _build_graph(_build_checkpointer())
    return _graph


def reset_agent_graph():
    """测试辅助：丢弃缓存的图与连接池。"""
    global _graph, _pool
    _graph = None
    if _pool is not None:
        try:
            _pool.close()
        finally:
            _pool = None
