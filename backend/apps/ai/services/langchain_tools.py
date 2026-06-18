"""把自研工具注册表（services/tools.py）适配为 LangChain 工具。

设计要点：
- 复用同一份业务实现与证据链，不重复逻辑；LangGraph 只负责"何时调用"，
  业务执行与 AgentSuggestion 落库仍由 execute_tool 完成（AI 只建议，人工确认）。
- OpenAI/DeepSeek 的 function name 仅允许 [a-zA-Z0-9_-]，而注册表用点号命名
  （如 logistics.eta_risk_analysis），故对外暴露时把 "." 规范化为 "__"。
- response_format="content_and_artifact"：工具返回 (摘要文本, 完整结构化结果)，
  摘要进入 ToolMessage.content 供 LLM 推理，完整 evidence/suggestion 挂在
  ToolMessage.artifact 上，供运行器可靠取回（线程安全，无需 contextvar）。
- per-tool schema：从每个工具自带的 input_schema(JSON Schema) 动态生成 pydantic 模型，
  这样后期接入参数各异的大量 API 工具时，无需为每个工具手写 args 模型。
"""

from langchain_core.tools import StructuredTool
from pydantic import BaseModel, Field, create_model

from .tools import RISK_HIGH, execute_tool, list_tools

_JSON_TYPE_MAP = {
    "string": str,
    "integer": int,
    "number": float,
    "boolean": bool,
    "object": dict,
    "array": list,
}


def _normalize(name: str) -> str:
    return name.replace(".", "__")


def _schema_to_model(tool_name: str, input_schema: dict) -> type[BaseModel]:
    """JSON Schema → pydantic 模型，支持任意工具的自有参数。"""
    properties = input_schema.get("properties", {})
    required = set(input_schema.get("required", []))
    fields: dict = {}
    for field_name, spec in properties.items():
        py_type = _JSON_TYPE_MAP.get(spec.get("type", "string"), str)
        description = spec.get("description", "")
        if field_name in required:
            fields[field_name] = (py_type, Field(description=description))
        else:
            fields[field_name] = (py_type | None, Field(default=None, description=description))
    model_name = f"{_normalize(tool_name).title().replace('_', '')}Args"
    return create_model(model_name, **fields)


def build_langchain_tools() -> list[StructuredTool]:
    """根据注册表动态生成 LangChain 工具列表。"""
    tools: list[StructuredTool] = []
    for spec in list_tools():
        original = spec["name"]
        risk = spec.get("risk", "low")
        args_model = _schema_to_model(original, spec.get("input_schema", {}))

        def _runner(_name: str = original, **kwargs):
            # 真正的业务执行 + 证据链 + 人工确认落库都在 execute_tool 里
            result = execute_tool(_name, kwargs)
            summary = result.get("summary") or f"{_name} 已执行。"
            return summary, result

        description = spec["description"]
        if risk == RISK_HIGH:
            description += "（高风险：仅产出建议，需人工确认后才会真正执行，不可自动落地。）"

        tools.append(
            StructuredTool.from_function(
                func=_runner,
                name=_normalize(original),
                description=description,
                args_schema=args_model,
                response_format="content_and_artifact",
            )
        )
    return tools
