from rest_framework import serializers

from .models import Alert, Device, Geofence, VehicleState


class GeofenceSerializer(serializers.ModelSerializer):
    class Meta:
        model = Geofence
        fields = [
            "id", "name", "shape", "purpose", "center_lng", "center_lat",
            "radius_m", "polygon", "is_active", "created_at",
        ]


class DeviceSerializer(serializers.ModelSerializer):
    vehicle_plate = serializers.CharField(source="vehicle.plate_no", read_only=True, default="")

    class Meta:
        model = Device
        fields = [
            "id", "device_no", "device_type", "vehicle", "vehicle_plate",
            "sim_no", "status", "last_seen_at", "meta", "created_at",
        ]
        read_only_fields = ["status", "last_seen_at"]


class VehicleStateSerializer(serializers.ModelSerializer):
    vehicle_plate = serializers.CharField(source="vehicle.plate_no", read_only=True, default="")
    vehicle_type = serializers.CharField(source="vehicle.vehicle_type", read_only=True, default="")
    waybill_no = serializers.CharField(source="waybill.waybill_no", read_only=True, default="")

    class Meta:
        model = VehicleState
        fields = [
            "id", "vehicle", "vehicle_plate", "vehicle_type", "waybill", "waybill_no",
            "lng", "lat", "speed_kmh", "heading", "mileage_km",
            "temperature_c", "fuel_pct", "online", "reported_at",
        ]


class AlertSerializer(serializers.ModelSerializer):
    vehicle_plate = serializers.CharField(source="vehicle.plate_no", read_only=True, default="")
    waybill_no = serializers.CharField(source="waybill.waybill_no", read_only=True, default="")
    device_no = serializers.CharField(source="device.device_no", read_only=True, default="")

    class Meta:
        model = Alert
        fields = [
            "id", "alert_type", "level", "status", "vehicle", "vehicle_plate",
            "device", "device_no", "waybill", "waybill_no", "message",
            "value", "threshold", "detail", "triggered_at", "handled_at", "created_at",
        ]
        read_only_fields = ["status", "handled_at"]
