"""组织中台序列化器：组织 / 部门 / 用户组 / 员工 / 服务区划 / 账号移交。"""

from rest_framework import serializers

from .models import (
    AccountHandover,
    Department,
    Employee,
    EmployeeGroup,
    LoginAttempt,
    Organization,
    Permission,
    Role,
    RoleAssignment,
    ServiceArea,
)


class PermissionSerializer(serializers.ModelSerializer):
    class Meta:
        model = Permission
        fields = ["id", "code", "name", "module"]


class RoleSerializer(serializers.ModelSerializer):
    data_scope_label = serializers.CharField(source="get_data_scope_display", read_only=True)
    permission_codes = serializers.SerializerMethodField()
    permission_count = serializers.SerializerMethodField()

    class Meta:
        model = Role
        fields = [
            "id", "code", "name", "data_scope", "data_scope_label",
            "permissions", "permission_codes", "permission_count", "is_active",
        ]
        extra_kwargs = {"permissions": {"required": False}}

    def get_permission_codes(self, obj):
        return list(obj.permissions.values_list("code", flat=True))

    def get_permission_count(self, obj):
        return obj.permissions.count()


class RoleAssignmentSerializer(serializers.ModelSerializer):
    role_code = serializers.CharField(source="role.code", read_only=True)
    role_name = serializers.CharField(source="role.name", read_only=True)
    username = serializers.CharField(source="user.username", read_only=True, default="")
    organization_name = serializers.CharField(source="organization.name", read_only=True, default="")

    class Meta:
        model = RoleAssignment
        fields = ["id", "user", "username", "role", "role_code", "role_name", "organization", "organization_name"]


class OrganizationSerializer(serializers.ModelSerializer):
    type_label = serializers.CharField(source="get_type_display", read_only=True)
    org_property_label = serializers.CharField(source="get_org_property_display", read_only=True)
    parent_name = serializers.CharField(source="parent.name", read_only=True, default="")

    class Meta:
        model = Organization
        fields = [
            "id", "name", "short_name", "code", "type", "type_label",
            "org_property", "org_property_label", "parent", "parent_name", "path",
            "province", "city", "district", "address", "lng", "lat",
            "manager_name", "manager_phone", "business_phone", "service_phone",
            "complaint_phone", "receipt_return_address", "sort_order", "is_active",
        ]
        read_only_fields = ["path"]


class EmployeeGroupSerializer(serializers.ModelSerializer):
    member_count = serializers.SerializerMethodField()

    class Meta:
        model = EmployeeGroup
        fields = ["id", "code", "name", "description", "roles", "is_active", "member_count"]

    def get_member_count(self, obj):
        # 详情才统计，列表跳过避免逐行查询
        view = self.context.get("view")
        if getattr(view, "action", None) == "list":
            return None
        return obj.employees.count()


class DepartmentSerializer(serializers.ModelSerializer):
    organization_name = serializers.CharField(source="organization.name", read_only=True, default="")
    manager_name = serializers.CharField(source="manager.name", read_only=True, default="")
    parent_name = serializers.CharField(source="parent.name", read_only=True, default="")

    class Meta:
        model = Department
        fields = [
            "id", "organization", "organization_name", "parent", "parent_name",
            "code", "name", "manager", "manager_name", "sort_order", "is_active",
        ]


class EmployeeSerializer(serializers.ModelSerializer):
    organization_name = serializers.CharField(source="organization.name", read_only=True, default="")
    department_name = serializers.CharField(source="department.name", read_only=True, default="")
    supervisor_name = serializers.CharField(source="supervisor.name", read_only=True, default="")
    status_label = serializers.CharField(source="get_status_display", read_only=True)
    username = serializers.CharField(source="user.username", read_only=True, default="")
    account_active = serializers.BooleanField(read_only=True)
    group_names = serializers.SerializerMethodField()

    class Meta:
        model = Employee
        fields = [
            "id", "employee_no", "name", "phone", "email", "id_no",
            "organization", "organization_name", "department", "department_name",
            "supervisor", "supervisor_name", "groups", "group_names", "position",
            "status", "status_label", "hire_date", "leave_date",
            "user", "username", "account_active",
        ]

    def get_group_names(self, obj):
        view = self.context.get("view")
        if getattr(view, "action", None) == "list":
            return None
        return list(obj.groups.values_list("name", flat=True))


