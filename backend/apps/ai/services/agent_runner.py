"""Agent 运行器：封装一次问答（同步）与流式（SSE）两种入口。

- 复用编译后的图单例与 Postgres 状态（按 thread_id 续接多轮对话）。
- 工具结构化结果从 ToolMessage.artifact 取回（evidence + 待人工确认 suggestion）。
- 同时把 AI 活动推送到控制塔实时事件通道（core.redis.publish_event），打通实时信息流。
"""

import json
import uuid

from django.conf import settings
from langchain_core.messages import ToolMessage

from apps.core.redis import publish_event

from .agent_graph import get_agent_graph


def _new_thread_id() -> str:
    return uuid.uuid4().hex


def _run_config(thread_id: str) -> dict:
    return {
        "configurable": {"thread_id": thread_id},
        "recursion_limit": 2 * settings.AGENT_MAX_TOOL_LOOPS + 1,
    }


def _tool_call(artifact: dict) -> dict:
    return {
        "tool_name": artifact.get("tool_name"),
        "waybill_no": artifact.get("waybill_no"),
        "summary": artifact.get("summary"),
        "risk_detected": artifact.get("risk_detected"),
        "evidence": artifact.get("evidence"),
    }


def _digest_messages(messages: list) -> tuple[list, list]:
    """从消息历史的 ToolMessage.artifact 提取工具摘要与待确认 suggestions。"""
    tool_calls, suggestions = [], []
    for msg in messages:
        if isinstance(msg, ToolMessage) and isinstance(getattr(msg, "artifact", None), dict):
            artifact = msg.artifact
            tool_calls.append(_tool_call(artifact))
            if artifact.get("suggestion"):
                suggestions.append(artifact["suggestion"])
    return tool_calls, suggestions


def run_agent(message: str, thread_id: str | None = None) -> dict:
    """同步运行一轮，返回最终答复 + 工具轨迹 + 待确认建议。"""
    from langchain_core.messages import HumanMessage

    thread_id = thread_id or _new_thread_id()
    graph = get_agent_graph()

    state = graph.invoke({"messages": [HumanMessage(content=message)]}, _run_config(thread_id))
    messages = state.get("messages", [])
    answer = messages[-1].content if messages else ""
    tool_calls, suggestions = _digest_messages(messages)

    if suggestions:
        publish_event(
            "agent_suggestions",
            {"thread_id": thread_id, "count": len(suggestions), "suggestions": suggestions},
        )

    return {
        "thread_id": thread_id,
        "answer": answer,
        "tool_calls": tool_calls,
        "suggestions": suggestions,
    }


def _sse(event: str, data: dict) -> str:
    return f"event: {event}\ndata: {json.dumps(data, ensure_ascii=False, default=str)}\n\n"


def stream_agent(message: str, thread_id: str | None = None):
    """流式运行，逐段产出 SSE：token（增量答复）/ tool（工具执行）/ done（最终汇总）。"""
    from langchain_core.messages import HumanMessage

    thread_id = thread_id or _new_thread_id()
    graph = get_agent_graph()

    yield "retry: 5000\n\n"
    yield _sse("ready", {"thread_id": thread_id})

    config = _run_config(thread_id)
    tool_calls, suggestions = [], []
    try:
        for mode, payload in graph.stream(
            {"messages": [HumanMessage(content=message)]}, config, stream_mode=["updates", "messages"]
        ):
            if mode == "messages":
                token, _meta = payload
                text = getattr(token, "content", "")
                if text:
                    yield _sse("token", {"text": text})
            elif mode == "updates" and "tools" in payload:
                for msg in payload["tools"].get("messages", []):
                    artifact = getattr(msg, "artifact", None)
                    if not isinstance(artifact, dict):
                        continue
                    tool_calls.append(_tool_call(artifact))
                    if artifact.get("suggestion"):
                        suggestions.append(artifact["suggestion"])
                    yield _sse(
                        "tool",
                        {
                            "tool_name": artifact.get("tool_name"),
                            "waybill_no": artifact.get("waybill_no"),
                            "summary": artifact.get("summary"),
                            "risk_detected": artifact.get("risk_detected"),
                            "suggestion": artifact.get("suggestion"),
                        },
                    )
    except Exception as exc:  # noqa: BLE001 - 流式异常以事件形式回传，避免连接挂死
        yield _sse("error", {"message": str(exc)})
        return

    if suggestions:
        publish_event(
            "agent_suggestions",
            {"thread_id": thread_id, "count": len(suggestions), "suggestions": suggestions},
        )
    yield _sse("done", {"thread_id": thread_id, "tool_calls": tool_calls, "suggestions": suggestions})
