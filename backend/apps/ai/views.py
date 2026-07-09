from django.db.models import Q
from django.http import StreamingHttpResponse
from django.utils import timezone
from rest_framework import mixins, viewsets
from rest_framework.decorators import action
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.core.context import request_id_var
from apps.core.exceptions import AppError
from apps.core.throttling import OptionalScopedRateThrottle
from apps.iam.permissions import HasPermission
from apps.iam.scoping import scope_queryset
from apps.ops.models import Waybill
from apps.ops.serializers import WaybillSerializer

from .models import AgentSuggestion
from .serializers import AgentSuggestionSerializer
from .services.deepseek import DeepSeekClient, DeepSeekError
from .services.tools import execute_tool, list_tools

# AI/Agent 端点统一权限点：使用 AI 能力（含 LLM、Agent 编排、查单）
PERM_AI_USE = "ai.use"


class _AIAuthedView(APIView):
    """AI 端点基类：需登录 + ai.use 权限，且按 ai scope 限流（防 token 成本 DoS）。"""

    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = PERM_AI_USE
    throttle_classes = [OptionalScopedRateThrottle]
    throttle_scope = "ai"


class DeepSeekStatusView(_AIAuthedView):
    def get(self, request):
        return Response(DeepSeekClient().status())


class DeepSeekChatView(_AIAuthedView):
    def post(self, request):
        messages = request.data.get("messages")
        if not isinstance(messages, list) or not messages:
            raise AppError("INVALID_MESSAGES", "messages 必须是非空数组。", status=400)
        try:
            resp = DeepSeekClient().chat_completion(
                messages=messages,
                model=request.data.get("model"),
                temperature=request.data.get("temperature"),
            )
        except DeepSeekError as exc:
            raise AppError(exc.code, exc.message, status=exc.status) from exc
        content = resp.get("choices", [{}])[0].get("message", {}).get("content", "")
        return Response({"provider": "deepseek", "model": resp.get("model"), "content": content, "raw": resp})


class AgentToolsView(_AIAuthedView):
    def get(self, request):
        return Response({"tools": list_tools()})


class AgentToolExecuteView(_AIAuthedView):
    def post(self, request):
        name = request.data.get("tool_name")
        arguments = request.data.get("arguments") or {}
        if not name:
            raise AppError("TOOL_NAME_REQUIRED", "tool_name 必填。", status=400)
        if not isinstance(arguments, dict):
            raise AppError("INVALID_ARGUMENTS", "arguments 必须是对象。", status=400)
        result = execute_tool(name, arguments)
        self._audit(request, name, arguments, result)
        return Response({"tool_name": name, "result": result})

    @staticmethod
    def _audit(request, name, arguments, result):
        from apps.audit.models import AuditLog

        AuditLog.objects.create(
            actor=request.user if request.user.is_authenticated else None,
            action=f"agent_tool:{name}",
            resource_type="waybill",
            resource_id=str(arguments.get("waybill_no", "")),
            request_id=request_id_var.get(),
            method=request.method,
            path=request.path,
            status_code=200,
            payload={"arguments": arguments, "risk_detected": result.get("risk_detected")},
        )


class AgentChatView(_AIAuthedView):
    """LangGraph ReAct Agent 同步问答：自动编排工具，返回答复 + 工具轨迹 + 待确认建议。"""

    def post(self, request):
        from .services.agent_runner import run_agent

        message = (request.data.get("message") or "").strip()
        if not message:
            raise AppError("MESSAGE_REQUIRED", "message 必填。", status=400)
        thread_id = request.data.get("thread_id") or None
        result = run_agent(message, thread_id)
        return Response(result)


class AgentStreamView(_AIAuthedView):
    """LangGraph Agent 流式问答（SSE）：逐段 token + 工具执行事件，对接实时信息流。"""

    def post(self, request):
        from .services.agent_runner import stream_agent

        message = (request.data.get("message") or "").strip()
        if not message:
            raise AppError("MESSAGE_REQUIRED", "message 必填。", status=400)
        thread_id = request.data.get("thread_id") or None
        response = StreamingHttpResponse(
            stream_agent(message, thread_id), content_type="text/event-stream"
        )
        response["Cache-Control"] = "no-cache"
        response["X-Accel-Buffering"] = "no"
        return response


class QueryWaybillView(_AIAuthedView):
    """自然语言查单（规则版）：按关键字检索运单；为空时返回风险/待回单运单。

    仅返回调用者数据范围内的运单（超管全见，否则按组织子树），杜绝跨租户查单。
    """

    def post(self, request):
        query = (request.data.get("query") or "").strip()
        # 按数据范围收口（组织子树），再叠加关键字过滤
        queryset = scope_queryset(
            Waybill.objects.select_related("customer", "carrier", "vehicle", "driver"),
            request.user,
        )
        if query:
            queryset = queryset.filter(
                Q(waybill_no__icontains=query)
                | Q(route_name__icontains=query)
                | Q(customer__name__icontains=query)
                | Q(vehicle__plate_no__icontains=query)
            )
        else:
            queryset = queryset.filter(
                Q(risk_level__in=[Waybill.RISK_HIGH, Waybill.RISK_MEDIUM]) | Q(receipt_status="pending")
            )
        items = list(queryset[:10])
        risk_count = sum(1 for w in items if w.risk_level in {Waybill.RISK_HIGH, Waybill.RISK_MEDIUM})
        return Response(
            {
                "answer": f"找到 {len(items)} 条相关运单，其中 {risk_count} 条存在 ETA/路线风险。",
                "query": query,
                "waybills": WaybillSerializer(items, many=True).data,
            }
        )


class AgentSuggestionViewSet(mixins.ListModelMixin, mixins.RetrieveModelMixin, viewsets.GenericViewSet):
    serializer_class = AgentSuggestionSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_AI_USE, "confirm": PERM_AI_USE}
    filterset_fields = ["suggestion_type", "status", "waybill"]
    search_fields = ["title", "body"]

    def get_queryset(self):
        # 建议按其运单组织归属数据范围，避免跨租户读 AI 建议
        return scope_queryset(
            AgentSuggestion.objects.select_related("waybill").all(),
            self.request.user, org_field="waybill__organization", include_null=True,
        )

    @action(detail=True, methods=["post"], url_path="confirm")
    def confirm(self, request, pk=None):
        """人工确认闭环：采纳或驳回 AI 建议。"""
        suggestion = self.get_object()
        decision = request.data.get("status")
        if decision not in (AgentSuggestion.STATUS_ACCEPTED, AgentSuggestion.STATUS_REJECTED):
            raise AppError("INVALID_DECISION", "status 必须是 accepted 或 rejected。", status=400)
        suggestion.status = decision
        suggestion.confirmed_by = request.user if request.user.is_authenticated else None
        suggestion.confirmed_at = timezone.now()
        suggestion.save(update_fields=["status", "confirmed_by", "confirmed_at", "updated_at"])
        return Response(AgentSuggestionSerializer(suggestion).data)
