from django.contrib import admin

from .models import AuditLog


@admin.register(AuditLog)
class AuditLogAdmin(admin.ModelAdmin):
    list_display = ("action", "resource_type", "resource_id", "actor", "status_code", "created_at")
    search_fields = ("action", "resource_type", "resource_id", "request_id")
    list_filter = ("action", "method")
    readonly_fields = ("created_at", "updated_at")
