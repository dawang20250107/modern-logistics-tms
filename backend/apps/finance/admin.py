from django.contrib import admin

from .models import (
    ExpenseItem,
    ExpenseRecord,
    PaymentRequest,
    PricingRule,
    Webhook,
    WebhookDelivery,
)


@admin.register(ExpenseItem)
class ExpenseItemAdmin(admin.ModelAdmin):
    list_display = ("code", "name", "direction", "is_active")
    list_filter = ("direction", "is_active")
    search_fields = ("code", "name")


@admin.register(ExpenseRecord)
class ExpenseRecordAdmin(admin.ModelAdmin):
    list_display = ("expense_item_code", "direction", "amount", "risk_status", "waybill")
    list_filter = ("direction", "risk_status")
    raw_id_fields = ("waybill",)


@admin.register(PaymentRequest)
class PaymentRequestAdmin(admin.ModelAdmin):
    list_display = ("request_no", "counterparty_type", "amount", "status")
    list_filter = ("status",)
    search_fields = ("request_no",)


@admin.register(PricingRule)
class PricingRuleAdmin(admin.ModelAdmin):
    list_display = ("name", "price_type", "expense_item_code", "base_price", "fuel_surcharge_pct", "priority", "is_active")
    list_filter = ("price_type", "is_active")
    search_fields = ("name", "route_name")


@admin.register(Webhook)
class WebhookAdmin(admin.ModelAdmin):
    list_display = ("name", "target_url", "events", "is_active")
    list_filter = ("is_active",)
    search_fields = ("name", "target_url")


@admin.register(WebhookDelivery)
class WebhookDeliveryAdmin(admin.ModelAdmin):
    list_display = ("webhook", "event_type", "status", "response_code", "attempts", "created_at")
    list_filter = ("status", "event_type")
