from django.contrib import admin

from .models import Alert, Device, VehicleState


@admin.register(Device)
class DeviceAdmin(admin.ModelAdmin):
    list_display = ("device_no", "device_type", "vehicle", "status", "last_seen_at")
    list_filter = ("device_type", "status")
    search_fields = ("device_no", "sim_no")


@admin.register(VehicleState)
class VehicleStateAdmin(admin.ModelAdmin):
    list_display = ("vehicle", "online", "lat", "lng", "speed_kmh", "reported_at")
    list_filter = ("online",)


@admin.register(Alert)
class AlertAdmin(admin.ModelAdmin):
    list_display = ("alert_type", "level", "status", "vehicle", "message", "triggered_at")
    list_filter = ("alert_type", "level", "status")
    search_fields = ("message",)
