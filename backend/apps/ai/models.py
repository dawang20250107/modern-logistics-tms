"""AI 建议落库：AI 只识别/推荐/解释/预警/草拟，高风险动作由人工确认。"""

from django.conf import settings
from django.db import models

from apps.core.models import BaseModel


class AgentSuggestion(BaseModel):
    STATUS_PENDING = "pending"
    STATUS_ACCEPTED = "accepted"
    STATUS_REJECTED = "rejected"
    STATUS_CHOICES = [
        (STATUS_PENDING, "待确认"),
        (STATUS_ACCEPTED, "已采纳"),
        (STATUS_REJECTED, "已驳回"),
    ]

    waybill = models.ForeignKey(
        "ops.Waybill",
        null=True,
        blank=True,
        on_delete=models.CASCADE,
        related_name="agent_suggestions",
    )
    suggestion_type = models.CharField(max_length=64)
    title = models.CharField(max_length=160)
    body = models.TextField()
    status = models.CharField(max_length=24, default=STATUS_PENDING, choices=STATUS_CHOICES)
    evidence = models.JSONField(default=dict, blank=True)
    tool_name = models.CharField(max_length=64, blank=True)
    confirmed_by = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        null=True,
        blank=True,
        on_delete=models.SET_NULL,
        related_name="confirmed_suggestions",
    )
    confirmed_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "ai_agent_suggestion"
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["suggestion_type", "status"])]
        verbose_name = "AI 建议"
        verbose_name_plural = "AI 建议"

    def __str__(self) -> str:
        return f"{self.suggestion_type}:{self.title}"
