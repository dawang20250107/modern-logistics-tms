from datetime import timedelta

from django.utils import timezone
from rest_framework import serializers

from .models import B2BPartner, Carrier, CarrierLanePrice, Customer, Driver, DriverCredential, Route, Vehicle


def _is_list(serializer) -> bool:
    """当前是否为列表动作（list 时跳过逐行重聚合，避免 N+1）。"""
    view = serializer.context.get("view")
    return getattr(view, "action", None) == "list"


class CustomerSerializer(serializers.ModelSerializer):
    history = serializers.SerializerMethodField()
    level_label = serializers.CharField(source="get_level_display", read_only=True, default="")

    class Meta:
        model = Customer
        fields = [
            "id", "code", "name", "category", "level", "level_label",
            "contact_name", "contact_phone", "wechat_group",
            "settlement_type", "credit_limit", "credit_days", "billing_day", "is_active", "history",
        ]

    def get_history(self, obj):
        # 聚合较重，仅在详情页计算，避免列表 N+1
        if _is_list(self):
            return None
        from apps.ops.stats import customer_history

        return customer_history(obj)


class CarrierSerializer(serializers.ModelSerializer):
    grade_label = serializers.CharField(source="get_grade_display", read_only=True, default="")
    carrier_type_label = serializers.CharField(source="get_carrier_type_display", read_only=True, default="")
    dispatch_blocked = serializers.SerializerMethodField()
    expiry_alerts = serializers.SerializerMethodField()
    performance = serializers.SerializerMethodField()

    class Meta:
        model = Carrier
        fields = [
            "id", "code", "name", "carrier_type", "carrier_type_label",
            "contact_name", "contact_phone", "city", "service_area", "settlement_type", "is_active",
            "grade", "grade_label", "blacklisted", "blacklist_reason",
            "business_license_no", "transport_license_no", "qualification_expiry",
            "contract_expiry", "insurance_expiry", "tax_no",
            "credit_limit", "credit_days", "billing_day",
            "dispatch_blocked", "expiry_alerts", "performance",
        ]

    def get_dispatch_blocked(self, obj) -> str:
        """当前是否因风控不可派单（黑名单/停用/资质过期）；可派单为空串。"""
        return obj.dispatch_block_reason()

    def get_expiry_alerts(self, obj) -> list:
        """资质/合同/保险到期预警（今日起 30 天内到期或已过期）。"""
        today = timezone.localdate()
        soon = today + timedelta(days=30)
        alerts = []
        for field, label in [
            ("qualification_expiry", "承运资质"), ("contract_expiry", "合作合同"), ("insurance_expiry", "承运人责任险"),
        ]:
            d = getattr(obj, field, None)
            if d and d <= soon:
                alerts.append({"field": field, "label": label, "date": d.isoformat(), "expired": d < today})
        return alerts

    def get_performance(self, obj) -> dict | None:
        # 经营表现聚合较重，仅详情页计算，避免列表 N+1
        if _is_list(self):
            return None
        from apps.ops.carrier_scoring import carrier_performance, frequent_routes

        perf = carrier_performance(obj, "", "")
        perf["frequent_routes"] = frequent_routes(obj)
        return perf


class CarrierLanePriceSerializer(serializers.ModelSerializer):
    carrier_name = serializers.CharField(source="carrier.name", read_only=True, default="")

    class Meta:
        model = CarrierLanePrice
        fields = [
            "id", "carrier", "carrier_name", "origin_city", "dest_city", "vehicle_type", "vehicle_length_m",
            "standard_price", "min_price", "max_price", "last_deal_price",
            "effective_from", "effective_to", "is_preferred", "is_recommended", "note", "is_active",
        ]


class VehicleSerializer(serializers.ModelSerializer):
    carrier_name = serializers.CharField(source="carrier.name", read_only=True, default="")
    vehicle_class_label = serializers.CharField(source="get_vehicle_class_display", read_only=True, default="")
    dispatch_source_label = serializers.CharField(source="get_dispatch_source_display", read_only=True, default="")
    freight_total = serializers.SerializerMethodField()

    body_type_label = serializers.CharField(source="get_body_type_display", read_only=True, default="")

    class Meta:
        model = Vehicle
        fields = [
            "id", "plate_no", "vehicle_class", "vehicle_class_label", "body_type", "body_type_label",
            "vehicle_length_m", "dispatch_source", "dispatch_source_label",
            "vehicle_type", "ownership_type", "carrier", "carrier_name", "load_capacity_ton", "volume_capacity_cbm",
            "road_transport_cert_no", "inspection_expiry", "insurance_expiry", "maintenance_due_date",
            "freight_total", "is_active",
        ]

    def get_freight_total(self, obj):
        # 聚合较重，仅在详情页计算，避免列表 N+1
        if _is_list(self):
            return None
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


class DriverCredentialSerializer(serializers.ModelSerializer):
    cred_type_label = serializers.CharField(source="get_cred_type_display", read_only=True)
    side_label = serializers.CharField(source="get_side_display", read_only=True)
    driver_name = serializers.CharField(source="driver.name", read_only=True, default="")
    file_display = serializers.SerializerMethodField()

    class Meta:
        model = DriverCredential
        fields = [
            "id", "driver", "driver_name", "cred_type", "cred_type_label", "side", "side_label",
            "file", "file_url", "file_display", "ocr_status", "ocr_result",
            "holder_name", "cert_no", "expiry_date", "self_uploaded", "created_at",
        ]
        extra_kwargs = {"file": {"required": False}}

    def get_file_display(self, obj):
        if obj.file:
            try:
                return obj.file.url
            except ValueError:
                return ""
        return obj.file_url


class B2BPartnerSerializer(serializers.ModelSerializer):
    partner_type_label = serializers.CharField(source="get_partner_type_display", read_only=True)

    class Meta:
        model = B2BPartner
        fields = [
            "id", "partner_type", "partner_type_label", "code", "name",
            "contact_name", "contact_phone", "address", "city", "is_active",
        ]
