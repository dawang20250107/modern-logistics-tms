"""轨迹削峰落库与在途预警的异步任务。"""

import json

from celery import shared_task
from django.utils import timezone
from django.utils.dateparse import parse_datetime

from apps.core.redis import get_redis, publish_event

from .models import TrackingPoint, Waybill

TRACKING_QUEUE = "tms:tracking:queue"


@shared_task(name="ops.flush_tracking_points")
def flush_tracking_points(batch: int = 1000) -> int:
    """从 Redis 队列批量取出轨迹点并落库（削峰）。"""
    r = get_redis()
    items = r.lpop(TRACKING_QUEUE, batch)
    if not items:
        return 0
    if isinstance(items, str):
        items = [items]

    parsed = [json.loads(i) for i in items]
    wbnos = {p.get("waybill_no") for p in parsed if p.get("waybill_no")}
    waybills = {w.waybill_no: w for w in Waybill.objects.filter(waybill_no__in=wbnos)}

    objs = []
    for p in parsed:
        waybill = waybills.get(p.get("waybill_no"))
        if waybill is None:
            continue
        objs.append(
            TrackingPoint(
                waybill=waybill,
                lng=p.get("lng"),
                lat=p.get("lat"),
                speed_kmh=p.get("speed_kmh") or 0,
                reported_at=parse_datetime(p.get("reported_at") or "") or timezone.now(),
                provider=p.get("provider", ""),
            )
        )
    TrackingPoint.objects.bulk_create(objs, batch_size=500)
    # 点位到达自动化：新轨迹点做围栏判定，盖到达/离开戳
    try:
        from .geofence import process_points

        process_points([(o.waybill, o.lat, o.lng, o.reported_at) for o in objs])
    except Exception:  # noqa: BLE001 — 围栏失败不阻断轨迹落库
        pass
    return len(objs)


@shared_task(name="ops.scan_eta_risks")
def scan_eta_risks(medium_minutes: int = 120, high_minutes: int = 240) -> int:
    """扫描在途运单，按 ETA 偏移更新风险等级并生成预警建议。"""
    from apps.ai.models import AgentSuggestion

    risky = Waybill.objects.filter(
        status=Waybill.STATUS_IN_TRANSIT, eta_drift_minutes__gte=medium_minutes
    )
    created = 0
    for waybill in risky:
        level = Waybill.RISK_HIGH if waybill.eta_drift_minutes >= high_minutes else Waybill.RISK_MEDIUM
        if waybill.risk_level != level:
            waybill.risk_level = level
            waybill.save(update_fields=["risk_level", "updated_at"])

        if AgentSuggestion.objects.filter(
            waybill=waybill, suggestion_type="eta_risk", status=AgentSuggestion.STATUS_PENDING
        ).exists():
            continue

        AgentSuggestion.objects.create(
            waybill=waybill,
            suggestion_type="eta_risk",
            title="ETA 风险预警",
            body=f"{waybill.waybill_no} ETA 偏移 {waybill.eta_drift_minutes} 分钟，建议核实并通知客户。",
            tool_name="scan.eta",
            evidence={"waybill_no": waybill.waybill_no, "eta_drift_minutes": waybill.eta_drift_minutes},
        )
        publish_event(
            "risk",
            {"waybill_no": waybill.waybill_no, "risk_level": level, "eta_drift_minutes": waybill.eta_drift_minutes},
        )
        created += 1
    return created


@shared_task(name="ops.scan_receipt_reminders")
def scan_receipt_reminders() -> int:
    """扫描待回单运单，生成回单催收建议。"""
    from apps.ai.models import AgentSuggestion

    created = 0
    for waybill in Waybill.objects.filter(receipt_status="pending"):
        if AgentSuggestion.objects.filter(
            waybill=waybill, suggestion_type="receipt_reminder", status=AgentSuggestion.STATUS_PENDING
        ).exists():
            continue
        AgentSuggestion.objects.create(
            waybill=waybill,
            suggestion_type="receipt_reminder",
            title="回单催收",
            body=f"{waybill.waybill_no} 回单待上传，建议提醒承运商上传并触发 OCR。",
            tool_name="scan.receipt",
            evidence={"receipt_status": waybill.receipt_status},
        )
        created += 1
    return created


@shared_task(name="ops.process_receipt_ocr")
def process_receipt_ocr(receipt_id: str) -> bool:
    """异步处理回单 OCR（可插拔引擎）。"""
    from .models import Receipt
    from .ocr import run_ocr

    receipt = Receipt.objects.filter(id=receipt_id).select_related("waybill").first()
    if receipt is None:
        return False
    receipt.ocr_status = "processing"
    receipt.save(update_fields=["ocr_status", "updated_at"])
    try:
        result = run_ocr(receipt)
        receipt.ocr_result = result
        receipt.signatory = result.get("fields", {}).get("signatory", "") or receipt.signatory
        receipt.ocr_status = "done"
        receipt.save(update_fields=["ocr_result", "signatory", "ocr_status", "updated_at"])
        publish_event(
            "receipt_ocr",
            {"waybill_no": receipt.waybill.waybill_no, "receipt_id": str(receipt.id), "status": "done"},
        )
        return True
    except Exception:  # noqa: BLE001
        receipt.ocr_status = "failed"
        receipt.save(update_fields=["ocr_status", "updated_at"])
        return False


@shared_task(name="ops.scan_sla_breaches")
def scan_sla_breaches(at_risk_minutes: int = 120) -> int:
    """扫描进行中订单的 SLA：超过承诺到达时间标超时、临近标临期，并通知建单人。"""
    from datetime import timedelta

    from apps.notifications.services import notify_users

    from .models import Order

    now = timezone.now()
    in_progress = [Order.STATUS_POOLED, Order.STATUS_DISPATCHING, Order.STATUS_CONVERTED]
    changed = 0
    qs = Order.objects.filter(
        status__in=in_progress, expected_delivery_at__isnull=False,
        sla_status__in=[Order.SLA_PENDING, Order.SLA_AT_RISK],
    )
    for order in qs:
        if now > order.expected_delivery_at:
            new_status = Order.SLA_BREACHED
        elif now > order.expected_delivery_at - timedelta(minutes=at_risk_minutes):
            new_status = Order.SLA_AT_RISK
        else:
            continue
        if order.sla_status == new_status:
            continue
        order.sla_status = new_status
        order.save(update_fields=["sla_status", "updated_at"])
        publish_event("order_sla", {"order_no": order.order_no, "sla_status": new_status})
        if order.created_by_id:
            notify_users(
                [order.created_by_id], category="order_sla",
                title=f"订单时效{'超时' if new_status == Order.SLA_BREACHED else '临期'}：{order.order_no}",
                body=f"承诺到达 {order.expected_delivery_at:%Y-%m-%d %H:%M}",
                level="critical" if new_status == Order.SLA_BREACHED else "warning",
                link_type="order", link_id=str(order.id),
            )
        changed += 1
    return changed
