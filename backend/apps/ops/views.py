import json
from decimal import Decimal

from django.contrib.auth import get_user_model
from django.db.models import Q, Sum
from django.utils import timezone
from django.utils.dateparse import parse_datetime
from rest_framework import viewsets
from rest_framework.decorators import action
from rest_framework.permissions import AllowAny, IsAuthenticated
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
from .services import merge_waybills, sign_waybill, split_waybill, transition_waybill
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
        "split": "waybill.manage",
        "merge": "waybill.manage",
        "dispatch_recommendation": "waybill.view",
        "dispatch_plan": "waybill.view",
        "sign": "waybill.manage",
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

    @action(detail=True, methods=["post"], url_path="sign")
    def sign(self, request, waybill_no=None):
        """司机/客户签收回传（e-POD）：电子签名 + 回单，推进到已签收并触发订单完成。"""
        waybill = self.get_object()
        receipt = sign_waybill(
            waybill,
            signatory=request.data.get("signatory", ""),
            signature=request.data.get("signature", ""),
            file_url=request.data.get("file_url", ""),
            sign_source=request.data.get("sign_source", "driver"),
            operator=request.user,
        )
        from .serializers import ReceiptSerializer

        return Response({"waybill_no": waybill.waybill_no, "status": waybill.status, "receipt": ReceiptSerializer(receipt).data}, status=201)

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

    @action(detail=True, methods=["post"], url_path="split")
    def split(self, request, waybill_no=None):
        waybill = self.get_object()
        splits = request.data.get("splits") or []
        children = split_waybill(waybill, splits, operator=request.user)
        return Response({"parent": waybill.waybill_no, "children": WaybillSerializer(children, many=True).data}, status=201)

    @action(detail=True, methods=["get"], url_path="dispatch-recommendation")
    def dispatch_recommendation(self, request, waybill_no=None):
        from .dispatch import recommend_dispatch

        waybill = self.get_object()
        return Response(recommend_dispatch(waybill))

    @action(detail=False, methods=["post"], url_path="dispatch-plan")
    def dispatch_plan(self, request):
        from .dispatch import plan_dispatch

        nos = request.data.get("waybill_nos") or []
        if nos:
            waybills = list(self.get_queryset().filter(waybill_no__in=nos))
        else:
            waybills = list(self.get_queryset().filter(status=Waybill.STATUS_PENDING_DISPATCH)[:200])
        return Response(plan_dispatch(waybills))

    @action(detail=False, methods=["post"], url_path="merge")
    def merge(self, request):
        nos = request.data.get("waybill_nos") or []
        if len(nos) < 2:
            raise AppError("INVALID_MERGE", "waybill_nos 至少 2 个。", status=400)
        waybills = list(self.get_queryset().filter(waybill_no__in=nos))
        if len(waybills) != len(set(nos)):
            raise AppError("WAYBILL_NOT_FOUND", "部分运单不存在或无权限。", status=404)
        merged = merge_waybills(waybills, operator=request.user, route_name=request.data.get("route_name", ""))
        return Response(WaybillSerializer(merged).data, status=201)


