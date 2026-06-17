from django.contrib import admin

from .models import ExceptionRecord, Order, TrackingPoint, Waybill, WaybillEvent


@admin.register(Order)
class OrderAdmin(admin.ModelAdmin):
    list_display = ("order_no", "customer", "status", "created_at")
    search_fields = ("order_no",)


@admin.register(Waybill)
class WaybillAdmin(admin.ModelAdmin):
    list_display = ("waybill_no", "route_name", "status", "risk_level", "receipt_status", "eta_drift_minutes")
    search_fields = ("waybill_no", "route_name", "origin", "destination")
    list_filter = ("status", "risk_level", "receipt_status")
    raw_id_fields = ("order", "customer", "carrier", "vehicle", "driver")


@admin.register(WaybillEvent)
class WaybillEventAdmin(admin.ModelAdmin):
    list_display = ("waybill", "event_type", "event_time")
    search_fields = ("waybill__waybill_no", "event_type")


@admin.register(ExceptionRecord)
class ExceptionRecordAdmin(admin.ModelAdmin):
    list_display = ("exception_type", "status", "waybill", "responsibility_party", "amount")
    list_filter = ("exception_type", "status")


@admin.register(TrackingPoint)
class TrackingPointAdmin(admin.ModelAdmin):
    list_display = ("waybill", "lng", "lat", "speed_kmh", "reported_at")
