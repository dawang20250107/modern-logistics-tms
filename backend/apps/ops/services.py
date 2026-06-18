"""运单状态机与执行服务。"""

from decimal import Decimal

from django.utils import timezone

from apps.core.exceptions import AppError
from apps.core.redis import publish_event

from .models import Waybill, WaybillEvent

# 合法状态流转表
ALLOWED_TRANSITIONS: dict[str, list[str]] = {
    Waybill.STATUS_DRAFT: [Waybill.STATUS_PENDING_DISPATCH, Waybill.STATUS_CANCELLED],
    Waybill.STATUS_PENDING_DISPATCH: [Waybill.STATUS_DISPATCHED, Waybill.STATUS_CANCELLED],
    Waybill.STATUS_DISPATCHED: [Waybill.STATUS_LOADED, Waybill.STATUS_PENDING_DISPATCH, Waybill.STATUS_CANCELLED],
    Waybill.STATUS_LOADED: [Waybill.STATUS_DEPARTED],
    Waybill.STATUS_DEPARTED: [Waybill.STATUS_IN_TRANSIT],
    Waybill.STATUS_IN_TRANSIT: [Waybill.STATUS_ARRIVED],
    Waybill.STATUS_ARRIVED: [Waybill.STATUS_SIGNED],
    Waybill.STATUS_SIGNED: [Waybill.STATUS_DELIVERED],
    Waybill.STATUS_DELIVERED: [Waybill.STATUS_SETTLED],
    Waybill.STATUS_SETTLED: [],
    Waybill.STATUS_CANCELLED: [],
    Waybill.STATUS_VOIDED: [],
}

# 允许从绝大多数非终态直接作废
_VOIDABLE_FROM = {
    Waybill.STATUS_DRAFT,
    Waybill.STATUS_PENDING_DISPATCH,
    Waybill.STATUS_DISPATCHED,
    Waybill.STATUS_LOADED,
}


def allowed_next(status: str) -> list[str]:
    nexts = list(ALLOWED_TRANSITIONS.get(status, []))
    if status in _VOIDABLE_FROM and Waybill.STATUS_VOIDED not in nexts:
        nexts.append(Waybill.STATUS_VOIDED)
    return nexts


def transition_waybill(waybill: Waybill, to_status: str, *, operator=None, remark: str = "") -> Waybill:
    if to_status not in allowed_next(waybill.status):
        raise AppError(
            "INVALID_TRANSITION",
            f"不允许从 {waybill.status} 流转到 {to_status}。",
            status=409,
        )
    from_status = waybill.status
    waybill.status = to_status
    # 关键里程碑实际时间物化（事件日志仍是真相源）
    now = timezone.now()
    milestone_field = {
        Waybill.STATUS_LOADED: "loaded_at",
        Waybill.STATUS_DEPARTED: "departed_at",
        Waybill.STATUS_ARRIVED: "arrived_at",
        Waybill.STATUS_SIGNED: "signed_at",
    }.get(to_status)
    update_fields = ["status", "updated_at"]
    if milestone_field and getattr(waybill, milestone_field) is None:
        setattr(waybill, milestone_field, now)
        update_fields.append(milestone_field)
    waybill.save(update_fields=update_fields)
    WaybillEvent.objects.create(
        waybill=waybill,
        event_type=f"status_changed:{to_status}",
        event_time=timezone.now(),
        resource=waybill.waybill_no,
        source="transition",
        payload={"from": from_status, "to": to_status, "remark": remark},
    )
    publish_event(
        "waybill_status",
        {"waybill_no": waybill.waybill_no, "from": from_status, "to": to_status},
    )
    # 签收/送达 → 回写订单完成，闭环到对账
    _complete_order_on_delivery(waybill, to_status)
    # 对外 Webhook 事件（懒导入避免应用间循环依赖）
    from apps.finance.services import emit_event

    emit_event("waybill.status_changed", {"waybill_no": waybill.waybill_no, "from": from_status, "to": to_status})
    return waybill


def sign_waybill(waybill, *, signatory="", signature="", file_url="", sign_source="driver", operator=None):
    """司机/客户签收回传（e-POD）：落回单 + 一步推进到已签收（触发订单完成）。"""
    from .models import Receipt

    if waybill.status in (Waybill.STATUS_SIGNED, Waybill.STATUS_DELIVERED, Waybill.STATUS_SETTLED):
        raise AppError("ALREADY_SIGNED", "运单已签收。", status=409)
    if waybill.status not in (Waybill.STATUS_IN_TRANSIT, Waybill.STATUS_ARRIVED):
        raise AppError("NOT_SIGNABLE", "仅在途/已到达运单可签收。", status=409)

    receipt = Receipt.objects.create(
        waybill=waybill,
        receipt_type="signed_pod",
        status="confirmed",
        file_url=file_url,
        signatory=signatory,
        signature=signature,
        sign_source=sign_source,
        signed_at=timezone.now(),
        uploaded_by=operator if operator and getattr(operator, "is_authenticated", False) else None,
    )
    # 推进状态机：在途→到达→签收（签收触发订单完成回写）
    if waybill.status == Waybill.STATUS_IN_TRANSIT:
        transition_waybill(waybill, Waybill.STATUS_ARRIVED, operator=operator, remark="签收回传自动到达")
    transition_waybill(waybill, Waybill.STATUS_SIGNED, operator=operator, remark=f"签收人 {signatory}")
    waybill.receipt_status = "received"
    waybill.save(update_fields=["receipt_status", "updated_at"])
    return receipt