class OrderViewSet(viewsets.ModelViewSet):
    queryset = (
        Order.objects.select_related("customer", "created_by", "claimed_by")
        .prefetch_related("waybills", "cargo_items", "stops", "attachments")
        .all()
    )
    serializer_class = OrderSerializer
    filterset_fields = ["status", "channel", "source_type", "business_type", "priority"]
    search_fields = ["order_no", "remark", "contact_phone", "origin", "destination"]
    ordering_fields = ["created_at", "order_no", "priority"]

    @action(detail=False, methods=["post"], url_path="intake")
    def intake(self, request):
        """多渠道建单入口：传 text(自然语言/微信群) 或 fields(结构化)，可带货物明细/站点/草稿。"""
        from .intake import create_order_from_intake

        text = (request.data.get("text") or "").strip()
        fields = request.data.get("fields")
        cargo_items = request.data.get("cargo_items")
        stops = request.data.get("stops")
        if not text and not fields and not cargo_items:
            raise AppError("INTAKE_EMPTY", "text、fields 或 cargo_items 至少其一。", status=400)
        customer = None
        if fields and fields.get("customer"):
            from apps.masterdata.models import Customer

            customer = Customer.objects.filter(id=fields.get("customer")).first()
        order = create_order_from_intake(
            text=text,
            fields=fields,
            channel=request.data.get("channel", Order.CHANNEL_CS),
            source=request.data.get("source", ""),
            customer=customer,
            operator=request.user,
            cargo_items=cargo_items,
            stops=stops,
            status=request.data.get("status"),
        )
        return Response(OrderSerializer(order).data, status=201)

    @action(detail=False, methods=["get"], url_path="customer-addresses")
    def customer_addresses(self, request):
        """客户地址簿：按历史订单站点/地址去重返回常用提货/送货地址，供录单快速带出。"""
        from .models import OrderStop

        cid = request.query_params.get("customer")
        if not cid:
            return Response({"pickup": [], "delivery": []})
        seen, pickup, delivery = set(), [], []
        stops = (
            OrderStop.objects.filter(order__customer_id=cid)
            .order_by("-created_at")
            .values("stop_type", "city", "address", "contact_name", "contact_phone")[:200]
        )
        for s in stops:
            key = (s["stop_type"], s["city"], s["address"])
            if key in seen or not (s["address"] or s["city"]):
                continue
            seen.add(key)
            item = {"city": s["city"], "address": s["address"], "contact_name": s["contact_name"], "contact_phone": s["contact_phone"]}
            (pickup if s["stop_type"] == "pickup" else delivery).append(item)
        return Response({"pickup": pickup[:10], "delivery": delivery[:10]})

    @action(detail=True, methods=["post"], url_path="edit")
    def edit(self, request, pk=None):
        """编辑订单（含货物明细/站点替换），草稿/待确认/已确认可改。"""
        from .intake import update_order

        order = update_order(
            self.get_object(),
            fields=request.data.get("fields") or {},
            cargo_items=request.data.get("cargo_items"),
            stops=request.data.get("stops"),
            operator=request.user,
        )
        return Response(OrderSerializer(order).data)

    @action(detail=True, methods=["get", "post"], url_path="attachments")
    def attachments(self, request, pk=None):
        """订单附件：GET 列表 / POST 上传（合同/委托书/货物照片）。"""
        from .models import OrderAttachment
        from .serializers import OrderAttachmentSerializer

        order = self.get_object()
        if request.method == "GET":
            return Response(OrderAttachmentSerializer(order.attachments.all(), many=True).data)
        att = OrderAttachment.objects.create(
            order=order,
            kind=request.data.get("kind", OrderAttachment.KIND_OTHER),
            name=request.data.get("name", "") or (request.FILES.get("file").name if request.FILES.get("file") else ""),
            file=request.FILES.get("file"),
            file_url=request.data.get("file_url", ""),
            uploaded_by=_current_user_or_none(request),
        )
        return Response(OrderAttachmentSerializer(att).data, status=201)

    @action(detail=True, methods=["delete"], url_path="attachments/(?P<att_id>[^/]+)")
    def delete_attachment(self, request, pk=None, att_id=None):
        """删除订单附件。"""
        self.get_object().attachments.filter(id=att_id).delete()
        return Response(status=204)

    @action(detail=True, methods=["post"], url_path="approve")
    def approve(self, request, pk=None):
        """主管审批通过高价值订单。"""
        from .intake import approve_order

        order = approve_order(self.get_object(), operator=request.user, remark=request.data.get("remark", ""))
        return Response(OrderSerializer(order).data)

    @action(detail=True, methods=["post"], url_path="reject")
    def reject(self, request, pk=None):
        """主管驳回高价值订单。"""
        from .intake import reject_order

        order = reject_order(self.get_object(), operator=request.user, remark=request.data.get("remark", ""))
        return Response(OrderSerializer(order).data)

    @action(detail=True, methods=["post"], url_path="clone")
    def clone(self, request, pk=None):
        """复制建单：以现有订单为蓝本生成新草稿。"""
        from .intake import clone_order

        order = clone_order(self.get_object(), operator=request.user)
        return Response(OrderSerializer(order).data, status=201)

    @action(detail=True, methods=["post"], url_path="split")
    def split(self, request, pk=None):
        """订单拆单：{groups:[{cargo_item_ids:[...]}, ...]} 按货物明细拆成多张子订单。"""
        from .intake import split_order

        children = split_order(self.get_object(), request.data.get("groups") or [], operator=request.user)
        return Response(OrderSerializer(children, many=True).data, status=201)

    @action(detail=False, methods=["post"], url_path="merge")
    def merge(self, request, pk=None):
        """订单合单：{ids:[...]} 把多张订单合并为一张。"""
        from .intake import merge_orders

        merged = merge_orders(request.data.get("ids") or [], operator=request.user)
        return Response(OrderSerializer(merged).data, status=201)

    @action(detail=False, methods=["post"], url_path="quote")
    def quote(self, request):
        """录单自动报价：按客户/线路/货量估价。"""
        from apps.finance.services import estimate_order_quote

        data = request.data
        weight = data.get("cargo_weight_ton") or data.get("weight_ton") or 0
        volume = data.get("cargo_volume_cbm") or data.get("volume_cbm") or 0
        result = estimate_order_quote(
            customer_id=data.get("customer") or None,
            route_name=f"{data.get('origin', '')}→{data.get('destination', '')}",
            weight_ton=weight,
            volume_cbm=volume,
        )
        return Response(result)

    @action(detail=False, methods=["get"], url_path="export")
    def export(self, request):
        """导出当前筛选的订单为 CSV。"""
        import csv

        from django.http import HttpResponse

        qs = self.filter_queryset(self.get_queryset())[:5000]
        resp = HttpResponse(content_type="text/csv; charset=utf-8-sig")
        resp["Content-Disposition"] = 'attachment; filename="orders.csv"'
        resp.write("﻿")  # BOM，Excel 正确识别中文
        writer = csv.writer(resp)
        writer.writerow(["订单号", "客户", "渠道", "状态", "始发", "目的", "货量(吨)", "件数", "报价", "创建时间"])
        status_map = dict(Order.STATUS_CHOICES)
        channel_map = dict(Order.CHANNEL_CHOICES)
        for o in qs:
            writer.writerow([
                o.order_no, o.customer.name if o.customer else "", channel_map.get(o.channel, o.channel),
                status_map.get(o.status, o.status), o.origin, o.destination,
                o.cargo_weight_ton, o.cargo_quantity, o.quoted_amount, o.created_at.strftime("%Y-%m-%d %H:%M"),
            ])
        return resp

    @action(detail=True, methods=["post"], url_path="pool")
    def pool(self, request, pk=None):
        from .intake import pool_order

        return Response(OrderSerializer(pool_order(self.get_object(), operator=request.user)).data)

    @action(detail=True, methods=["post"], url_path="cancel")
    def cancel(self, request, pk=None):
        from .intake import cancel_order

        return Response(OrderSerializer(cancel_order(self.get_object(), operator=request.user)).data)

    @action(detail=False, methods=["post"], url_path="import")
    def import_rows(self, request):
        """批量建单：{rows: [{origin, destination, ...}], channel}。"""
        from .intake import import_orders

        result = import_orders(
            request.data.get("rows") or [],
            channel=request.data.get("channel", Order.CHANNEL_CS),
            source=request.data.get("source", ""),
            operator=request.user,
        )
        return Response(result, status=201)

    @action(detail=False, methods=["post"], url_path="batch")
    def batch(self, request):
        """批量操作：{action: confirm|pool|cancel|delete, ids: [...]}。"""
        from .intake import batch_orders

        action_name = request.data.get("action")
        ids = request.data.get("ids") or []
        if not ids:
            raise AppError("IDS_REQUIRED", "ids 必填。", status=400)
        return Response(batch_orders(action_name, ids, operator=request.user))

    @action(detail=False, methods=["get"], url_path="pool")
    def pool_list(self, request):
        """订单池：在池待派(POOLED) + 调度中(DISPATCHING，已认领)订单，按优先级与进池时间排序。

        包含已认领订单，避免认领后从看板消失；?mine=1 仅看自己认领的。
        """
        qs = self.get_queryset().filter(
            status__in=[Order.STATUS_POOLED, Order.STATUS_DISPATCHING]
        ).order_by("-priority", "pooled_at")
        if request.query_params.get("mine") in ("1", "true"):
            qs = qs.filter(claimed_by=_current_user_or_none(request))
        page = self.paginate_queryset(qs)
        ser = OrderSerializer(page if page is not None else qs, many=True)
        return self.get_paginated_response(ser.data) if page is not None else Response(ser.data)

    @action(detail=True, methods=["post"], url_path="claim")
    def claim(self, request, pk=None):
        """调度认领（并发安全，行锁防抢单）。"""
        from .order_dispatch import claim_order

        order = claim_order(pk, request.user)
        return Response(OrderSerializer(order).data)

    @action(detail=True, methods=["post"], url_path="release")
    def release(self, request, pk=None):
        from .order_dispatch import release_order

        return Response(OrderSerializer(release_order(self.get_object(), request.user)).data)

    @action(detail=True, methods=["get"], url_path="dispatch-suggestion")
    def dispatch_suggestion(self, request, pk=None):
        """AI 派单建议：运力候选 + 承运商比价 + 外部信号 + 派单类型建议。"""
        from .order_dispatch import recommend_dispatch_for_order

        return Response(recommend_dispatch_for_order(self.get_object()))

    @action(detail=False, methods=["post"], url_path="dispatch-plan")
    def dispatch_plan(self, request):
        """订单池批量智能排线：传 {ids:[...]}，返回每单的自有车分配建议（不落库）。"""
        from .order_dispatch import plan_dispatch_orders

        ids = request.data.get("ids") or []
        if not ids:
            raise AppError("IDS_REQUIRED", "ids 必填。", status=400)
        orders = list(
            self.get_queryset().filter(id__in=ids, status__in=[Order.STATUS_POOLED, Order.STATUS_DISPATCHING])
        )
        return Response(plan_dispatch_orders(orders))

    @action(detail=True, methods=["post"], url_path="dispatch")
    def dispatch_order_action(self, request, pk=None):
        """派单：指派承运商/车辆/司机 + 派单类型，生成运单。"""
        from apps.masterdata.models import Carrier, Driver, Vehicle

        from .order_dispatch import dispatch_order

        order = self.get_object()
        data = request.data
        carrier = Carrier.objects.filter(id=data["carrier"]).first() if data.get("carrier") else None
        vehicle = Vehicle.objects.filter(id=data["vehicle"]).first() if data.get("vehicle") else None
        driver = Driver.objects.filter(id=data["driver"]).first() if data.get("driver") else None
        waybill = dispatch_order(
            order, dispatch_type=data.get("dispatch_type", ""), carrier=carrier,
            vehicle=vehicle, driver=driver, operator=request.user,
        )
        return Response(WaybillSerializer(waybill).data, status=201)

    @action(detail=False, methods=["post"], url_path="parse-preview")
    def parse_preview(self, request):
        """仅解析预览，不落库（供前端 AI 建单先看结果再确认）。"""
        from .intake import find_duplicate_orders, parse_order_text

        text = (request.data.get("text") or "").strip()
        if not text:
            raise AppError("INTAKE_EMPTY", "text 必填。", status=400)
        parsed = parse_order_text(text)
        meta = parsed.pop("_meta", {})
        # AI 赋能客服：指出关键信息缺口，建议补全
        important = {"origin": "始发地", "destination": "目的地", "contact_phone": "联系电话", "cargo_weight_ton": "货量"}
        missing = [{"field": f, "label": label} for f, label in important.items() if not parsed.get(f)]
        # AI 赋能客服：近 24h 同电话/同线路查重，防重复下单
        dups = find_duplicate_orders(
            contact_phone=parsed.get("contact_phone", ""),
            origin=parsed.get("origin", ""),
            destination=parsed.get("destination", ""),
        )
        duplicates = [
            {
                "id": str(o.id), "order_no": o.order_no, "status": o.status,
                "origin": o.origin, "destination": o.destination,
                "contact_phone": o.contact_phone, "created_at": o.created_at.isoformat(),
            }
            for o in dups
        ]
        return Response({"fields": parsed, "meta": meta, "missing": missing, "duplicates": duplicates})

    @action(detail=True, methods=["get"], url_path="timeline")
    def timeline(self, request, pk=None):
        """订单全生命周期事件时间线。"""
        from .serializers import OrderEventSerializer

        order = self.get_object()
        return Response(OrderEventSerializer(order.events.select_related("actor").all(), many=True).data)

    @action(detail=True, methods=["post"], url_path="confirm")
    def confirm(self, request, pk=None):
        from .intake import confirm_order

        return Response(OrderSerializer(confirm_order(self.get_object(), operator=request.user)).data)

    @action(detail=True, methods=["post"], url_path="convert")
    def convert(self, request, pk=None):
        from .intake import convert_order_to_waybill

        waybill = convert_order_to_waybill(self.get_object(), operator=request.user)
        return Response(WaybillSerializer(waybill).data, status=201)


