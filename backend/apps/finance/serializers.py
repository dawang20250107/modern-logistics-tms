from rest_framework import serializers

from apps.core.exceptions import AppError

from .models import (
    ExpenseItem,
    ExpenseRecord,
    PaymentRequest,
    PricingRule,
    Webhook,
    WebhookDelivery,
)


class ExpenseItemSerializer(serializers.ModelSerializer):
    class Meta:
        model = ExpenseItem
        fields = ["id", "code", "name", "direction", "debit_account_code", "credit_account_code", "is_active"]


class ExpenseRecordSerializer(serializers.ModelSerializer):
    # 外部系统按运单业务号推送；内部也可直接传 waybill(UUID)
    waybill_no = serializers.CharField(write_only=True, required=False)

    class Meta:
        model = ExpenseRecord
        fields = [
            "id", "waybill", "waybill_no", "direction", "expense_item_code", "amount", "currency",
            "occurred_at", "risk_status", "source_system", "external_id", "created_at",
        ]
        extra_kwargs = {"waybill": {"required": False}}

    def create(self, validated_data):
        wbno = validated_data.pop("waybill_no", None)
        if wbno and not validated_data.get("waybill"):
            from apps.ops.models import Waybill

            waybill = Waybill.objects.filter(waybill_no=wbno).first()
            if waybill is None:
                raise AppError("WAYBILL_NOT_FOUND", "运单不存在", status=404)
            validated_data["waybill"] = waybill
        if not validated_data.get("waybill"):
            raise AppError("WAYBILL_REQUIRED", "需提供 waybill 或 waybill_no", status=400)
        return super().create(validated_data)


class PaymentRequestSerializer(serializers.ModelSerializer):
    class Meta:
        model = PaymentRequest
        fields = [
            "id", "request_no", "waybill", "counterparty_type", "counterparty_ref", "amount",
            "reason", "status", "external_approval_no", "created_at",
        ]


class PricingRuleSerializer(serializers.ModelSerializer):
    class Meta:
        model = PricingRule
        fields = [
            "id", "name", "price_type", "expense_item_code", "customer", "carrier", "route_name",
            "vehicle_type", "base_price", "price_per_ton", "min_price", "priority", "is_active",
            "created_at",
        ]


class WebhookSerializer(serializers.ModelSerializer):
    class Meta:
        model = Webhook
        fields = ["id", "name", "target_url", "secret", "events", "is_active", "created_at"]
        extra_kwargs = {"secret": {"write_only": True}}


class WebhookDeliverySerializer(serializers.ModelSerializer):
    class Meta:
        model = WebhookDelivery
        fields = ["id", "webhook", "event_type", "payload", "status", "response_code", "attempts", "created_at"]
