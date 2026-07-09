from rest_framework import mixins, viewsets
from rest_framework.decorators import action
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.core.exceptions import AppError
from apps.iam.scoping import OrgScopedQuerysetMixin

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

    @action(detail=True, methods=["post"], url_path="audit")
    def audit(self, request, pk=None):
        """AI 异常审计：按同科目历史均值检出本单过高费用（非模拟，见 services.audit_statement）。"""
        from .services import audit_statement

        statement = self.get_object()
        summary = audit_statement(statement)
        return Response({**summary, "statement": StatementSerializer(statement).data})


class ExpenseItemViewSet(viewsets.ModelViewSet):
    queryset = ExpenseItem.objects.all()
    serializer_class = ExpenseItemSerializer
    filterset_fields = ["direction", "is_active"]
    search_fields = ["code", "name"]


class ExpenseRecordViewSet(OrgScopedQuerysetMixin, viewsets.ModelViewSet):
    # 费用按其运单组织归属数据范围；无运单的费用（org 为空）不误伤，对全体可见
    org_field = "waybill__organization"
    org_scope_include_null = True
    queryset = ExpenseRecord.objects.select_related("waybill").all()
    serializer_class = ExpenseRecordSerializer
    filterset_fields = ["direction", "risk_status", "waybill"]
    search_fields = ["expense_item_code", "external_id"]


class PaymentRequestViewSet(OrgScopedQuerysetMixin, viewsets.ModelViewSet):
    org_field = "waybill__organization"
    org_scope_include_null = True
    queryset = PaymentRequest.objects.select_related("waybill").all()
    serializer_class = PaymentRequestSerializer
    filterset_fields = ["status"]
    search_fields = ["request_no"]


class ReimbursementViewSet(OrgScopedQuerysetMixin, viewsets.ModelViewSet):
    """内部简易报销：提交 → 审批(生成应付+付款申请) → 付款。"""

    org_field = "waybill__organization"
    org_scope_include_null = True
    filterset_fields = ["status", "category", "waybill"]
    search_fields = ["reimb_no", "order_no", "reason"]
    ordering_fields = ["created_at", "amount"]

    def get_queryset(self):
        from .models import Reimbursement

        return Reimbursement.objects.select_related("waybill", "submitted_by").all()

    def get_serializer_class(self):
        from .serializers import ReimbursementSerializer

        return ReimbursementSerializer

    def create(self, request, *args, **kwargs):
        from apps.ops.models import Waybill

        from .reimbursement import submit_reimbursement
        from .serializers import ReimbursementSerializer

        data = request.data
        wb = Waybill.objects.filter(waybill_no=data.get("waybill_no")).first() if data.get("waybill_no") else None
        if wb is None and data.get("waybill"):
            wb = Waybill.objects.filter(id=data["waybill"]).first()
        reimb = submit_reimbursement(
            waybill=wb, order_no=data.get("order_no", ""), category=data.get("category", "other"),
            amount=data.get("amount", 0), reason=data.get("reason", ""), operator=request.user,
        )
        return Response(ReimbursementSerializer(reimb).data, status=201)

    @action(detail=True, methods=["post"], url_path="approve")
    def approve(self, request, pk=None):
        from .reimbursement import approve_reimbursement

        return Response(self.get_serializer(approve_reimbursement(self.get_object(), operator=request.user)).data)

    @action(detail=True, methods=["post"], url_path="reject")
    def reject(self, request, pk=None):
        from .reimbursement import reject_reimbursement

        reimb = reject_reimbursement(self.get_object(), reason=request.data.get("reason", ""), operator=request.user)
        return Response(self.get_serializer(reimb).data)

    @action(detail=True, methods=["post"], url_path="pay")
    def pay(self, request, pk=None):
        from .reimbursement import pay_reimbursement

        return Response(self.get_serializer(pay_reimbursement(self.get_object(), operator=request.user)).data)


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
    queryset = PricingRule.objects.select_related("customer", "carrier").all()
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


class FinancialDashboardMetricsView(APIView):
    """大屏财务指标与车队成本可视化 API（供 ECharts 调用）。"""

    def get(self, request):
        from datetime import timedelta

        from django.db.models import Sum
        from django.utils import timezone

        from .cost_items import COST_ITEMS
        from .models import ExpenseRecord

        days = int(request.query_params.get("days", 7))
        end_date = timezone.localdate()
        start_date = end_date - timedelta(days=days - 1)

        # 1. 营业额与利润趋势：按天聚合应收(收入)/应付(成本)/毛利。
        # 注：ExpenseRecord 无状态机字段，所有已落库的应收/应付即为真实发生额。
        def _daily(direction):
            rows = (
                ExpenseRecord.objects.filter(
                    direction=direction, created_at__date__gte=start_date, created_at__date__lte=end_date
                )
                .values("created_at__date")
                .annotate(t=Sum("amount"))
            )
            return {r["created_at__date"]: float(r["t"] or 0) for r in rows}

        rev_by_day = _daily(ExpenseRecord.DIRECTION_RECEIVABLE)
        cost_by_day = _daily(ExpenseRecord.DIRECTION_PAYABLE)
        trend = []
        for i in range(days):
            d = start_date + timedelta(days=i)
            rev, cost = rev_by_day.get(d, 0.0), cost_by_day.get(d, 0.0)
            trend.append({
                "date": d.strftime("%m-%d"), "revenue": rev, "cost": cost, "profit": round(rev - cost, 2),
            })

        # 2. 成本构成：按真实费用科目（cost_items.COST_ITEMS）聚合应付，零额科目不展示。
        composition_rows = (
            ExpenseRecord.objects.filter(
                direction=ExpenseRecord.DIRECTION_PAYABLE, created_at__date__gte=start_date
            )
            .values("expense_item_code")
            .annotate(t=Sum("amount"))
            .order_by("-t")
        )
        cost_composition = [
            {"name": COST_ITEMS.get(r["expense_item_code"], r["expense_item_code"] or "未分类"),
             "value": float(r["t"] or 0)}
            for r in composition_rows if (r["t"] or 0) > 0
        ]

        return Response({
            "trend": trend,
            "cost_composition": cost_composition,
            "period": f"近 {days} 天",
        })
