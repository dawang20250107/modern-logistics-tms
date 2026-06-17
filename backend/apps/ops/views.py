import json
from decimal import Decimal

from django.contrib.auth import get_user_model
from django.db.models import Sum
from django.utils import timezone
from django.utils.dateparse import parse_datetime
from rest_framework import viewsets
from rest_framework.decorators import action
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.core.exceptions import AppError
from apps.core.redis import get_redis
from apps.finance.models import ExpenseRecord
from apps.finance.services import generate_costs
from apps.iam.permissions import HasPermission
from apps.iam.scoping import OrgScopedQuerysetMixin

from .models import ExceptionRecord, Order, Receipt, Waybill, WaybillEvent
from .serializers import (
    ExceptionSerializer,
    OrderSerializer,
    ReceiptSerializer,
    TrackingPointSerializer,
    WaybillDetailSerializer,
    WaybillEventSerializer,
    WaybillSerializer,
    WaybillWriteSerializer,
)
from .services import transition_waybill
from .tasks import TRACKING_QUEUE, flush_tracking_points, process_receipt_ocr


def _current_user_or_none(request):
    user = request.user
    return user if isinstance(user, get_user_model()) else None


def _expense_payload(item):
    return {
        "id": str(item.id),
        "direction": item.direction,
        "expense_item_code": item.expense_item_code,
        "amount": float(item.amount),
        "risk_status": item.risk_status,
    }


class WaybillViewSet(OrgScopedQuerysetMixin, viewsets.ModelViewSet):
    queryset = Waybill.objects.select_related("customer", "carrier", "vehicle", "driver").all()
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {
        "list": "waybill.view",
        "retrieve": "waybill.view",
        "costs": "waybill.view",
        "eta": "waybill.view",
        "create": "waybill.manage",
        "update": "waybill.manage",
        "partial_update": "waybill.manage",
        "destroy": "waybill.manage",
        "assign": "waybill.manage",
        "transition": "waybill.manage",
        "tracking": "waybill.view",
        "gen_costs": "waybill.manage",
        "events": "waybill.manage",
    }
    lookup_field = "waybill_no"
    lookup_value_regex = "[^/]+"
    search_fields = ["waybill_no", "route_name", "customer__name", "vehicle__plate_no"]
    filterset_fields = ["status", "risk_level", "receipt_status"]
    ordering_fields = ["eta_drift_minutes", "created_at", "waybill_no"]
    ordering = ["-eta_drift_minutes", "risk_level", "waybill_no"]

    def get_serializer_class(self):
        if self.action in ("create", "update", "partial_update"):
            return WaybillWriteSerializer
        if self.action == "retrieve":
            return WaybillDetailSerializer
        return WaybillSerializer

    @action(detail=True, methods=["post"], url_path="dispatch")
    def assign(self, request, waybill_no=None):
        waybill = self.get_object()
        waybill.dispatch_status = request.data.get("dispatch_status", "accepted")
        waybill.status = request.data.get("status", Waybill.STATUS_IN_TRANSIT)
        waybill.save(update_fields=["dispatch_status", "status", "updated_at"])
        return Response(WaybillSerializer(waybill).data)

    @action(detail=True, methods=["get", "post"], url_path="events")
    def events(self, request, waybill_no=None):
        waybill = self.get_object()
        if request.method == "GET":
            return Response(WaybillEventSerializer(waybill.events.all(), many=True).data)
        event = WaybillEvent.objects.create(
            waybill=waybill,
            event_type=request.data.get("event_type", "manual_event"),
            event_time=parse_datetime(request.data.get("event_time", "") or "") or timezone.now(),
            resource=request.data.get("resource", waybill.waybill_no),
            source=request.data.get("source", "api"),
            payload=request.data.get("payload") or {},
        )
        return Response(WaybillEventSerializer(event).data, status=201)

    @action(detail=True, methods=["get"], url_path="costs")
    def costs(self, request, waybill_no=None):
        waybill = self.get_object()
        receivables = waybill.expenses.filter(direction=ExpenseRecord.DIRECTION_RECEIVABLE)
        payables = waybill.expenses.filter(direction=ExpenseRecord.DIRECTION_PAYABLE)
        external = waybill.expenses.filter(direction=ExpenseRecord.DIRECTION_EXTERNAL)

        def total(qs):
            return qs.aggregate(t=Sum("amount"))["t"] or Decimal("0")

        rt, pt, et = total(receivables), total(payables), total(external)
        gross = rt - pt - et
        return Response(
            {
                "waybill_no": waybill.waybill_no,
                "receivables": [_expense_payload(i) for i in receivables],
                "payables": [_expense_payload(i) for i in payables],
                "external_expenses": [_expense_payload(i) for i in external],
                "gross_profit": float(gross),
                "gross_margin": float(gross / rt) if rt else 0,
            }
        )

    @action(detail=True, methods=["get"], url_path="eta")
    def eta(self, request, waybill_no=None):
        waybill = self.get_object()
        return Response(
            {
                "waybill_no": waybill.waybill_no,
                "planned_arrival": waybill.planned_arrival,
                "estimated_arrival": waybill.estimated_arrival,
                "eta_drift_minutes": waybill.eta_drift_minutes,
                "risk_level": waybill.risk_level,
                "reason": "route_deviation_detected"
                if waybill.risk_level == Waybill.RISK_HIGH
                else "traffic_or_capacity_risk",
            }
        )

    @action(detail=True, methods=["post"], url_path="transition")
    def transition(self, request, waybill_no=None):
        waybill = self.get_object()
        to_status = request.data.get("to_status")
        if not to_status:
            raise AppError("TO_STATUS_REQUIRED", "to_status 必填。", status=400)
        transition_waybill(waybill, to_status, operator=request.user, remark=request.data.get("remark", ""))
        return Response(WaybillDetailSerializer(waybill).data)

    @action(detail=True, methods=["get"], url_path="tracking")
    def tracking(self, request, waybill_no=None):
        waybill = self.get_object()
        points = waybill.tracking_points.all().order_by("-reported_at")[:200]
        return Response(TrackingPointSerializer(points, many=True).data)

    @action(detail=True, methods=["post"], url_path="generate-costs")
    def gen_costs(self, request, waybill_no=None):
        waybill = self.get_object()
        result = generate_costs(waybill)
        return Response({"waybill_no": waybill.waybill_no, "generated": result})


