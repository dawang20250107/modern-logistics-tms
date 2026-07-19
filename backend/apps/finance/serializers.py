from rest_framework import serializers

from apps.core.exceptions import AppError

from .models import (
    ExpenseItem,
    ExpenseRecord,
    PaymentRequest,
    PricingRule,
    Statement,
    StatementLine,
    StatementPayment,
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
            "occurred_at", "risk_status", "source_system", "external_id",
            "payee_type", "payee_ref", "remark", "created_at",
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


class ReimbursementSerializer(serializers.ModelSerializer):
    status_label = serializers.CharField(source="get_status_display", read_only=True)
    category_label = serializers.CharField(source="get_category_display", read_only=True)
    waybill_no = serializers.CharField(source="waybill.waybill_no", read_only=True, default="")
    submitted_by_name = serializers.CharField(source="submitted_by.username", read_only=True, default="")

    class Meta:
        from .models import Reimbursement

        model = Reimbursement
        fields = [
            "id", "reimb_no", "waybill", "waybill_no", "order_no", "category", "category_label",
            "amount", "reason", "status", "status_label", "submitted_by_name",
            "approved_at", "paid_at", "remark", "created_at",
        ]
        read_only_fields = ["reimb_no", "status", "approved_at", "paid_at"]


class PricingRuleSerializer(serializers.ModelSerializer):
    customer_name = serializers.CharField(source="customer.name", read_only=True, default="")
    carrier_name = serializers.CharField(source="carrier.name", read_only=True, default="")
    charge_method_label = serializers.CharField(source="get_charge_method_display", read_only=True)

    class Meta:
        model = PricingRule
        fields = [
            "id", "name", "price_type", "charge_method", "charge_method_label",
            "expense_item_code", "customer", "customer_name",
            "carrier", "carrier_name", "route_name", "vehicle_type", "base_price", "min_price",
            "unit_price", "min_charge_qty", "tier_prices", "volumetric_factor", "fuel_surcharge_pct",
            "priority", "is_active", "created_at",
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


class StatementLineSerializer(serializers.ModelSerializer):
    class Meta:
        model = StatementLine
        fields = [
            "id", "waybill_no", "expense_item_code", "amount", "occurred_at",
            "is_anomaly", "baseline_avg", "deviation_pct",
        ]


class StatementPaymentSerializer(serializers.ModelSerializer):
    method_label = serializers.CharField(source="get_method_display", read_only=True)
    created_by_name = serializers.CharField(source="created_by.username", read_only=True, default="")

    class Meta:
        model = StatementPayment
        fields = [
            "id", "statement", "amount", "method", "method_label", "paid_at",
            "reference_no", "remark", "created_by_name", "created_at",
        ]
        read_only_fields = ["created_at"]


class StatementSerializer(serializers.ModelSerializer):
    diff = serializers.DecimalField(max_digits=14, decimal_places=2, read_only=True)
    outstanding = serializers.DecimalField(max_digits=14, decimal_places=2, read_only=True)
    status_label = serializers.CharField(source="get_status_display", read_only=True)
    lines = StatementLineSerializer(many=True, read_only=True)

    class Meta:
        model = Statement
        fields = [
            "id", "statement_no", "direction", "counterparty_type", "counterparty_id", "counterparty_name",
            "period_start", "period_end", "due_date", "total_amount", "item_count", "external_total", "diff",
            "settled_amount", "outstanding", "settled_at", "status", "status_label",
            "confirmed_at", "audited_at", "created_at", "lines",
        ]
        read_only_fields = [
            "status", "total_amount", "item_count", "settled_amount", "settled_at", "confirmed_at", "audited_at",
        ]


class StatementListSerializer(StatementSerializer):
    class Meta(StatementSerializer.Meta):
        fields = [f for f in StatementSerializer.Meta.fields if f != "lines"]
