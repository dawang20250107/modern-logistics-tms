"""运单状态机与执行服务。"""

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
    waybill.save(update_fields=["status", "updated_at"])
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
    # 对外 Webhook 事件（懒导入避免应用间循环依赖）
    from apps.finance.services import emit_event

    emit_event("waybill.status_changed", {"waybill_no": waybill.waybill_no, "from": from_status, "to": to_status})
    return waybill
