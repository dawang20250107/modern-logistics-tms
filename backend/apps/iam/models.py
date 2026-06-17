"""组织树 + RBAC（角色/权限/分配）+ 数据权限范围 + 对外 API 密钥。"""

import secrets

from django.conf import settings
from django.db import models

from apps.core.models import BaseModel


class Organization(BaseModel):
    TYPE_CHOICES = [
        ("group", "集团"),
        ("company", "公司"),
        ("dept", "部门"),
        ("station", "网点"),
    ]

    name = models.CharField(max_length=120)
    code = models.CharField(max_length=64, unique=True)
    type = models.CharField(max_length=16, choices=TYPE_CHOICES, default="dept")
    parent = models.ForeignKey(
        "self", null=True, blank=True, on_delete=models.SET_NULL, related_name="children"
    )
    # 物化路径（"祖先id/.../自身id"），便于按组织子树做数据权限过滤
    path = models.CharField(max_length=512, blank=True, db_index=True)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "iam_organization"
        ordering = ["code"]
        verbose_name = "组织"
        verbose_name_plural = "组织"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"

    def save(self, *args, **kwargs):
        if self.parent_id:
            base = self.parent.path or str(self.parent_id)
            self.path = f"{base}/{self.id}"
        else:
            self.path = str(self.id)
        super().save(*args, **kwargs)

    def descendant_filter(self):
        """返回用于过滤本组织及全部下级的 Q 路径前缀。"""
        return f"{self.path}/"


class Permission(BaseModel):
    """应用级权限点（区别于 Django 内置 auth.Permission），如 waybill.view。"""

    code = models.CharField(max_length=128, unique=True)
    name = models.CharField(max_length=128)
    module = models.CharField(max_length=64, blank=True)

    class Meta:
        db_table = "iam_permission"
        ordering = ["code"]
        verbose_name = "权限点"
        verbose_name_plural = "权限点"

    def __str__(self) -> str:
        return self.code


class Role(BaseModel):
    DATA_SCOPE_CHOICES = [
        ("self", "仅本人"),
        ("org", "本组织"),
        ("org_sub", "本组织及下级"),
        ("all", "全部"),
    ]

    code = models.CharField(max_length=64, unique=True)
    name = models.CharField(max_length=64)
    data_scope = models.CharField(max_length=16, choices=DATA_SCOPE_CHOICES, default="org_sub")
    permissions = models.ManyToManyField(Permission, blank=True, related_name="roles")
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "iam_role"
        ordering = ["code"]
        verbose_name = "角色"
        verbose_name_plural = "角色"

    def __str__(self) -> str:
        return self.code


class RoleAssignment(BaseModel):
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="role_assignments"
    )
    role = models.ForeignKey(Role, on_delete=models.CASCADE, related_name="assignments")
    organization = models.ForeignKey(
        Organization, null=True, blank=True, on_delete=models.SET_NULL, related_name="role_assignments"
    )

    class Meta:
        db_table = "iam_role_assignment"
        unique_together = [("user", "role", "organization")]
        verbose_name = "角色分配"
        verbose_name_plural = "角色分配"

    def __str__(self) -> str:
        return f"{self.user_id}:{self.role_id}"


class ApiKey(BaseModel):
    """对外系统 HMAC 鉴权密钥。

    secret 需服务端持有以验签（HMAC 非单向）；生产应做 KMS/列加密，此为后续项。
    """

    name = models.CharField(max_length=120)
    key_id = models.CharField(max_length=40, unique=True)
    secret = models.CharField(max_length=80)
    organization = models.ForeignKey(
        Organization, null=True, blank=True, on_delete=models.SET_NULL, related_name="api_keys"
    )
    scopes = models.CharField(max_length=255, blank=True, help_text="逗号分隔权限点；* 表示全部")
    is_active = models.BooleanField(default=True)
    last_used_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "iam_api_key"
        ordering = ["-created_at"]
        verbose_name = "API 密钥"
        verbose_name_plural = "API 密钥"

    def __str__(self) -> str:
        return f"{self.name}({self.key_id})"

    @staticmethod
    def generate_pair() -> tuple[str, str]:
        return secrets.token_hex(8), secrets.token_urlsafe(32)