class OrderTemplateViewSet(viewsets.ModelViewSet):
    """录单模板：保存常用订单为模板，一键套用建单。"""

    serializer_class = None  # set below to avoid import cycle at class-def time
    search_fields = ["name"]
    ordering_fields = ["created_at", "name"]

    def get_queryset(self):
        from .models import OrderTemplate

        return OrderTemplate.objects.select_related("created_by").all()

    def get_serializer_class(self):
        from .serializers import OrderTemplateSerializer

        return OrderTemplateSerializer

    def perform_create(self, serializer):
        serializer.save(created_by=_current_user_or_none(self.request))


class ExceptionViewSet(viewsets.ModelViewSet):
    queryset = ExceptionRecord.objects.select_related("waybill", "assignee").all()
    serializer_class = ExceptionSerializer
    filterset_fields = ["exception_type", "status", "level", "source", "waybill"]
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


class PublicTrackingView(APIView):
    """客户自助订单跟踪（免登录）：订单号 + 手机号验证，返回脱敏进度。"""

    authentication_classes: list = []
    permission_classes = [AllowAny]

    @staticmethod
    def _phone_ok(order, phone):
        if not phone:
            return False
        for cand in (order.contact_phone, order.pickup_contact_phone, order.delivery_contact_phone):
            if cand and (cand == phone or (len(phone) >= 4 and cand.endswith(phone))):
                return True
        return False

    def get(self, request):
        order_no = (request.query_params.get("order_no") or "").strip()
        phone = (request.query_params.get("phone") or "").strip()
        if not order_no or not phone:
            raise AppError("TRACK_PARAMS", "order_no 与 phone 必填。", status=400)
        order = Order.objects.filter(order_no=order_no).first()
        if order is None or not self._phone_ok(order, phone):
            raise AppError("TRACK_NOT_FOUND", "未找到匹配的订单，请核对订单号与手机号。", status=404)

        milestones = [
            {"event": e.event_type, "time": e.event_time.isoformat()}
            for e in order.events.all()
            if e.event_type in {"created", "confirmed", "pooled", "dispatched", "completed"}
        ]
        shipment = None
        waybill = order.waybills.order_by("-created_at").first()
        if waybill is not None:
            position = None
            if waybill.vehicle_id:
                from apps.telematics.models import VehicleState

                state = VehicleState.objects.filter(vehicle_id=waybill.vehicle_id).first()
                if state and state.reported_at:
                    position = {"lat": float(state.lat), "lng": float(state.lng), "at": state.reported_at.isoformat()}
            shipment = {
                "waybill_no": waybill.waybill_no,
                "status": waybill.get_status_display(),
                "estimated_arrival": waybill.estimated_arrival.isoformat() if waybill.estimated_arrival else None,
                "receipt_status": waybill.receipt_status,
                "position": position,
            }
        return Response({
            "order_no": order.order_no,
            "status": dict(Order.STATUS_CHOICES).get(order.status, order.status),
            "business_type": order.get_business_type_display(),
            "origin": order.origin,
            "destination": order.destination,
            "created_at": order.created_at.isoformat(),
            "milestones": milestones,
            "shipment": shipment,
        })


