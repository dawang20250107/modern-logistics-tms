from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    AgentChatView,
    AgentStreamView,
    AgentSuggestionViewSet,
    AgentToolExecuteView,
    AgentToolsView,
    DeepSeekChatView,
    DeepSeekStatusView,
    QueryWaybillView,
)

router = DefaultRouter(trailing_slash=False)
router.register("ai/suggestions", AgentSuggestionViewSet, basename="agent-suggestion")

urlpatterns = [
    path("ai/deepseek/status", DeepSeekStatusView.as_view(), name="deepseek-status"),
    path("ai/deepseek/chat", DeepSeekChatView.as_view(), name="deepseek-chat"),
    path("ai/query-waybill", QueryWaybillView.as_view(), name="ai-query-waybill"),
    path("agent/tools", AgentToolsView.as_view(), name="agent-tools"),
    path("agent/tools/execute", AgentToolExecuteView.as_view(), name="agent-tool-execute"),
    # LangGraph ReAct Agent（增量并存，旧工具端点保留向后兼容）
    path("agent/chat", AgentChatView.as_view(), name="agent-chat"),
    path("agent/stream", AgentStreamView.as_view(), name="agent-stream"),
    *router.urls,
]
