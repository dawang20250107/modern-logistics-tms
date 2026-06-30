"""组织中台序列化器：组织 / 部门 / 用户组 / 员工 / 服务区划 / 账号移交。"""

from rest_framework import serializers

from .models import (
    AccountHandover,
    Department,
    Employee,
    EmployeeGroup,
    Organization,
    ServiceArea,
)


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
