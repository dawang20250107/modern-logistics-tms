from django.contrib import admin

from .models import (
    AgentSuggestion,
    Carrier,
    Customer,
    Driver,
    ExceptionRecord,
    ExpenseRecord,
    TrackingPoint,
    Vehicle,
    Waybill,
    WaybillEvent,
)


@admin.register(Customer, Carrier)
class PartyAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "is_active")
    search_fields = ("code", "name")


@admin.register(Vehicle)
class VehicleAdmin(admin.ModelAdmin):
    list_display = ("plate_no", "vehicle_type", "carrier")
    search_fields = ("plate_no", "vehicle_type")


@admin.register(Driver)
class DriverAdmin(admin.ModelAdmin):
    list_display = ("name", "phone", "carrier")
    search_fields = ("name", "phone")


@admin.register(Waybill)
class WaybillAdmin(admin.ModelAdmin):
    list_display = ("waybill_no", "route_name", "status", "risk_level", "receipt_status", "eta_drift_minutes")
    list_filter = ("status", "risk_level", "receipt_status")
    search_fields = ("waybill_no", "route_name")


@admin.register(WaybillEvent)
class WaybillEventAdmin(admin.ModelAdmin):
    list_display = ("waybill", "event_type", "event_time", "resource")
    list_filter = ("event_type",)
    search_fields = ("waybill__waybill_no", "resource")


@admin.register(TrackingPoint)
class TrackingPointAdmin(admin.ModelAdmin):
    list_display = ("waybill", "lng", "lat", "speed_kmh", "reported_at")
    search_fields = ("waybill__waybill_no",)


@admin.register(ExpenseRecord)
class ExpenseRecordAdmin(admin.ModelAdmin):
    list_display = ("waybill", "direction", "expense_item_code", "amount", "risk_status")
    list_filter = ("direction", "risk_status")
    search_fields = ("waybill__waybill_no", "expense_item_code")


@admin.register(ExceptionRecord)
class ExceptionRecordAdmin(admin.ModelAdmin):
    list_display = ("waybill", "exception_type", "status", "responsibility_party", "amount")
    list_filter = ("exception_type", "status")
    search_fields = ("waybill__waybill_no", "description")


@admin.register(AgentSuggestion)
class AgentSuggestionAdmin(admin.ModelAdmin):
    list_display = ("waybill", "suggestion_type", "title", "status", "created_at")
    list_filter = ("suggestion_type", "status")
    search_fields = ("waybill__waybill_no", "title")
