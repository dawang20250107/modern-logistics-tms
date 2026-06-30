"""LangGraph ReAct Agent 测试（离线：用假 LLM 驱动工具回路，不外呼 DeepSeek）。"""

import pytest
from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.messages import AIMessage
from langchain_core.outputs import ChatGeneration, ChatResult

from apps.ai.models import AgentSuggestion
from apps.ai.services import agent_graph
from apps.ai.services import tools as tool_registry
from apps.ai.services.langchain_tools import _schema_to_model, build_langchain_tools
from apps.ai.services.mcp_tools import build_mcp_tools
from apps.ops.models import Waybill


class FakeToolCallingModel(BaseChatModel):
    """按队列返回预设消息，bind_tools 直接返回自身。"""

    responses: list

    def bind_tools(self, tools, **kwargs):
        return self

    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        message = self.responses.pop(0)
        return ChatResult(generations=[ChatGeneration(message=message)])

    @property
    def _llm_type(self) -> str:
        return "fake-tool-calling"


@pytest.fixture(autouse=True)
def _reset_graph():
    agent_graph.reset_agent_graph()
    yield
    agent_graph.reset_agent_graph()


def test_build_langchain_tools_normalizes_names():
    tools = build_langchain_tools()
    assert len(tools) == 10  # 增加了 logistics.intelligent_consolidation 和 exception_handler
    names = {t.name for t in tools}
    assert "logistics__eta_risk_analysis" in names
    assert "telematics__vehicle_alert_summary" in names
    assert "analytics__query_metric" in names
    assert all("." not in n for n in names)  # OpenAI function name 不允许点号


def test_schema_to_model_supports_per_tool_fields():
    schema = {
        "type": "object",
        "required": ["waybill_no"],
        "properties": {
            "waybill_no": {"type": "string"},
            "amount": {"type": "number"},
            "force": {"type": "boolean"},
        },
    }
    model = _schema_to_model("finance.payout", schema)
    fields = model.model_fields
    assert fields["waybill_no"].is_required()
    assert not fields["amount"].is_required()  # 非 required → 可选
    assert not fields["force"].is_required()


def test_high_risk_tool_flags_confirmation_in_description():
    # 临时注册一个高风险写工具，验证风险等级会下沉到 LLM 可见描述
    @tool_registry.tool(
        "test.write_action",
        "执行某高风险写操作。",
        {"type": "object", "required": ["waybill_no"], "properties": {"waybill_no": {"type": "string"}}},
        risk=tool_registry.RISK_HIGH,
    )
    def _write(args):  # pragma: no cover - 不会被实际调用
        return {"summary": "ok"}

    try:
        built = {t.name: t for t in build_langchain_tools()}
        assert "test__write_action" in built
        assert "高风险" in built["test__write_action"].description
    finally:
        tool_registry._REGISTRY.pop("test.write_action", None)


def test_build_mcp_tools_noop_without_config():
    # 未配置 AGENT_MCP_SERVERS 时零开销返回空列表，不影响内置工具
    assert build_mcp_tools() == []


# transaction=True：LangGraph 同步执行在工作线程跑节点，工具的 ORM 查询走独立连接，
# 需数据真实提交才可见（生产环境各线程自有连接、数据已提交，无此约束）。
@pytest.mark.django_db(transaction=True)
def test_react_loop_runs_tool_and_collects_suggestion(monkeypatch):
    from apps.ai.services.agent_runner import run_agent

    wb = Waybill.objects.create(
        waybill_no="AG1",
        route_name="沪-蓉",
        risk_level=Waybill.RISK_HIGH,
        eta_drift_minutes=300,
    )

    responses = [
        AIMessage(
            content="",
            tool_calls=[
                {
                    "name": "logistics__eta_risk_analysis",
                    "args": {"waybill_no": wb.waybill_no},
                    "id": "call_1",
                    "type": "tool_call",
                }
            ],
        ),
        AIMessage(content=f"运单 {wb.waybill_no} 存在 ETA 风险，建议联系司机并同步客户。"),
    ]
    monkeypatch.setattr(agent_graph, "_build_llm", lambda: FakeToolCallingModel(responses=responses))

    result = run_agent("AG1 有没有风险？", thread_id="t-test-1")

    assert "ETA 风险" in result["answer"]
    assert result["thread_id"] == "t-test-1"
    assert len(result["tool_calls"]) == 1
    assert result["tool_calls"][0]["tool_name"] == "logistics.eta_risk_analysis"
    assert result["tool_calls"][0]["risk_detected"] is True
    # 高风险工具产出待人工确认建议并落库
    assert len(result["suggestions"]) == 1
    assert AgentSuggestion.objects.filter(waybill=wb, suggestion_type="eta_risk").count() == 1
