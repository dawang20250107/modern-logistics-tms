from django.contrib import admin

from .models import ApiKey, Organization, Permission, Role, RoleAssignment


@admin.register(Organization)
class OrganizationAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "type", "parent", "is_active")
    search_fields = ("code", "name")
    list_filter = ("type", "is_active")


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
