from django.contrib import admin

from .models import AgentSuggestion


@admin.register(AgentSuggestion)
class AgentSuggestionAdmin(admin.ModelAdmin):
    list_display = ("suggestion_type", "title", "status", "waybill", "tool_name", "created_at")
    list_filter = ("suggestion_type", "status")
    search_fields = ("title", "body")
    raw_id_fields = ("waybill",)
