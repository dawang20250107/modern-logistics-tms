from django.contrib import admin

from .models import (
    AccountHandover,
    ApiKey,
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


@admin.register(Organization)
class OrganizationAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "type", "org_property", "parent", "manager_name", "is_active")
    search_fields = ("code", "name", "short_name", "manager_name")
    list_filter = ("type", "org_property", "is_active")


@admin.register(Department)
class DepartmentAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "organization", "parent", "manager", "is_active")
    search_fields = ("code", "name")
    list_filter = ("organization", "is_active")


@admin.register(EmployeeGroup)
class EmployeeGroupAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "is_active")
    search_fields = ("code", "name")
    filter_horizontal = ("roles",)


@admin.register(Employee)
class EmployeeAdmin(admin.ModelAdmin):
    list_display = ("employee_no", "name", "phone", "organization", "department", "supervisor", "status")
    search_fields = ("employee_no", "name", "phone")
    list_filter = ("status", "organization")
    filter_horizontal = ("groups",)
    raw_id_fields = ("user", "supervisor", "department")


@admin.register(ServiceArea)
class ServiceAreaAdmin(admin.ModelAdmin):
    list_display = ("region_name", "area_type", "organization", "priority", "is_active")
    search_fields = ("region_name", "region_code")
    list_filter = ("area_type", "is_active", "organization")


@admin.register(AccountHandover)
class AccountHandoverAdmin(admin.ModelAdmin):
    list_display = ("from_employee", "to_employee", "moved_reports", "moved_departments", "disabled_account", "created_at")
    search_fields = ("from_employee__name", "to_employee__name")


@admin.register(Permission)
class PermissionAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "module")
    search_fields = ("code", "name", "module")


@admin.register(Role)
class RoleAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "data_scope", "is_active")
    search_fields = ("code", "name")
    filter_horizontal = ("permissions",)


@admin.register(RoleAssignment)
class RoleAssignmentAdmin(admin.ModelAdmin):
    list_display = ("user", "role", "organization")
    search_fields = ("user__username", "role__code")


@admin.register(ApiKey)
class ApiKeyAdmin(admin.ModelAdmin):
    list_display = ("name", "key_id", "organization", "scopes", "is_active", "last_used_at")
    search_fields = ("name", "key_id")
    list_filter = ("is_active",)
    readonly_fields = ("last_used_at",)


@admin.register(LoginAttempt)
class LoginAttemptAdmin(admin.ModelAdmin):
    list_display = ("username", "success", "result", "ip", "created_at")
    search_fields = ("username", "ip")
    list_filter = ("success", "result")
    readonly_fields = ("username", "user", "success", "result", "ip", "user_agent", "created_at")
