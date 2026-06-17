from django.contrib import admin

from .models import Carrier, Customer, Driver, Vehicle


@admin.register(Customer)
class CustomerAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "contact_phone", "is_active")
    search_fields = ("code", "name", "contact_phone")


@admin.register(Carrier)
class CarrierAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "contact_phone", "is_active")
    search_fields = ("code", "name", "contact_phone")


@admin.register(Vehicle)
class VehicleAdmin(admin.ModelAdmin):
    list_display = ("plate_no", "vehicle_type", "carrier", "is_active")
    search_fields = ("plate_no",)
    list_filter = ("vehicle_type",)


@admin.register(Driver)
class DriverAdmin(admin.ModelAdmin):
    list_display = ("name", "phone", "carrier", "is_active")
    search_fields = ("name", "phone")
