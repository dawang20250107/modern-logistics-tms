from django.contrib import admin

from .models import MetricSnapshot


@admin.register(MetricSnapshot)
class MetricSnapshotAdmin(admin.ModelAdmin):
    list_display = ("metric_code", "stat_date", "dimension_key", "value")
    list_filter = ("metric_code", "stat_date")
    search_fields = ("metric_code",)
