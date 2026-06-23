"""作业提醒：调度下发提醒给司机（富文本模板），司机端强制确认收到。

下发即推送司机端（微信/飞书为外部接入·预留）；司机点击「确认收到」记录确认时间。
"""

from django.utils import timezone

from apps.core.exceptions import AppError
from apps.core.redis import publish_event

from .models import DriverReminder, WaybillEvent


def send_reminder(waybill, *, template=None, title="", content="", ack_required=True, operator=None) -> DriverReminder:
    """向运单司机下发作业提醒（取自模板或自定义内容）。"""
    body = content or (template.content if template else "")
    if not body.strip():
        raise AppError("REMINDER_EMPTY", "提醒内容不能为空。", status=400)
    reminder = DriverReminder.objects.create(
        waybill=waybill,
        driver=waybill.driver,
        template=template,
        title=title or (template.name if template else "作业提醒"),
        content=body,
        ack_required=ack_required,
        sent_by=operator if operator and getattr(operator, "is_authenticated", False) else None,
    )
    # 推送司机端（微信/飞书预留）
    try:
        from apps.integrations.wechat import notify_customer  # 复用预留通道

        notify_customer(waybill, body)
    except Exception:  # noqa: BLE001
        pass
    WaybillEvent.objects.create(
        waybill=waybill, event_type="reminder_sent", event_time=timezone.now(),
        resource=waybill.waybill_no, source="dispatch",
        payload={"reminder_id": str(reminder.id), "title": reminder.title, "ack_required": ack_required},
    )
    publish_event("reminder_sent", {"waybill_no": waybill.waybill_no, "driver_id": str(waybill.driver_id or "")})
    return reminder


def acknowledge_reminder(reminder) -> DriverReminder:
    """司机确认收到提醒。"""
    if reminder.status == DriverReminder.STATUS_ACKNOWLEDGED:
        return reminder
    reminder.status = DriverReminder.STATUS_ACKNOWLEDGED
    reminder.acknowledged_at = timezone.now()
    reminder.save(update_fields=["status", "acknowledged_at", "updated_at"])
    if reminder.waybill_id:
        WaybillEvent.objects.create(
            waybill=reminder.waybill, event_type="reminder_acknowledged", event_time=timezone.now(),
            resource=reminder.waybill.waybill_no, source="driver",
            payload={"reminder_id": str(reminder.id)},
        )
        publish_event("reminder_acknowledged", {"waybill_no": reminder.waybill.waybill_no})
    return reminder
