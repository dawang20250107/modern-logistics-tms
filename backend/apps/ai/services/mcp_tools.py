"""MCP 工具接入：把外部 MCP server 暴露的工具加载为 LangChain 工具。

后期接入大量 API/MCP 接口时，只需在 AGENT_MCP_SERVERS 配置好连接，
这些工具会和内置业务工具一起绑定进同一张 LangGraph，agent 即可无缝调用。

- 配置为空 → 零开销 no-op（返回 []）。
- 某个 server 不可达 → 降级（记录并跳过），不影响内置工具与整图可用。
- get_tools 是异步接口，这里在无事件循环的同步上下文中安全执行。
"""

import asyncio
import logging
import threading

from django.conf import settings

logger = logging.getLogger(__name__)


def _run_coro(coro):
    """在同步上下文执行协程；若当前线程已有运行中的事件循环则切到新线程执行。"""
    try:
        return asyncio.run(coro)
    except RuntimeError:
        result: dict = {}

        def _worker():
            result["value"] = asyncio.run(coro)

        t = threading.Thread(target=_worker)
        t.start()
        t.join()
        return result.get("value", [])


def build_mcp_tools() -> list:
    """根据 settings.AGENT_MCP_SERVERS 加载 MCP 工具。无配置或失败时返回 []。"""
    servers = getattr(settings, "AGENT_MCP_SERVERS", None) or {}
    if not servers:
        return []
    try:
        from langchain_mcp_adapters.client import MultiServerMCPClient

        client = MultiServerMCPClient(servers)
        tools = _run_coro(client.get_tools())
        logger.info("已加载 %d 个 MCP 工具，来自 %d 个 server。", len(tools), len(servers))
        return list(tools)
    except Exception:  # noqa: BLE001 - MCP 不可用时降级，保证内置工具与整图仍可用
        logger.exception("加载 MCP 工具失败，已跳过。")
        return []