class WorkbenchView(APIView):
    """个人工作台「我的待办」：按角色聚合当前用户最该处理的事项。"""

    def get(self, request):
        from django.utils import timezone

        from apps.finance.models import Statement
        from apps.notifications.models import Notification

        user = request.user
        today = timezone.localdate()
        open_exc = ~Q(status__in=[ExceptionRecord.STATUS_CLOSED, ExceptionRecord.STATUS_REJECTED])

        my_pending = Order.objects.select_related("customer", "created_by", "claimed_by").prefetch_related("waybills", "cargo_items", "stops", "attachments").filter(
            created_by=user, status=Order.STATUS_PENDING_CONFIRM
        )
        pool = Order.objects.filter(status=Order.STATUS_POOLED)
        pool_serialized = Order.objects.select_related("customer", "created_by", "claimed_by").prefetch_related("waybills", "cargo_items", "stops", "attachments").filter(
            status=Order.STATUS_POOLED
        ).order_by("-priority", "pooled_at")[:5]
        return Response({
            "common": {
                "unread_notifications": Notification.objects.filter(recipient=user, is_read=False).count(),
                "my_open_exceptions": ExceptionRecord.objects.filter(open_exc, assignee=user).count(),
            },
            "cs": {
                "my_orders_pending_confirm": my_pending.count(),
                "my_orders_today": Order.objects.filter(created_by=user, created_at__date=today).count(),
                "recent_pending": OrderSerializer(my_pending.order_by("-created_at")[:5], many=True).data,
            },
            "dispatch": {
                "pool_count": pool.count(),
                "my_claimed": Order.objects.filter(claimed_by=user, status=Order.STATUS_DISPATCHING).count(),
                "pool_top": OrderSerializer(pool_serialized, many=True).data,
            },
            "finance": {
                "draft_statements": Statement.objects.filter(status=Statement.STATUS_DRAFT).count(),
            },
        })


