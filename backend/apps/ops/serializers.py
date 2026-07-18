from rest_framework import serializers

from .models import (
    Contract,
    ExceptionEvent,
    ExceptionRecord,
    Order,
    OrderAttachment,
    OrderCargoItem,
    OrderEvent,
    OrderStop,
    OrderTemplate,
    Receipt,
    TrackingPoint,
    Waybill,
    WaybillEvent,
    WaybillStop,
)
from .services import allowed_next


class OrderAttachmentSerializer(serializers.ModelSerializer):
    file_display = serializers.SerializerMethodField()
    uploaded_by_name = serializers.CharField(source="uploaded_by.username", read_only=True, default="")

    class Meta:
        model = OrderAttachment
        fields = ["id", "order", "kind", "name", "file", "file_url", "file_display", "uploaded_by_name", "created_at"]
        read_only_fields = ["uploaded_by_name"]
        extra_kwargs = {"file": {"required": False}, "order": {"required": False}}

    def get_file_display(self, obj):
        if obj.file:
            try:
                return obj.file.url
            except ValueError:
                return ""
        return obj.file_url


class OrderCargoItemSerializer(serializers.ModelSerializer):
    class Meta:
        model = OrderCargoItem
        fields = ["id", "seq", "name", "quantity", "weight_ton", "volume_cbm", "package_type", "temperature_range", "remark"]


class OrderStopSerializer(serializers.ModelSerializer):
    class Meta:
        model = OrderStop
        fields = [
            "id", "seq", "stop_type", "city", "address", "contact_name", "contact_phone",
            "expected_start", "expected_end", "cargo_note",
        ]


class OrderTemplateSerializer(serializers.ModelSerializer):
    created_by_name = serializers.CharField(source="created_by.username", read_only=True, default="")

    class Meta:
        model = OrderTemplate
        fields = ["id", "name", "payload", "created_by_name", "created_at"]
        read_only_fields = ["created_by_name"]


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
    trailer_plate = serializers.CharField(source="trailer.plate_no", read_only=True, default="")
    driver_name = serializers.CharField(source="driver.name", read_only=True, default="")
    driver_phone = serializers.CharField(source="driver.phone", read_only=True, default="")
    driver_employment = serializers.CharField(source="driver.get_employment_type_display", read_only=True, default="")
    drivers = serializers.SerializerMethodField()
    cargo = serializers.SerializerMethodField()
    freight_term_label = serializers.CharField(source="get_freight_term_display", read_only=True)
    freight_payer_label = serializers.CharField(source="get_freight_payer_display", read_only=True)
    cod_status_label = serializers.CharField(source="get_cod_status_display", read_only=True)
    dispatch_type_label = serializers.CharField(source="get_dispatch_type_display", read_only=True, default="")
    channel = serializers.SerializerMethodField()
    receivable_amount = serializers.SerializerMethodField()
    payable_amount = serializers.SerializerMethodField()

    class Meta:
        model = Waybill
        fields = [
            "id", "waybill_no", "customer_name", "carrier_name", "vehicle_plate", "trailer_plate",
            "driver_name", "driver_phone", "driver_employment", "drivers",
            "route_name", "ai_conversation_id", "origin", "destination", "status", "dispatch_status", "risk_level",
            "dispatch_type", "dispatch_type_label", "channel", "platform_name", "platform_order_no",
            "receipt_status", "eta_drift_minutes", "planned_arrival", "estimated_arrival",
            "loaded_at", "departed_at", "arrived_at", "signed_at",
            "freight_term", "freight_term_label", "freight_payer", "freight_payer_label",
            "cod_amount", "cod_status", "cod_status_label", "cod_collected_at", "cod_remitted_at",
            "receivable_amount", "payable_amount",
            "cargo", "created_at",
        ]

    def get_channel(self, obj):
        return Waybill.CHANNEL_LABELS.get(obj.dispatch_type, "")

    def get_receivable_amount(self, obj):
        # 由视图 annotate 注入；未聚合时回退 0，避免列表 N+1
        return float(getattr(obj, "receivable_total", 0) or 0)

    def get_payable_amount(self, obj):
        return float(getattr(obj, "payable_total", 0) or 0)

    def get_drivers(self, obj):
        return [
            {
                "id": str(a.driver_id), "name": a.driver.name, "phone": a.driver.phone,
                "wechat": a.driver.wechat, "app_registered": a.driver.app_registered,
                "role": a.role, "role_label": a.get_role_display(),
                "employment": a.driver.get_employment_type_display(), "note": a.note,
            }
            # 复用视图层 prefetch_related("driver_assignments__driver") 的缓存，
            # 不在此处加 select_related（会另起查询导致列表 N+1）
            for a in obj.driver_assignments.all()
        ]

    def get_cargo(self, obj):
        return {
            "quantity": obj.cargo_quantity,
            "weight_ton": float(obj.cargo_weight_ton),
            "volume_cbm": float(obj.cargo_volume_cbm),
        }


