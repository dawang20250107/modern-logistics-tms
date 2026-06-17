from rest_framework import serializers

from .models import Carrier, Customer, Driver, Vehicle


class CustomerSerializer(serializers.ModelSerializer):
    class Meta:
        model = Customer
        fields = ["id", "code", "name", "contact_name", "contact_phone", "settlement_type", "is_active"]


class CarrierSerializer(serializers.ModelSerializer):
    class Meta:
        model = Carrier
        fields = ["id", "code", "name", "contact_name", "contact_phone", "settlement_type", "is_active"]


class VehicleSerializer(serializers.ModelSerializer):
    carrier_name = serializers.CharField(source="carrier.name", read_only=True, default="")

    class Meta:
        model = Vehicle
        fields = ["id", "plate_no", "vehicle_type", "ownership_type", "carrier", "carrier_name", "is_active"]


class DriverSerializer(serializers.ModelSerializer):
    carrier_name = serializers.CharField(source="carrier.name", read_only=True, default="")

    class Meta:
        model = Driver
        fields = ["id", "name", "phone", "id_no", "license_no", "carrier", "carrier_name", "is_active"]
