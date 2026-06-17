from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    AgentSuggestionViewSet,
    AgentToolExecuteView,
    AgentToolsView,
    DeepSeekChatView,
    DeepSeekStatusView,
    query_waybill,
)

router = DefaultRouter(trailing_slash=False)
router.register("ai/suggestions", AgentSuggestionViewSet, basename="agent-suggestion")

urlpatterns = [
    path("ai/deepseek/status", DeepSeekStatusView.as_view(), name="deepseek-status"),
    path("ai/deepseek/chat", DeepSeekChatView.as_view(), name="deepseek-chat"),
    path("ai/query-waybill", query_waybill, name="ai-query-waybill"),
    path("agent/tools", AgentToolsView.as_view(), name="agent-tools"),
    path("agent/tools/execute", AgentToolExecuteView.as_view(), name="agent-tool-execute"),
    *router.urls,
]
