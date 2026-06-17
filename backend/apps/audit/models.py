from django.conf import settings
from django.db import models

from apps.core.models import BaseModel


class AuditLog(BaseModel):
    """操作审计：谁在什么 RequestID 下，对哪个资源做了什么动作。

    AI 建议与高风险动作的人工确认也通过此表留痕，保证可追溯。
    """

    actor = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        null=True,
        blank=True,
        on_delete=models.SET_NULL,
        related_name="audit_logs",
    )
    action = models.CharField(max_length=128)
    resource_type = models.CharField(max_length=64, blank=True)
    resource_id = models.CharField(max_length=64, blank=True)
    request_id = models.CharField(max_length=64, blank=True, db_index=True)
    method = models.CharField(max_length=8, blank=True)
    path = models.CharField(max_length=255, blank=True)
    status_code = models.IntegerField(null=True, blank=True)
    ip = models.GenericIPAddressField(null=True, blank=True)
    payload = models.JSONField(default=dict, blank=True)

    class Meta:
        db_table = "audit_log"
        ordering = ["-created_at"]
        indexes = [
            models.Index(fields=["resource_type", "resource_id"]),
            models.Index(fields=["action", "created_at"]),
        ]
        verbose_name = "审计日志"
        verbose_name_plural = "审计日志"

    def __str__(self) -> str:
        return f"{self.action} {self.resource_type}:{self.resource_id}"
