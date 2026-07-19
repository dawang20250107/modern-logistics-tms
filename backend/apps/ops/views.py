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


def _valid_coord(value) -> bool:
    """轨迹坐标校验：可转 float 且在合理经纬度范围内。"""
    try:
        v = float(value)
    except (TypeError, ValueError):
        return False
    return -180.0 <= v <= 180.0


def _expense_payload(item):
    from apps.finance.cost_items import item_label, payee_label

    return {
        "id": str(item.id),
        "direction": item.direction,
        "expense_item_code": item.expense_item_code,
        "item_label": item_label(item.expense_item_code),
        "amount": float(item.amount),
        "risk_status": item.risk_status,
        "payee_type": item.payee_type,
        "payee_label": payee_label(item.payee_type),
        "payee_ref": item.payee_ref,
        "source_system": item.source_system,
        "remark": item.remark,
    }


class WaybillViewSet(OrgScopedQuerysetMixin, viewsets.ModelViewSet):
    queryset = (
        Waybill.objects.select_related("customer", "carrier", "vehicle", "trailer", "driver")
        .prefetch_related("driver_assignments__driver")  # 消除列表 drivers 的 N+1
        # 应收/应付按运单条件聚合，零 N+1，供运单列表直呈"钱"这条主线
        .annotate(
            receivable_total=Sum("expenses__amount", filter=Q(expenses__direction="receivable")),
            payable_total=Sum("expenses__amount", filter=Q(expenses__direction="payable")),
        )
        .all()
    )
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
        "partial_sign": "waybill.manage",
        "reject": "waybill.manage",
        "collection": "waybill.view",
        "collect_cod_action": "waybill.manage",
        "remit_cod_action": "waybill.manage",
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
        """派车受理：更新受理状态，并（可选）按状态机推进——不再直写 status 绕过流转校验。"""
        from .services import allowed_next

        waybill = self.get_object()
        waybill.dispatch_status = request.data.get("dispatch_status", "accepted")
        waybill.save(update_fields=["dispatch_status", "updated_at"])
        target = request.data.get("status")
        if target and target != waybill.status:
            if target not in allowed_next(waybill.status):
                raise AppError(
                    "INVALID_TRANSITION",
                    f"不允许从 {waybill.status} 流转到 {target}。合法：{allowed_next(waybill.status)}",
                    status=409,
                )
            # 走状态机（盖里程碑戳 + 事件 + 订单完成/Webhook），杜绝绕过
            transition_waybill(waybill, target, operator=request.user, remark="派车受理")
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
        # 应付按收款方归集（上下游结算视角）
        from apps.finance.cost_items import payee_label

        by_payee: dict = {}
        for i in payables:
            key = i.payee_type or "other"
            row = by_payee.setdefault(key, {"payee_type": key, "payee_label": payee_label(key), "amount": 0.0})
            row["amount"] += float(i.amount)
        return Response(
            {
                "waybill_no": waybill.waybill_no,
                "receivables": [_expense_payload(i) for i in receivables],
                "payables": [_expense_payload(i) for i in payables],
                "external_expenses": [_expense_payload(i) for i in external],
                "payables_by_payee": list(by_payee.values()),
                "receivable_total": float(rt),
                "payable_total": float(pt),
                "gross_profit": float(gross),
                "gross_margin": float(gross / rt) if rt else 0,
            }
        )

    @action(detail=True, methods=["post"], url_path="add-expense")
    def add_expense(self, request, waybill_no=None):
        """新增结构化费用明细（运费/油卡/过路/装卸/押车/信息/回单/扣款）+ 收款方。"""
        from apps.finance.cost_items import ALL_ITEM_LABELS

        waybill = self.get_object()
        data = request.data
        direction = data.get("direction")
        code = data.get("expense_item_code")
        if direction not in (ExpenseRecord.DIRECTION_RECEIVABLE, ExpenseRecord.DIRECTION_PAYABLE):
            raise AppError("INVALID_DIRECTION", "direction 取值 receivable|payable。", status=400)
        if code not in ALL_ITEM_LABELS:
            raise AppError("INVALID_EXPENSE_ITEM", "费用科目非法。", status=400)
        try:
            amount = Decimal(str(data.get("amount") or "0"))
        except (TypeError, ValueError, ArithmeticError) as err:
            raise AppError("INVALID_AMOUNT", "金额非法。", status=400) from err
        ExpenseRecord.objects.create(
            waybill=waybill, direction=direction, expense_item_code=code, amount=amount,
            payee_type=data.get("payee_type", ""), payee_ref=data.get("payee_ref", ""),
            remark=data.get("remark", ""), source_system="manual",
        )
        return Response({"ok": True}, status=201)

    @action(detail=False, methods=["get"], url_path="cost-catalog")
    def cost_catalog(self, request):
        """费用科目与收款方目录（供前端录入下拉）。"""
        from apps.finance.cost_items import COST_ITEMS, INCOME_ITEMS, PAYEE_LABELS

        return Response({
            "cost_items": COST_ITEMS, "income_items": INCOME_ITEMS, "payees": PAYEE_LABELS,
        })

    @action(detail=True, methods=["get"], url_path="eta")
    def eta(self, request, waybill_no=None):
        """ETA：优先按当前定位+剩余里程+均速动态预测（数据不足则回退已存值）。"""
        from .eta import predict_eta

        waybill = self.get_object()
        prediction = predict_eta(waybill)
        return Response(
            {
                "waybill_no": waybill.waybill_no,
                "planned_arrival": waybill.planned_arrival,
                "estimated_arrival": waybill.estimated_arrival,
                "eta_drift_minutes": waybill.eta_drift_minutes,
                "risk_level": waybill.risk_level,
                "predicted": bool(prediction),
                "remaining_km": prediction["remaining_km"] if prediction else None,
                "avg_speed_kmh": prediction["avg_speed_kmh"] if prediction else None,
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

    @action(detail=True, methods=["post"], url_path="stop-event")
    def stop_event(self, request, waybill_no=None):
        """手动盖点位到达/离开戳（无 GPS 坐标时由司机/调度操作）：{seq, event: arrived|departed}。"""
        from django.utils import timezone

        from .models import WaybillEvent, WaybillStop

        waybill = self.get_object()
        seq = request.data.get("seq")
        event = request.data.get("event")
        stop = WaybillStop.objects.filter(waybill=waybill, seq=seq).first()
        if stop is None:
            raise AppError("STOP_NOT_FOUND", "点位不存在。", status=404)
        now = timezone.now()
        if event == "arrived":
            stop.actual_arrival_at = now
            stop.arrival_source = WaybillStop.SRC_MANUAL
            stop.status = WaybillStop.STATUS_ARRIVED
            stop.save(update_fields=["actual_arrival_at", "arrival_source", "status", "updated_at"])
        elif event == "departed":
            stop.actual_depart_at = now
            stop.status = WaybillStop.STATUS_DEPARTED
            stop.save(update_fields=["actual_depart_at", "status", "updated_at"])
        else:
            raise AppError("INVALID_STOP_EVENT", "event 取值 arrived|departed。", status=400)
        WaybillEvent.objects.create(
            waybill=waybill, event_type=f"stop_{event}", event_time=now,
            resource=f"stop#{stop.seq}", source="manual", payload={"seq": stop.seq},
        )
        return Response(WaybillDetailSerializer(waybill).data)

    @action(detail=True, methods=["get", "post"], url_path="contract")
    def contract(self, request, waybill_no=None):
        """合同库：GET 取最新合同；POST 生成承运合同（含中文PDF）。"""
        from .contracts import generate_contract
        from .serializers import ContractSerializer

        waybill = self.get_object()
        if request.method == "POST":
            c = generate_contract(waybill, operator=request.user)
            return Response(ContractSerializer(c).data, status=201)
        latest = waybill.contracts.first()
        return Response(ContractSerializer(latest).data if latest else None)

    @action(detail=True, methods=["post"], url_path="contract/send")
    def contract_send(self, request, waybill_no=None):
        """发送最新合同给司机（微信下发预留）。"""
        from .contracts import send_contract
        from .serializers import ContractSerializer

        waybill = self.get_object()
        latest = waybill.contracts.first()
        if latest is None:
            raise AppError("NO_CONTRACT", "请先生成合同。", status=404)
        return Response(ContractSerializer(send_contract(latest, operator=request.user)).data)

    @action(detail=True, methods=["post"], url_path="contract/confirm")
    def contract_confirm(self, request, waybill_no=None):
        """司机确认/拒签最新合同：{accepted, reply}。"""
        from .contracts import confirm_contract
        from .serializers import ContractSerializer

        waybill = self.get_object()
        latest = waybill.contracts.first()
        if latest is None:
            raise AppError("NO_CONTRACT", "无可确认的合同。", status=404)
        c = confirm_contract(
            latest, accepted=bool(request.data.get("accepted", True)),
            reply=request.data.get("reply", ""), operator=request.user,
        )
        return Response(ContractSerializer(c).data)

    @action(detail=True, methods=["get", "post"], url_path="reminders")
    def reminders(self, request, waybill_no=None):
        """作业提醒：GET 列出本运单提醒；POST 下发提醒（template 或 title+content）。"""
        from .models import ReminderTemplate
        from .reminders import send_reminder
        from .serializers import DriverReminderSerializer

        waybill = self.get_object()
        if request.method == "POST":
            tpl_id = request.data.get("template")
            template = ReminderTemplate.objects.filter(id=tpl_id).first() if tpl_id else None
            reminder = send_reminder(
                waybill, template=template, title=request.data.get("title", ""),
                content=request.data.get("content", ""),
                ack_required=bool(request.data.get("ack_required", True)), operator=request.user,
            )
            return Response(DriverReminderSerializer(reminder).data, status=201)
        rows = waybill.reminders.select_related("driver").all()
        return Response(DriverReminderSerializer(rows, many=True).data)

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

    @action(detail=True, methods=["post"], url_path="partial-sign")
    def partial_sign(self, request, waybill_no=None):
        """部分签收（货损货差）：记应收/实收/货损/短少数量，落回单 + 自动立货损异常。"""
        from .serializers import ReceiptSerializer
        from .services import partial_sign_waybill

        waybill = self.get_object()
        receipt = partial_sign_waybill(
            waybill,
            total_quantity=request.data.get("total_quantity", 0),
            signed_quantity=request.data.get("signed_quantity", 0),
            damaged_quantity=request.data.get("damaged_quantity", 0),
            shortage_quantity=request.data.get("shortage_quantity", 0),
            signatory=request.data.get("signatory", ""),
            signature=request.data.get("signature", ""),
            file_url=request.data.get("file_url", ""),
            sign_source=request.data.get("sign_source", "driver"),
            note=request.data.get("note", ""),
            operator=request.user,
        )
        return Response(
            {"waybill_no": waybill.waybill_no, "status": waybill.status, "receipt": ReceiptSerializer(receipt).data},
            status=201,
        )

    @action(detail=True, methods=["post"], url_path="reject")
    def reject(self, request, waybill_no=None):
        """整车拒收：记拒收原因，落拒收回单 + 自动立客诉异常，运单进入已拒收态。"""
        from .serializers import ReceiptSerializer
        from .services import reject_waybill

        waybill = self.get_object()
        receipt = reject_waybill(
            waybill,
            reason=request.data.get("reason", ""),
            signatory=request.data.get("signatory", ""),
            sign_source=request.data.get("sign_source", "driver"),
            operator=request.user,
        )
        return Response(
            {"waybill_no": waybill.waybill_no, "status": waybill.status, "receipt": ReceiptSerializer(receipt).data},
            status=201,
        )

    @action(detail=True, methods=["get"], url_path="collection")
    def collection(self, request, waybill_no=None):
        """司机送达应收明细：到付运费 + 代收货款合计。"""
        from .services import driver_collection

        return Response(driver_collection(self.get_object()))

    @action(detail=True, methods=["post"], url_path="collect-cod")
    def collect_cod_action(self, request, waybill_no=None):
        """司机确认已代收货款。"""
        from .services import collect_cod

        waybill = collect_cod(self.get_object(), operator=request.user)
        return Response(WaybillDetailSerializer(waybill).data)

    @action(detail=True, methods=["post"], url_path="remit-cod")
    def remit_cod_action(self, request, waybill_no=None):
        """财务确认代收货款已回款给货主。"""
        from .services import remit_cod

        waybill = remit_cod(self.get_object(), operator=request.user)
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

    @action(detail=True, methods=["get"], url_path="reply-card")
    def reply_card(self, request, waybill_no=None):
        """客服回复卡：状态/司机/车牌/最近节点/ETA/异常/回单 + 可复制文案。"""
        from .customer_ctx import reply_card

        return Response(reply_card(self.get_object()))

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


class OrderViewSet(OrgScopedQuerysetMixin, viewsets.ModelViewSet):
    # 订单本身无组织外键，按建单人所属组织归属其数据范围（组织子树可见）
    org_field = "created_by__organization"
    queryset = (
        Order.objects.select_related("customer", "created_by", "claimed_by")
        .prefetch_related("waybills", "cargo_items", "stops", "attachments")
        .all()
    )
    serializer_class = OrderSerializer
    filterset_fields = ["status", "channel", "source_type", "business_type", "priority"]
    search_fields = ["order_no", "remark", "contact_phone", "origin", "destination"]
    ordering_fields = ["created_at", "order_no", "priority"]

    @action(detail=False, methods=["get"], url_path="funnel")
    def funnel(self, request):
        """订单生命周期漏斗：按状态/渠道计数 + 今日建单数，供建单工作台"从哪来到哪去"管道。"""
        from django.db.models import Count
        from django.utils import timezone

        qs = self.get_queryset()
        by_status = {r["status"]: r["n"] for r in qs.values("status").annotate(n=Count("id"))}
        by_channel = {r["channel"]: r["n"] for r in qs.values("channel").annotate(n=Count("id"))}
        return Response({
            "by_status": by_status,
            "by_channel": by_channel,
            "today_created": qs.filter(created_at__date=timezone.localdate()).count(),
            "total": qs.count(),
        })

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
            quantity=data.get("cargo_quantity") or data.get("quantity") or 0,
            distance_km=data.get("distance_km") or 0,
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

    @action(detail=True, methods=["get"], url_path="ymm-quote")
    def ymm_quote(self, request, pk=None):
        """运满满调车运费比价（外部接口，未接入则离线参考价）。"""
        from apps.integrations.ymm import freight_quote

        order = self.get_object()
        return Response(freight_quote(
            order.origin, order.destination,
            weight_ton=order.cargo_weight_ton, volume_cbm=order.cargo_volume_cbm,
        ))

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
        trailer = Vehicle.objects.filter(id=data["trailer"]).first() if data.get("trailer") else None
        co_ids = data.get("co_drivers") or []
        co_drivers = list(Driver.objects.filter(id__in=co_ids)) if co_ids else []
        waybill = dispatch_order(
            order, dispatch_type=data.get("dispatch_type", ""), carrier=carrier,
            vehicle=vehicle, driver=driver, trailer=trailer, co_drivers=co_drivers,
            platform_name=(data.get("platform_name") or "").strip(),
            platform_order_no=(data.get("platform_order_no") or "").strip(),
            operator=request.user,
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
        # 客服辅助：指出关键信息缺口，建议补全
        important = {"origin": "始发地", "destination": "目的地", "contact_phone": "联系电话", "cargo_weight_ton": "货量"}
        missing = [{"field": f, "label": label} for f, label in important.items() if not parsed.get(f)]
        # 客服辅助：近 24h 同电话/同线路查重，防重复下单
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

    @action(detail=True, methods=["get"], url_path="workflow")
    def workflow(self, request, pk=None):
        """订单全流程总览：建单→确认→派单→合同→司机注册→在途→签收→报销→付款→对账→完成。"""
        from .workflow import order_workflow

        return Response(order_workflow(self.get_object()))

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


class ReminderTemplateViewSet(viewsets.ModelViewSet):
    """作业提醒富文本回复库：维护常用提醒模板。"""

    serializer_class = None
    search_fields = ["name", "category"]
    filterset_fields = ["category", "is_active"]
    ordering_fields = ["category", "name", "created_at"]

    def get_queryset(self):
        from .models import ReminderTemplate

        return ReminderTemplate.objects.all()

    def get_serializer_class(self):
        from .serializers import ReminderTemplateSerializer

        return ReminderTemplateSerializer

    def perform_create(self, serializer):
        serializer.save(created_by=_current_user_or_none(self.request))


class DriverReminderViewSet(viewsets.ModelViewSet):
    """下发给司机的提醒：列表 + 确认收到。"""

    serializer_class = None
    filterset_fields = ["driver", "waybill", "status"]
    ordering_fields = ["sent_at"]
    http_method_names = ["get", "post", "head", "options"]

    def get_queryset(self):
        from .models import DriverReminder

        return DriverReminder.objects.select_related("driver", "waybill").all()

    def get_serializer_class(self):
        from .serializers import DriverReminderSerializer

        return DriverReminderSerializer

    @action(detail=True, methods=["post"], url_path="acknowledge")
    def acknowledge(self, request, pk=None):
        """司机确认收到提醒。"""
        from .reminders import acknowledge_reminder

        reminder = acknowledge_reminder(self.get_object())
        return Response(self.get_serializer(reminder).data)


class ExceptionViewSet(OrgScopedQuerysetMixin, viewsets.ModelViewSet):
    # 异常挂在运单上，按运单组织归属其数据范围
    org_field = "waybill__organization"
    queryset = ExceptionRecord.objects.select_related("waybill", "assignee").all()
    serializer_class = ExceptionSerializer
    filterset_fields = ["exception_type", "status", "level", "source", "waybill"]
    search_fields = ["exception_type", "description"]

    def perform_create(self, serializer):
        from .services import record_exception_event

        exc = serializer.save()
        record_exception_event(
            exc, "create", actor=self.request.user, to_status=exc.status,
            note=exc.description, source=exc.source,
        )

    @action(detail=True, methods=["get"], url_path="timeline")
    def timeline(self, request, pk=None):
        """异常处置全过程事件溯源：立案/认领/AI 诊断/闭环等留痕。"""
        from .serializers import ExceptionEventSerializer

        exc = self.get_object()
        return Response(ExceptionEventSerializer(exc.events.select_related("actor").all(), many=True).data)

    @action(detail=True, methods=["post"], url_path="assign")
    def assign(self, request, pk=None):
        from .services import record_exception_event

        exc = self.get_object()
        from_status = exc.status
        exc.assignee_id = request.data.get("assignee") or None
        exc.status = ExceptionRecord.STATUS_HANDLING
        exc.save(update_fields=["assignee", "status", "updated_at"])
        record_exception_event(
            exc, "assign", actor=request.user, from_status=from_status, to_status=exc.status,
            note=f"指派给 {exc.assignee.username}" if exc.assignee_id else "取消指派",
            assignee_id=str(exc.assignee_id) if exc.assignee_id else None,
        )
        return Response(ExceptionSerializer(exc).data)

    @action(detail=True, methods=["post"], url_path="handle")
    def handle(self, request, pk=None):
        from .services import record_exception_event

        exc = self.get_object()
        from_status = exc.status
        exc.status = ExceptionRecord.STATUS_PENDING_AUDIT
        exc.resolution = request.data.get("resolution", exc.resolution)
        exc.save(update_fields=["status", "resolution", "updated_at"])
        record_exception_event(
            exc, "handle", actor=request.user, from_status=from_status, to_status=exc.status,
            note=request.data.get("resolution", ""),
        )
        return Response(ExceptionSerializer(exc).data)

    @action(detail=True, methods=["post"], url_path="close")
    def close(self, request, pk=None):
        from .services import record_exception_event

        exc = self.get_object()
        from_status = exc.status
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
        record_exception_event(
            exc, "close", actor=request.user, from_status=from_status, to_status=exc.status,
            note=exc.resolution, responsibility_party=exc.responsibility_party, amount=str(exc.amount),
        )
        return Response(ExceptionSerializer(exc).data)

    @action(detail=True, methods=["post"], url_path="ai-resolve")
    def ai_resolve(self, request, pk=None):
        """AI 自动化排查与异常预案生成：根据当前异常信息调用底层 LangGraph 大脑进行核验并建议。"""
        from apps.ai.services.agent_runner import run_agent

        from .services import record_exception_event

        exc = self.get_object()
        from_status = exc.status

        # 将异常交由 AI 处理，使用结构化系统 Prompt 引导
        prompt = (
            f"请作为资深物流安全调度主管，审查并处理以下运输异常：\n"
            f"异常类型: {exc.get_exception_type_display()} (级别: {exc.get_level_display()})\n"
            f"运单关联: {exc.waybill.waybill_no if exc.waybill else '无'}\n"
            f"详细描述: {exc.description}\n\n"
            f"请执行以下动作：\n"
            f"1. 调用相关工具查阅该运单的时效或详情（若适用）。\n"
            f"2. 给出具体的业务定损建议、后续人工操作要求，以及降低该类风险的根本措施。\n"
            f"3. 必须输出 '风险阻断' 的确认方案。"
        )

        # 调用核心 AI Agent 服务（固定 thread_id，复诊同一异常时可续接上下文）
        agent_result = run_agent(prompt, thread_id=f"exception_{exc.id}")

        # 回写 AI 的判断报告至异常工单的备忘录，并且推进状态到处理中
        exc.resolution = f"🤖 [AI 智能诊断与预案]\n{agent_result['answer']}\n\n[原有处理]: {exc.resolution}"
        if exc.status == ExceptionRecord.STATUS_PENDING:
            exc.status = ExceptionRecord.STATUS_HANDLING
        exc.save(update_fields=["resolution", "status", "updated_at"])
        record_exception_event(
            exc, "ai_resolve", actor=request.user, from_status=from_status, to_status=exc.status,
            note=agent_result["answer"], tool_calls=agent_result.get("tool_calls"),
        )

        return Response({
            "status": exc.status,
            "ai_resolution": agent_result["answer"],
            "tool_calls": agent_result["tool_calls"]
        })


class ReceiptViewSet(OrgScopedQuerysetMixin, viewsets.ModelViewSet):
    # 回单挂在运单上，按运单组织归属其数据范围
    org_field = "waybill__organization"
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

    MAX_POINTS = 1000

    def post(self, request):
        points = request.data.get("points", []) or []
        if not isinstance(points, list):
            raise AppError("TRACK_POINTS_INVALID", "points 必须为数组。", status=400)
        if len(points) > self.MAX_POINTS:
            raise AppError("TRACK_POINTS_TOO_MANY", f"单次最多上报 {self.MAX_POINTS} 个轨迹点。", status=413)
        redis = get_redis()
        pipe = redis.pipeline()
        queued = 0
        for point in points:
            if not isinstance(point, dict) or not point.get("waybill_no"):
                continue
            if not _valid_coord(point.get("lat")) or not _valid_coord(point.get("lng")):
                continue  # 丢弃非法坐标，避免脏数据落库
            pipe.rpush(TRACKING_QUEUE, json.dumps(point, default=str))
            queued += 1
        pipe.execute()
        if queued:
            flush_tracking_points.delay()
        return Response({"queued": queued, "received": len(points), "status": "queued_for_async_persist"}, status=202)


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


class IntegrationStatusView(APIView):
    """外部接入状态：运满满（已实现/离线）、飞书与微信（预留）。"""

    def get(self, request):
        from apps.integrations.status import integration_status

        return Response(integration_status())


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
