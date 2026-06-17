from rest_framework import serializers

from .models import ExceptionRecord, Order, OrderEvent, Receipt, TrackingPoint, Waybill, WaybillEvent
from .services import allowed_next


class OrderEventSerializer(serializers.ModelSerializer):
    actor_name = serializers.CharField(source="actor.username", read_only=True, default="")

    class Meta:
        model = OrderEvent
        fields = ["id", "event_type", "from_status", "to_status", "actor_name", "source", "payload", "event_time"]


class WaybillEventSerializer(serializers.ModelSerializer):
    class Meta:
        model = WaybillEvent
        fields = ["id", "event_type", "event_time", "resource", "source", "payload"]


class _SuggestionSerializer(serializers.Serializer):
    """序列化反向关联的 ai.AgentSuggestion（按属性读取，避免跨应用导入）。"""

    id = serializers.UUIDField()
    suggestion_type = serializers.CharField()
    title = serializers.CharField()
    body = serializers.CharField()
    status = serializers.CharField()
    evidence = serializers.JSONField()
    created_at = serializers.DateTimeField()


class WaybillSerializer(serializers.ModelSerializer):
    customer_name = serializers.CharField(source="customer.name", read_only=True, default="")
    carrier_name = serializers.CharField(source="carrier.name", read_only=True, default="")
    vehicle_plate = serializers.CharField(source="vehicle.plate_no", read_only=True, default="")
    driver_name = serializers.CharField(source="driver.name", read_only=True, default="")
    cargo = serializers.SerializerMethodField()

    class Meta:
        model = Waybill
        fields = [
            "id", "waybill_no", "customer_name", "carrier_name", "vehicle_plate", "driver_name",
            "route_name", "origin", "destination", "status", "dispatch_status", "risk_level",
            "receipt_status", "eta_drift_minutes", "planned_arrival", "estimated_arrival",
            "cargo", "created_at",
        ]

    def get_cargo(self, obj):
        return {
            "quantity": obj.cargo_quantity,
            "weight_ton": float(obj.cargo_weight_ton),
            "volume_cbm": float(obj.cargo_volume_cbm),
        }


class WaybillDetailSerializer(WaybillSerializer):
    timeline = WaybillEventSerializer(source="events", many=True, read_only=True)
    agent_suggestions = _SuggestionSerializer(many=True, read_only=True)
    next_statuses = serializers.SerializerMethodField()

    class Meta(WaybillSerializer.Meta):
        fields = WaybillSerializer.Meta.fields + ["timeline", "agent_suggestions", "next_statuses"]

    def get_next_statuses(self, obj):
        return allowed_next(obj.status)


class WaybillWriteSerializer(serializers.ModelSerializer):
    class Meta:
        model = Waybill
        fields = [
            "waybill_no", "order", "customer", "carrier", "vehicle", "driver", "organization",
            "route_name", "origin", "destination", "status", "dispatch_status", "risk_level",
            "receipt_status", "eta_drift_minutes", "cargo_quantity", "cargo_weight_ton",
            "cargo_volume_cbm", "planned_arrival", "estimated_arrival",
        ]


class TrackingPointSerializer(serializers.ModelSerializer):
    class Meta:
        model = TrackingPoint
        fields = ["id", "lng", "lat", "speed_kmh", "reported_at", "provider"]


class ExceptionSerializer(serializers.ModelSerializer):
    waybill_no = serializers.CharField(source="waybill.waybill_no", read_only=True, default="")
    assignee_name = serializers.CharField(source="assignee.username", read_only=True, default="")

    class Meta:
        model = ExceptionRecord
        fields = [
            "id", "waybill", "waybill_no", "exception_type", "level", "source", "description",
            "status", "assignee", "assignee_name", "responsibility_party", "amount", "resolution",
            "created_at",
        ]
        read_only_fields = ["status"]


class ReceiptSerializer(serializers.ModelSerializer):
    waybill_no = serializers.CharField(source="waybill.waybill_no", read_only=True, default="")
    file_display = serializers.SerializerMethodField()

    class Meta:
        model = Receipt
        fields = [
            "id", "waybill", "waybill_no", "receipt_type", "status", "file", "file_display",
            "file_url", "ocr_status", "ocr_result", "signatory", "signed_at", "created_at",
        ]
        read_only_fields = ["ocr_status", "ocr_result", "status"]

    def get_file_display(self, obj):
        if obj.file:
            try:
                return obj.file.url
            except ValueError:
                return ""
        return obj.file_url


class OrderSerializer(serializers.ModelSerializer):
    customer_name = serializers.CharField(source="customer.name", read_only=True, default="")
    created_by_name = serializers.CharField(source="created_by.username", read_only=True, default="")
    claimed_by_name = serializers.CharField(source="claimed_by.username", read_only=True, default="")

    class Meta:
        model = Order
        fields = [
            "id", "order_no", "customer", "customer_name", "channel", "source",
            "source_type", "business_type", "priority", "settlement_type", "status",
            "contact_name", "contact_phone", "origin", "destination",
            "pickup_address", "pickup_contact_name", "pickup_contact_phone",
            "delivery_address", "delivery_contact_name", "delivery_contact_phone",
            "cargo_desc", "cargo_quantity", "cargo_weight_ton", "cargo_volume_cbm",
            "cargo_value", "package_type", "is_hazardous", "temperature_range", "quoted_amount",
            "expected_pickup_at", "expected_delivery_at",
            "claimed_by", "claimed_by_name", "claimed_at", "pooled_at",
            "created_by", "created_by_name", "raw_text", "parse_meta", "remark", "created_at",
        ]
        read_only_fields = ["claimed_by", "claimed_at", "pooled_at", "created_by"]
