"""组织树 + RBAC（角色/权限/分配）+ 数据权限范围 + 对外 API 密钥。"""

import secrets

from django.conf import settings
from django.db import models

from apps.core.models import BaseModel


class Organization(BaseModel):
    TYPE_CHOICES = [
        ("group", "集团"),
        ("company", "公司"),
        ("region", "片区"),
        ("dept", "部门"),
        ("station", "网点"),
    ]
    PROPERTY_CHOICES = [
        ("self", "自营"),
        ("franchise", "加盟"),
        ("outsource", "外包"),
        ("partner", "合作"),
        ("jv", "合资"),
    ]

    name = models.CharField(max_length=120)
    short_name = models.CharField(max_length=64, blank=True, help_text="组织简称")
    code = models.CharField(max_length=64, unique=True)
    type = models.CharField(max_length=16, choices=TYPE_CHOICES, default="dept")
    org_property = models.CharField(
        max_length=16, choices=PROPERTY_CHOICES, default="self", help_text="经营属性"
    )
    parent = models.ForeignKey(
        "self", null=True, blank=True, on_delete=models.SET_NULL, related_name="children"
    )
    # 物化路径（"祖先id/.../自身id"），便于按组织子树做数据权限过滤
    path = models.CharField(max_length=512, blank=True, db_index=True)

    # ── 落地属性：结构化地址 + 坐标 + 多类联系电话 + 回单地址 ──
    province = models.CharField(max_length=32, blank=True)
    city = models.CharField(max_length=32, blank=True)
    district = models.CharField(max_length=32, blank=True)
    address = models.CharField(max_length=255, blank=True)
    lng = models.DecimalField(max_digits=9, decimal_places=6, null=True, blank=True)
    lat = models.DecimalField(max_digits=9, decimal_places=6, null=True, blank=True)
    manager_name = models.CharField(max_length=64, blank=True, help_text="负责人")
    manager_phone = models.CharField(max_length=32, blank=True)
    business_phone = models.CharField(max_length=32, blank=True, help_text="业务电话")
    service_phone = models.CharField(max_length=32, blank=True, help_text="客服电话")
    complaint_phone = models.CharField(max_length=32, blank=True, help_text="投诉电话")
    receipt_return_address = models.CharField(
        max_length=255, blank=True, help_text="司机回单寄回地址"
    )
    sort_order = models.IntegerField(default=0)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "iam_organization"
        ordering = ["sort_order", "code"]
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


# ── 组织中台：部门 / 员工 / 用户组 / 服务区划 / 账号移交 ─────────────


class EmployeeGroup(BaseModel):
    """用户组：用于权限批量授予与通知分发，并可挂载角色。"""

    code = models.CharField(max_length=64, unique=True)
    name = models.CharField(max_length=64)
    description = models.CharField(max_length=255, blank=True)
    roles = models.ManyToManyField(Role, blank=True, related_name="employee_groups")
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "iam_employee_group"
        ordering = ["code"]
        verbose_name = "用户组"
        verbose_name_plural = "用户组"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"


class Department(BaseModel):
    """部门：组织内的二级编制树（与组织树解耦，可独立挂接员工与负责人）。"""

    organization = models.ForeignKey(
        Organization, on_delete=models.CASCADE, related_name="departments"
    )
    parent = models.ForeignKey(
        "self", null=True, blank=True, on_delete=models.SET_NULL, related_name="children"
    )
    code = models.CharField(max_length=64)
    name = models.CharField(max_length=120)
    manager = models.ForeignKey(
        "Employee", null=True, blank=True, on_delete=models.SET_NULL, related_name="managed_departments"
    )
    sort_order = models.IntegerField(default=0)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "iam_department"
        ordering = ["sort_order", "code"]
        unique_together = [("organization", "code")]
        verbose_name = "部门"
        verbose_name_plural = "部门"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"


class Employee(BaseModel):
    """员工档案：工号 + 汇报线（直接上级）+ 用户组 + 账号生命周期。"""

    STATUS_CHOICES = [
        ("active", "在职"),
        ("disabled", "停用"),
        ("left", "离职"),
    ]

    user = models.OneToOneField(
        settings.AUTH_USER_MODEL, null=True, blank=True,
        on_delete=models.SET_NULL, related_name="employee",
    )
    employee_no = models.CharField(max_length=32, unique=True, help_text="工号")
    name = models.CharField(max_length=64)
    phone = models.CharField(max_length=32, blank=True, db_index=True)
    email = models.EmailField(blank=True)
    id_no = models.CharField(max_length=32, blank=True)
    organization = models.ForeignKey(
        Organization, null=True, blank=True, on_delete=models.SET_NULL, related_name="employees"
    )
    department = models.ForeignKey(
        Department, null=True, blank=True, on_delete=models.SET_NULL, related_name="employees"
    )
    supervisor = models.ForeignKey(
        "self", null=True, blank=True, on_delete=models.SET_NULL, related_name="reports",
        help_text="直接上级",
    )
    groups = models.ManyToManyField(EmployeeGroup, blank=True, related_name="employees")
    position = models.CharField(max_length=64, blank=True, help_text="职位")
    status = models.CharField(max_length=16, choices=STATUS_CHOICES, default="active")
    hire_date = models.DateField(null=True, blank=True)
    leave_date = models.DateField(null=True, blank=True)

    class Meta:
        db_table = "iam_employee"
        ordering = ["employee_no"]
        indexes = [
            models.Index(fields=["organization", "status"]),
            models.Index(fields=["status"]),
        ]
        verbose_name = "员工"
        verbose_name_plural = "员工"

    def __str__(self) -> str:
        return f"{self.employee_no} {self.name}"

    @property
    def account_active(self) -> bool:
        return bool(self.user and self.user.is_active)


