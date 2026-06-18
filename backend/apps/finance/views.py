from rest_framework import mixins, viewsets
from rest_framework.decorators import action
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.core.exceptions import AppError

from .models import (
    ExpenseItem,
    ExpenseRecord,
    PaymentRequest,
    PricingRule,
    Statement,
    Webhook,
    WebhookDelivery,
)
from .serializers import (
    ExpenseItemSerializer,
    ExpenseRecordSerializer,
    PaymentRequestSerializer,
    PricingRuleSerializer,
    StatementListSerializer,
    StatementSerializer,
    WebhookDeliverySerializer,
    WebhookSerializer,
)


class StatementViewSet(mixins.ListModelMixin, mixins.RetrieveModelMixin, viewsets.GenericViewSet):
    queryset = Statement.objects.prefetch_related("lines").all()
    serializer_class = StatementSerializer
    filterset_fields = ["direction", "status", "counterparty_type", "counterparty_id"]
    search_fields = ["statement_no", "counterparty_name"]

    def get_serializer_class(self):
        return StatementListSerializer if self.action == "list" else StatementSerializer

    @action(detail=False, methods=["post"], url_path="generate")
    def generate(self, request):
        from .services import generate_statement

        data = request.data
        required = ["direction", "counterparty_type", "counterparty_id", "period_start", "period_end"]
        missing = [k for k in required if not data.get(k)]
        if missing:
            raise AppError("MISSING_FIELDS", f"缺少字段：{', '.join(missing)}", status=400)
        statement = generate_statement(
            direction=data["direction"],
            counterparty_type=data["counterparty_type"],
            counterparty_id=data["counterparty_id"],
            start=data["period_start"],
            end=data["period_end"],
            external_total=data.get("external_total") or 0,
        )
        return Response(StatementSerializer(statement).data, status=201)

    @action(detail=True, methods=["post"], url_path="confirm")
    def confirm(self, request, pk=None):
        from .services import confirm_statement

        statement = confirm_statement(self.get_object(), operator=request.user)
        return Response(StatementSerializer(statement).data)


class ExpenseItemViewSet(viewsets.ModelViewSet):
    queryset = ExpenseItem.objects.all()
    serializer_class = ExpenseItemSerializer
    filterset_fields = ["direction", "is_active"]
    search_fields = ["code", "name"]


class ExpenseRecordViewSet(viewsets.ModelViewSet):
    queryset = ExpenseRecord.objects.select_related("waybill").all()
    serializer_class = ExpenseRecordSerializer
    filterset_fields = ["direction", "risk_status", "waybill"]
    search_fields = ["expense_item_code", "external_id"]


class PaymentRequestViewSet(viewsets.ModelViewSet):
    queryset = PaymentRequest.objects.select_related("waybill").all()
    serializer_class = PaymentRequestSerializer
    filterset_fields = ["status"]
    search_fields = ["request_no"]


class PaymentResultView(APIView):
    """外部 OA/ERP/财务回写付款结果（配合 Idempotency-Key 幂等）。"""

    def post(self, request):
        request_no = request.data.get("request_no")
        updated = False
        if request_no:
            pr = PaymentRequest.objects.filter(request_no=request_no).first()
            if pr:
                pr.status = request.data.get("status", pr.status)
                pr.external_approval_no = request.data.get("external_approval_no", pr.external_approval_no)
                pr.save(update_fields=["status", "external_approval_no", "updated_at"])
                updated = True
        return Response({"status": "recorded", "updated": updated})


class PricingRuleViewSet(viewsets.ModelViewSet):
    queryset = PricingRule.objects.all()
    serializer_class = PricingRuleSerializer
    filterset_fields = ["price_type", "is_active", "customer", "carrier"]
    search_fields = ["name", "route_name", "expense_item_code"]


class WebhookViewSet(viewsets.ModelViewSet):
    queryset = Webhook.objects.all()
    serializer_class = WebhookSerializer
    filterset_fields = ["is_active"]
    search_fields = ["name", "target_url"]


class WebhookDeliveryViewSet(mixins.ListModelMixin, mixins.RetrieveModelMixin, viewsets.GenericViewSet):
    queryset = WebhookDelivery.objects.select_related("webhook").all()
    serializer_class = WebhookDeliverySerializer
    filterset_fields = ["status", "event_type", "webhook"]


class AgingView(APIView):
    """应收/应付账龄：?direction=receivable|payable。"""

    def get(self, request):
        from .models import ExpenseRecord
        from .services import aging_report

        direction = request.query_params.get("direction", ExpenseRecord.DIRECTION_RECEIVABLE)
        if direction not in (ExpenseRecord.DIRECTION_RECEIVABLE, ExpenseRecord.DIRECTION_PAYABLE):
            raise AppError("INVALID_DIRECTION", "direction 必须是 receivable 或 payable。", status=400)
        return Response(aging_report(direction))
