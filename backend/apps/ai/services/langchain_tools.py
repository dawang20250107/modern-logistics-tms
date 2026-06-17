"""把自研工具注册表（services/tools.py）适配为 LangChain 工具。

设计要点：
- 复用同一份业务实现与证据链，不重复逻辑；LangGraph 只负责"何时调用"，
  业务执行与 AgentSuggestion 落库仍由 execute_tool 完成（AI 只建议，人工确认）。
- OpenAI/DeepSeek 的 function name 仅允许 [a-zA-Z0-9_-]，而注册表用点号命名
  （如 logistics.eta_risk_analysis），故对外暴露时把 "." 规范化为 "__"。
- response_format="content_and_artifact"：工具返回 (摘要文本, 完整结构化结果)，
  摘要进入 ToolMessage.content 供 LLM 推理，完整 evidence/suggestion 挂在
  ToolMessage.artifact 上，供运行器可靠取回（线程安全，无需 contextvar）。
"""

from langchain_core.tools import StructuredTool
from pydantic import BaseModel, Field

from .tools import execute_tool, list_tools


class WaybillInput(BaseModel):
    waybill_no: str = Field(description="运单号，例如 WB20240001")


def _normalize(name: str) -> str:
    return name.replace(".", "__")


def build_langchain_tools() -> list[StructuredTool]:
    """根据注册表动态生成 LangChain 工具列表。"""
    tools: list[StructuredTool] = []
    for spec in list_tools():
        original = spec["name"]

        def _runner(waybill_no: str, _name: str = original):
            # 真正的业务执行 + 证据链 + 人工确认落库都在 execute_tool 里
            result = execute_tool(_name, {"waybill_no": waybill_no})
            summary = result.get("summary") or f"{_name} 已执行。"
            return summary, result

        tools.append(
            StructuredTool.from_function(
                func=_runner,
                name=_normalize(original),
                description=spec["description"],
                args_schema=WaybillInput,
                response_format="content_and_artifact",
            )
        )
    return tools