class PublicOrderIntakeView(APIView):
    """客户自助下单（免登录）：客户填写联系/线路/货物，落待确认订单，进入客服确认队列。"""

    authentication_classes: list = []
    permission_classes = [AllowAny]

    def post(self, request):
        from .intake import create_order_from_intake

        data = request.data
        contact_phone = (data.get("contact_phone") or "").strip()
        origin = (data.get("origin") or "").strip()
        destination = (data.get("destination") or "").strip()
        if not (contact_phone and origin and destination):
            raise AppError("PUBLIC_INTAKE_REQUIRED", "联系电话、始发、目的地必填。", status=400)
        channel = data.get("channel")
        if channel not in (Order.CHANNEL_SELF, Order.CHANNEL_MINIPROGRAM):
            channel = Order.CHANNEL_SELF
        fields = {
            "contact_name": data.get("contact_name", ""),
            "contact_phone": contact_phone,
            "origin": origin,
            "destination": destination,
            "cargo_desc": data.get("cargo_desc", ""),
            "cargo_weight_ton": data.get("cargo_weight_ton") or 0,
            "cargo_quantity": data.get("cargo_quantity") or 0,
            "expected_pickup_at": data.get("expected_pickup_at") or None,
            "remark": data.get("remark", ""),
            "source_type": Order.SOURCE_INDIVIDUAL,
        }
        order = create_order_from_intake(
            fields=fields, channel=channel, source=data.get("source", "客户自助"),
            status=Order.STATUS_PENDING_CONFIRM,
        )
        return Response(
            {"order_no": order.order_no, "status": order.status,
             "message": "下单成功，客服将尽快与您确认。可凭订单号与手机号查询进度。"},
            status=201,
        )
