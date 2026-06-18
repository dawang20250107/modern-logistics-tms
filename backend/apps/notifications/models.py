"""站内通知：按收件人持久化，支持未读计数与已读标记。"""

from django.conf import settings
from django.db import models

from apps.core.models import BaseModel


class Notification(BaseModel):
    LEVEL_INFO = "info"
    LEVEL_WARNING = "warning"
    LEVEL_CRITICAL = "critical"
    LEVEL_CHOICES = [(LEVEL_INFO, "信息"), (LEVEL_WARNING, "提醒"), (LEVEL_CRITICAL, "重要")]

    recipient = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="notifications"
    )
    category = models.CharField(max_length=48, db_index=True)
    title = models.CharField(max_length=160)
    body = models.CharField(max_length=255, blank=True)
    level = models.CharField(max_length=16, choices=LEVEL_CHOICES, default=LEVEL_INFO)
    link_type = models.CharField(max_length=32, blank=True, help_text="order/waybill/alert")
    link_id = models.CharField(max_length=64, blank=True)
    payload = models.JSONField(default=dict, blank=True)
    is_read = models.BooleanField(default=False, db_index=True)
    read_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "ntf_notification"
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["recipient", "is_read"])]
        verbose_name = "通知"
        verbose_name_plural = "通知"

    def __str__(self) -> str:
        return f"{self.recipient_id}:{self.title}"
