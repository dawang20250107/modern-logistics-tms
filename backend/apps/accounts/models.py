from django.contrib.auth.models import AbstractUser
from django.db import models

from apps.core.ids import uuid7


class User(AbstractUser):
    """自定义用户：UUIDv7 主键 + 手机号 + 组织归属。"""

    id = models.UUIDField(primary_key=True, default=uuid7, editable=False)
    phone = models.CharField(max_length=32, blank=True, db_index=True)
    nickname = models.CharField(max_length=64, blank=True)
    avatar = models.ImageField(upload_to="avatars/", null=True, blank=True)
    # 个人偏好（默认首页 / 列表密度 / 每页条数 / 通知开关等，前端按需读取）
    preferences = models.JSONField(default=dict, blank=True)
    organization = models.ForeignKey(
        "iam.Organization",
        null=True,
        blank=True,
        on_delete=models.SET_NULL,
        related_name="members",
    )

    class Meta:
        db_table = "accounts_user"
        verbose_name = "用户"
        verbose_name_plural = "用户"

    def __str__(self) -> str:
        return self.username