class ContractSerializer(serializers.ModelSerializer):
    status_label = serializers.CharField(source="get_confirm_status_display", read_only=True)
    driver_name = serializers.CharField(source="driver.name", read_only=True, default="")
    pdf_url = serializers.SerializerMethodField()

    class Meta:
        model = Contract
        fields = [
            "id", "contract_no", "waybill", "driver", "driver_name", "template_code", "content",
            "sent_at", "driver_reply", "confirm_status", "status_label", "confirmed_at", "pdf_url", "created_at",
        ]

    def get_pdf_url(self, obj):
        return obj.pdf.url if obj.pdf else ""


class WaybillStopSerializer(serializers.ModelSerializer):
    stop_type_label = serializers.CharField(source="get_stop_type_display", read_only=True)
    status_label = serializers.CharField(source="get_status_display", read_only=True)

    class Meta:
        model = WaybillStop
        fields = [
            "id", "seq", "stop_type", "stop_type_label", "city", "address", "contact_name", "contact_phone",
            "lat", "lng", "radius_m", "planned_eta", "actual_arrival_at", "actual_depart_at",
            "arrival_source", "status", "status_label", "note",
        ]


class WaybillDetailSerializer(WaybillSerializer):
    timeline = WaybillEventSerializer(source="events", many=True, read_only=True)
    agent_suggestions = _SuggestionSerializer(many=True, read_only=True)
    next_statuses = serializers.SerializerMethodField()
    stops = WaybillStopSerializer(many=True, read_only=True)

    class Meta(WaybillSerializer.Meta):
        fields = WaybillSerializer.Meta.fields + ["timeline", "agent_suggestions", "next_statuses", "stops"]

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
            "freight_term", "freight_payer", "cod_amount", "cod_status",
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


class ExceptionEventSerializer(serializers.ModelSerializer):
    actor_name = serializers.CharField(source="actor.username", read_only=True, default="")

    class Meta:
        model = ExceptionEvent
        fields = ["id", "event_type", "from_status", "to_status", "actor_name", "note", "payload", "event_time"]


class ReceiptSerializer(serializers.ModelSerializer):
    waybill_no = serializers.CharField(source="waybill.waybill_no", read_only=True, default="")
    file_display = serializers.SerializerMethodField()

    class Meta:
        model = Receipt
        fields = [
            "id", "waybill", "waybill_no", "receipt_type", "status", "file", "file_display",
            "file_url", "ocr_status", "ocr_result", "signatory", "signed_at", "created_at",
            "outcome", "total_quantity", "signed_quantity", "damaged_quantity",
            "shortage_quantity", "rejection_reason",
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
    waybill_nos = serializers.SerializerMethodField()
    cargo_items = OrderCargoItemSerializer(many=True, read_only=True)
    stops = OrderStopSerializer(many=True, read_only=True)
    attachments = OrderAttachmentSerializer(many=True, read_only=True)
    freight_term_label = serializers.CharField(source="get_freight_term_display", read_only=True)
    freight_payer_label = serializers.CharField(source="get_freight_payer_display", read_only=True)

    def get_waybill_nos(self, obj) -> list[str]:
        # 依赖视图层 prefetch_related("waybills") 避免 N+1；拆单后可能多张
        return [w.waybill_no for w in obj.waybills.all()]

    class Meta:
        model = Order
        fields = [
            "id", "order_no", "customer", "customer_name", "channel", "source",
            "source_type", "business_type", "priority", "settlement_type", "status",
            "freight_term", "freight_term_label", "freight_payer", "freight_payer_label",
            "cod_amount", "cod_status",
            "contact_name", "contact_phone", "origin", "destination",
            "pickup_address", "pickup_contact_name", "pickup_contact_phone",
            "delivery_address", "delivery_contact_name", "delivery_contact_phone",
            "cargo_desc", "cargo_quantity", "cargo_weight_ton", "cargo_volume_cbm",
            "cargo_value", "package_type", "is_hazardous", "temperature_range", "quoted_amount",
            "expected_pickup_at", "expected_delivery_at", "sla_status", "delivered_at",
            "claimed_by", "claimed_by_name", "claimed_at", "pooled_at",
            "created_by", "created_by_name", "raw_text", "ai_conversation_id", "parse_meta", "remark", "created_at",
            "waybill_nos", "cargo_items", "stops", "attachments",
            "approval_status", "approval_remark", "approved_at",
        ]
        read_only_fields = ["claimed_by", "claimed_at", "pooled_at", "created_by"]


class ReminderTemplateSerializer(serializers.ModelSerializer):
    class Meta:
        from .models import ReminderTemplate

        model = ReminderTemplate
        fields = ["id", "name", "category", "content", "is_active", "created_at"]


class DriverReminderSerializer(serializers.ModelSerializer):
    driver_name = serializers.CharField(source="driver.name", read_only=True, default="")
    waybill_no = serializers.CharField(source="waybill.waybill_no", read_only=True, default="")

    class Meta:
        from .models import DriverReminder

        model = DriverReminder
        fields = [
            "id", "waybill", "waybill_no", "driver", "driver_name", "template", "title", "content",
            "ack_required", "status", "sent_at", "acknowledged_at",
        ]