class OrderViewSet(viewsets.ModelViewSet):
    queryset = Order.objects.select_related("customer").all()
    serializer_class = OrderSerializer
    filterset_fields = ["status"]
    search_fields = ["order_no", "remark"]
    ordering_fields = ["created_at", "order_no"]


class ExceptionViewSet(viewsets.ModelViewSet):
    queryset = ExceptionRecord.objects.select_related("waybill", "assignee").all()
    serializer_class = ExceptionSerializer
    filterset_fields = ["exception_type", "status", "level", "source"]
    search_fields = ["exception_type", "description"]

    @action(detail=True, methods=["post"], url_path="assign")
    def assign(self, request, pk=None):
        exc = self.get_object()
        exc.assignee_id = request.data.get("assignee") or None
        exc.status = ExceptionRecord.STATUS_HANDLING
        exc.save(update_fields=["assignee", "status", "updated_at"])
        return Response(ExceptionSerializer(exc).data)

    @action(detail=True, methods=["post"], url_path="handle")
    def handle(self, request, pk=None):
        exc = self.get_object()
        exc.status = ExceptionRecord.STATUS_PENDING_AUDIT
        exc.resolution = request.data.get("resolution", exc.resolution)
        exc.save(update_fields=["status", "resolution", "updated_at"])
        return Response(ExceptionSerializer(exc).data)

    @action(detail=True, methods=["post"], url_path="close")
    def close(self, request, pk=None):
        exc = self.get_object()
        exc.status = ExceptionRecord.STATUS_CLOSED
        exc.responsibility_party = request.data.get("responsibility_party", exc.responsibility_party)
        exc.amount = request.data.get("amount", exc.amount) or 0
        exc.resolution = request.data.get("resolution", exc.resolution)
        exc.save(update_fields=["status", "responsibility_party", "amount", "resolution", "updated_at"])
        # 异常费用责任 → 生成一条应付费用记录
        if exc.waybill_id and Decimal(str(exc.amount)) > 0:
            ExpenseRecord.objects.create(
                waybill=exc.waybill,
                direction=ExpenseRecord.DIRECTION_PAYABLE,
                expense_item_code="EXCEPTION_COST",
                amount=exc.amount,
                risk_status="normal",
                source_system="exception",
                external_id=str(exc.id),
            )
        return Response(ExceptionSerializer(exc).data)


class ReceiptViewSet(viewsets.ModelViewSet):
    queryset = Receipt.objects.select_related("waybill").all()
    serializer_class = ReceiptSerializer
    filterset_fields = ["waybill", "status", "ocr_status"]

    def perform_create(self, serializer):
        receipt = serializer.save(uploaded_by=_current_user_or_none(self.request))
        process_receipt_ocr.delay(str(receipt.id))

    @action(detail=True, methods=["post"], url_path="confirm")
    def confirm(self, request, pk=None):
        receipt = self.get_object()
        receipt.status = request.data.get("status", "confirmed")
        receipt.signatory = request.data.get("signatory", receipt.signatory)
        receipt.save(update_fields=["status", "signatory", "updated_at"])
        # 回写运单回单状态
        if receipt.waybill_id and receipt.status == "confirmed":
            receipt.waybill.receipt_status = "confirmed"
            receipt.waybill.save(update_fields=["receipt_status", "updated_at"])
        return Response(ReceiptSerializer(receipt).data)


class TrackingIngestView(APIView):
    """轨迹批量上报（高并发写热点）。

    削峰：请求仅把轨迹点压入 Redis 队列并触发异步落库，不直写主库；
    由 Celery 任务 ops.flush_tracking_points 批量 bulk_create（beat 每 5s 兜底）。
    """

    def post(self, request):
        points = request.data.get("points", []) or []
        redis = get_redis()
        pipe = redis.pipeline()
        queued = 0
        for point in points:
            if not point.get("waybill_no"):
                continue
            pipe.rpush(TRACKING_QUEUE, json.dumps(point, default=str))
            queued += 1
        pipe.execute()
        if queued:
            flush_tracking_points.delay()
        return Response({"queued": queued, "status": "queued_for_async_persist"}, status=202)