class ServiceAreaSerializer(serializers.ModelSerializer):
    organization_name = serializers.CharField(source="organization.name", read_only=True, default="")
    area_type_label = serializers.CharField(source="get_area_type_display", read_only=True)

    class Meta:
        model = ServiceArea
        fields = [
            "id", "organization", "organization_name", "area_type", "area_type_label",
            "province", "city", "district", "region_code", "region_name",
            "priority", "note", "is_active",
        ]


class LoginAttemptSerializer(serializers.ModelSerializer):
    result_label = serializers.CharField(source="get_result_display", read_only=True)
    username_display = serializers.CharField(source="username", read_only=True)

    class Meta:
        model = LoginAttempt
        fields = [
            "id", "username", "username_display", "user", "success",
            "result", "result_label", "ip", "user_agent", "created_at",
        ]
        read_only_fields = fields


class AccountHandoverSerializer(serializers.ModelSerializer):
    from_name = serializers.CharField(source="from_employee.name", read_only=True, default="")
    to_name = serializers.CharField(source="to_employee.name", read_only=True, default="")
    operator_name = serializers.CharField(source="operator.username", read_only=True, default="")

    class Meta:
        model = AccountHandover
        fields = [
            "id", "from_employee", "from_name", "to_employee", "to_name",
            "operator_name", "reason", "moved_reports", "moved_departments",
            "disabled_account", "created_at",
        ]
        read_only_fields = fields


# ── 自助账户：注册 / 资料 / 改密（个人中心） ─────────────────────
from django.contrib.auth import get_user_model, password_validation  # noqa: E402


class RegisterSerializer(serializers.Serializer):
    """自助注册：仅创建基础账号；组织与角色由管理员在组织中台分配（不自授权限）。"""

    username = serializers.CharField(max_length=150)
    nickname = serializers.CharField(max_length=64, required=False, allow_blank=True)
    phone = serializers.CharField(max_length=32, required=False, allow_blank=True)
    password = serializers.CharField(write_only=True, style={"input_type": "password"})

    def validate_username(self, value):
        value = (value or "").strip()
        if not value:
            raise serializers.ValidationError("用户名不能为空")
        if get_user_model().objects.filter(username__iexact=value).exists():
            raise serializers.ValidationError("该用户名已被占用")
        return value

    def validate_password(self, value):
        password_validation.validate_password(value)
        return value

    def create(self, validated_data):
        user = get_user_model()(
            username=validated_data["username"],
            nickname=validated_data.get("nickname", ""),
            phone=validated_data.get("phone", ""),
            is_active=True,
        )
        user.set_password(validated_data["password"])
        user.save()
        return user


class ProfileUpdateSerializer(serializers.Serializer):
    """本人资料自助维护（仅昵称/手机号/邮箱；不含组织、角色、启停等敏感字段）。"""

    nickname = serializers.CharField(max_length=64, required=False, allow_blank=True)
    phone = serializers.CharField(max_length=32, required=False, allow_blank=True)
    email = serializers.EmailField(required=False, allow_blank=True)


class ChangePasswordSerializer(serializers.Serializer):
    old_password = serializers.CharField(write_only=True)
    new_password = serializers.CharField(write_only=True)

    def validate_old_password(self, value):
        if not self.context["request"].user.check_password(value):
            raise serializers.ValidationError("当前密码不正确")
        return value

    def validate_new_password(self, value):
        password_validation.validate_password(value, self.context["request"].user)
        return value

    def validate(self, attrs):
        if attrs.get("old_password") and attrs.get("old_password") == attrs.get("new_password"):
            raise serializers.ValidationError({"new_password": "新密码不能与当前密码相同"})
        return attrs


class PasswordResetConfirmSerializer(serializers.Serializer):
    identifier = serializers.CharField()
    code = serializers.CharField(max_length=6, min_length=6)
    new_password = serializers.CharField(write_only=True)

    def validate_new_password(self, value):
        password_validation.validate_password(value)
        return value
