from rest_framework import serializers

from .models import Carrier, Customer, Driver, Route, Vehicle


class CustomerSerializer(serializers.ModelSerializer):
    history = serializers.SerializerMethodField()

    class Meta:
        model = Customer
        fields = [
            "id", "code", "name", "contact_name", "contact_phone", "wechat_group",
            "settlement_type", "is_active", "history",
        ]

    def get_history(self, obj):
        from apps.ops.stats import customer_history

        return customer_history(obj)


class CarrierSerializer(serializers.ModelSerializer):
    class Meta:
        model = Carrier
        fields = ["id", "code", "name", "contact_name", "contact_phone", "settlement_type", "is_active"]


class VehicleSerializer(serializers.ModelSerializer):
    carrier_name = serializers.CharField(source="carrier.name", read_only=True, default="")
    vehicle_class_label = serializers.CharField(source="get_vehicle_class_display", read_only=True, default="")
    dispatch_source_label = serializers.CharField(source="get_dispatch_source_display", read_only=True, default="")
    freight_total = serializers.SerializerMethodField()

    class Meta:
        model = Vehicle
        fields = [
            "id", "plate_no", "vehicle_class", "vehicle_class_label", "dispatch_source", "dispatch_source_label",
            "vehicle_type", "ownership_type", "carrier", "carrier_name", "load_capacity_ton", "volume_capacity_cbm",
            "road_transport_cert_no", "inspection_expiry", "insurance_expiry", "maintenance_due_date",
            "freight_total", "is_active",
        ]

    def get_freight_total(self, obj):
        from apps.ops.stats import vehicle_freight_total

        return vehicle_freight_total(obj)


class DriverSerializer(serializers.ModelSerializer):
    carrier_name = serializers.CharField(source="carrier.name", read_only=True, default="")
    employment_label = serializers.CharField(source="get_employment_type_display", read_only=True, default="")

    class Meta:
        model = Driver
        fields = [
            "id", "name", "phone", "wechat", "employment_type", "employment_label",
            "app_registered", "app_registered_at", "cumulative_waybills", "cumulative_freight",
            "id_no", "license_no", "license_type", "license_expiry",
            "qualification_cert_no", "qualification_expiry", "carrier", "carrier_name", "is_active",
        ]


class RouteSerializer(serializers.ModelSerializer):
    class Meta:
        model = Route
        fields = [
            "id", "code", "name", "origin", "destination", "waypoints",
            "corridor_m", "distance_km", "is_active",
        ]