class ServiceArea(BaseModel):
    """服务区划：网点的派送/中转/特殊/不派送/不中转覆盖范围（接单与派单路由依据）。"""

    AREA_TYPE_CHOICES = [
        ("deliver", "派送区域"),
        ("transfer", "中转区域"),
        ("special", "特殊区域"),
        ("no_deliver", "不派送区域"),
        ("no_transfer", "不中转区域"),
    ]

    organization = models.ForeignKey(
        Organization, on_delete=models.CASCADE, related_name="service_areas"
    )
    area_type = models.CharField(max_length=16, choices=AREA_TYPE_CHOICES, default="deliver")
    province = models.CharField(max_length=32, blank=True)
    city = models.CharField(max_length=32, blank=True)
    district = models.CharField(max_length=32, blank=True)
    region_code = models.CharField(max_length=16, blank=True, help_text="行政区划编码")
    region_name = models.CharField(max_length=120, help_text="区划展示名")
    priority = models.IntegerField(default=0, help_text="多网点覆盖时的优先级，越大越优先")
    note = models.CharField(max_length=255, blank=True)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "iam_service_area"
        ordering = ["-priority", "region_name"]
        indexes = [
            models.Index(fields=["organization", "area_type"]),
            models.Index(fields=["region_code"]),
        ]
        verbose_name = "服务区划"
        verbose_name_plural = "服务区划"

    def __str__(self) -> str:
        return f"{self.get_area_type_display()}:{self.region_name}"


class LoginAttempt(BaseModel):
    """登录审计：每次登录尝试（成功/失败）留痕，供安全审计与失败锁定判定。

    失败锁定的计数用 Redis 短缓存做（高频、可自动过期），此表是可追溯的持久流水。
    """

    RESULT_SUCCESS = "success"
    RESULT_BAD_CREDENTIALS = "bad_credentials"
    RESULT_INACTIVE = "inactive"
    RESULT_LOCKED = "locked"
    RESULT_CHOICES = [
        (RESULT_SUCCESS, "成功"),
        (RESULT_BAD_CREDENTIALS, "凭据错误"),
        (RESULT_INACTIVE, "账号停用"),
        (RESULT_LOCKED, "已锁定"),
    ]

    username = models.CharField(max_length=150, db_index=True)
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL,
        related_name="login_attempts",
    )
    success = models.BooleanField(default=False)
    result = models.CharField(max_length=32, choices=RESULT_CHOICES, default=RESULT_BAD_CREDENTIALS)
    ip = models.GenericIPAddressField(null=True, blank=True)
    user_agent = models.CharField(max_length=255, blank=True)

    class Meta:
        db_table = "iam_login_attempt"
        ordering = ["-created_at"]
        indexes = [
            models.Index(fields=["username", "created_at"]),
            models.Index(fields=["success", "created_at"]),
        ]
        verbose_name = "登录审计"
        verbose_name_plural = "登录审计"

    def __str__(self) -> str:
        return f"{self.username} {self.result} @{self.created_at:%Y-%m-%d %H:%M}"


class AccountHandover(BaseModel):
    """账号移交：将离职/转岗员工的下属、所辖部门改挂他人，并停用原账号（留痕可审计）。"""

    from_employee = models.ForeignKey(
        Employee, on_delete=models.CASCADE, related_name="handovers_out"
    )
    to_employee = models.ForeignKey(
        Employee, on_delete=models.CASCADE, related_name="handovers_in"
    )
    operator = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="+"
    )
    reason = models.CharField(max_length=255, blank=True)
    moved_reports = models.IntegerField(default=0)
    moved_departments = models.IntegerField(default=0)
    disabled_account = models.BooleanField(default=False)

    class Meta:
        db_table = "iam_account_handover"
        ordering = ["-created_at"]
        verbose_name = "账号移交"
        verbose_name_plural = "账号移交"

    def __str__(self) -> str:
        return f"{self.from_employee_id}→{self.to_employee_id}"
