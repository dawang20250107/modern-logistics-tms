from rest_framework import mixins, viewsets
from rest_framework.response import Response
from rest_framework.views import APIView

from .models import (
    ExpenseItem,
    ExpenseRecord,
    PaymentRequest,
    PricingRule,
    Webhook,
    WebhookDelivery,
)
from .serializers import (
    ExpenseItemSerializer,
    ExpenseRecordSerializer,
    PaymentRequestSerializer,
    PricingRuleSerializer,
    WebhookDeliverySerializer,
    WebhookSerializer,
)


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