def _complete_order_on_delivery(waybill, to_status):
    """运单签收/送达/结算时，把关联订单回写为已完成（幂等），打通订单全流程闭环。"""
    from .models import Order

    if to_status not in (Waybill.STATUS_SIGNED, Waybill.STATUS_DELIVERED, Waybill.STATUS_SETTLED):
        return
    order = waybill.order
    if order is None or order.status == Order.STATUS_COMPLETED:
        return
    from .intake import record_order_event

    prev = order.status
    now = timezone.now()
    order.status = Order.STATUS_COMPLETED
    order.delivered_at = now
    # SLA 判定：有承诺到达时间则比对实际完成时间
    if order.expected_delivery_at:
        order.sla_status = Order.SLA_ON_TIME if now <= order.expected_delivery_at else Order.SLA_BREACHED
    order.save(update_fields=["status", "delivered_at", "sla_status", "updated_at"])
    record_order_event(
        order, "completed", from_status=prev, to_status=order.status, source="system",
        waybill_no=waybill.waybill_no, trigger=to_status, sla_status=order.sla_status,
    )
    publish_event("order_completed", {"order_no": order.order_no, "waybill_no": waybill.waybill_no})
    # 持久化通知：完成→提醒建单客服
    if order.created_by_id:
        from apps.notifications.services import notify_users

        notify_users(
            [order.created_by_id], category="order_completed",
            title=f"订单已完成：{order.order_no}",
            body=f"运单 {waybill.waybill_no} 已签收/送达，可进入对账。",
            link_type="order", link_id=str(order.id),
        )


# 仅待调度前可拆/合
_SPLITTABLE_FROM = {Waybill.STATUS_DRAFT, Waybill.STATUS_PENDING_DISPATCH}


def _copy_fields(src: Waybill) -> dict:
    return {
        "order": src.order,
        "customer": src.customer,
        "carrier": src.carrier,
        "route_name": src.route_name,
        "planned_route": src.planned_route,
        "origin": src.origin,
        "destination": src.destination,
        "organization": src.organization,
    }


def split_waybill(waybill: Waybill, splits: list[dict], *, operator=None) -> list[Waybill]:
    """把一张运单按货量拆成多张子单；原单作废，子单指向原单（血缘）。"""
    if waybill.status not in _SPLITTABLE_FROM:
        raise AppError("INVALID_SPLIT", "仅待调度前的运单可拆单。", status=409)
    if not splits or len(splits) < 2:
        raise AppError("INVALID_SPLIT", "拆单至少需要 2 个子单。", status=400)

    now = timezone.now()
    children = []
    for idx, part in enumerate(splits, 1):
        child = Waybill.objects.create(
            waybill_no=f"{waybill.waybill_no}-S{idx}",
            parent=waybill,
            status=Waybill.STATUS_PENDING_DISPATCH,
            cargo_quantity=part.get("cargo_quantity", 0),
            cargo_weight_ton=part.get("cargo_weight_ton", 0),
            cargo_volume_cbm=part.get("cargo_volume_cbm", 0),
            **_copy_fields(waybill),
        )
        WaybillEvent.objects.create(
            waybill=child, event_type="split_from", event_time=now, resource=waybill.waybill_no,
            source="split", payload={"parent": waybill.waybill_no},
        )
        children.append(child)

    waybill.status = Waybill.STATUS_VOIDED
    waybill.save(update_fields=["status", "updated_at"])
    WaybillEvent.objects.create(
        waybill=waybill, event_type="split", event_time=now, resource=waybill.waybill_no,
        source="split", payload={"children": [c.waybill_no for c in children]},
    )
    publish_event("waybill_split", {"waybill_no": waybill.waybill_no, "children": [c.waybill_no for c in children]})
    return children


def merge_waybills(waybills: list[Waybill], *, operator=None, route_name: str = "") -> Waybill:
    """把多张运单合并为一张；源单作废并指向合并单（血缘），货量汇总。"""
    if len(waybills) < 2:
        raise AppError("INVALID_MERGE", "合单至少需要 2 张运单。", status=400)
    for w in waybills:
        if w.status not in _SPLITTABLE_FROM:
            raise AppError("INVALID_MERGE", f"{w.waybill_no} 非待调度前状态，不可合单。", status=409)

    now = timezone.now()
    first = waybills[0]
    merged = Waybill.objects.create(
        waybill_no=f"{first.waybill_no}-M",
        status=Waybill.STATUS_PENDING_DISPATCH,
        cargo_quantity=sum(w.cargo_quantity for w in waybills),
        cargo_weight_ton=sum((w.cargo_weight_ton for w in waybills), start=Decimal("0")),
        cargo_volume_cbm=sum((w.cargo_volume_cbm for w in waybills), start=Decimal("0")),
        **{**_copy_fields(first), "route_name": route_name or first.route_name},
    )
    for w in waybills:
        w.status = Waybill.STATUS_VOIDED
        w.parent = merged
        w.save(update_fields=["status", "parent", "updated_at"])
        WaybillEvent.objects.create(
            waybill=w, event_type="merged_into", event_time=now, resource=merged.waybill_no,
            source="merge", payload={"merged": merged.waybill_no},
        )
    WaybillEvent.objects.create(
        waybill=merged, event_type="merge", event_time=now, resource=merged.waybill_no,
        source="merge", payload={"sources": [w.waybill_no for w in waybills]},
    )
    publish_event("waybill_merge", {"waybill_no": merged.waybill_no, "sources": [w.waybill_no for w in waybills]})
    return merged
