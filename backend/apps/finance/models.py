"""费用与结算：费用字典 / 费用记录 / 付款申请 / 报价规则 / Webhook。

不做内部报销 UI；费用与付款通过开放接口与外部财务/OA/ERP 流转。
"""

from decimal import Decimal

from django.conf import settings
from django.db import models

from apps.core.models import BaseModel


class ExpenseItem(BaseModel):
    DIRECTION_RECEIVABLE = "receivable"
    DIRECTION_PAYABLE = "payable"
    DIRECTION_EXTERNAL = "external"
    DIRECTION_CHOICES = [
        (DIRECTION_RECEIVABLE, "应收"),
        (DIRECTION_PAYABLE, "应付"),
        (DIRECTION_EXTERNAL, "外部费用"),
    ]

    code = models.CharField(max_length=64, unique=True)
    name = models.CharField(max_length=128)
    direction = models.CharField(max_length=16, choices=DIRECTION_CHOICES)
    debit_account_code = models.CharField(max_length=64, blank=True)
    credit_account_code = models.CharField(max_length=64, blank=True)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "fin_expense_item"
        ordering = ["code"]
        verbose_name = "费用项"
        verbose_name_plural = "费用项"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"


class ExpenseRecord(BaseModel):
    DIRECTION_RECEIVABLE = "receivable"
    DIRECTION_PAYABLE = "payable"
    DIRECTION_EXTERNAL = "external"

    waybill = models.ForeignKey(
        "ops.Waybill", on_delete=models.CASCADE, related_name="expenses"
    )
    direction = models.CharField(max_length=16)
    expense_item_code = models.CharField(max_length=64)
    amount = models.DecimalField(max_digits=12, decimal_places=2)
    currency = models.CharField(max_length=8, default="CNY")
    occurred_at = models.DateTimeField(null=True, blank=True)
    risk_status = models.CharField(max_length=32, default="normal")
    source_system = models.CharField(max_length=64, blank=True)
    external_id = models.CharField(max_length=64, blank=True)
    # 上下游收/付款方：应付付给承运商/司机/油卡商，应收来自客户
    payee_type = models.CharField(max_length=16, blank=True, db_index=True, help_text="carrier/driver/fuel_card/customer/other")
    payee_ref = models.CharField(max_length=120, blank=True, help_text="收/付款方名称或标识")
    remark = models.CharField(max_length=255, blank=True)

    class Meta:
        db_table = "fin_expense_record"
        ordering = ["-created_at"]
        indexes = [
            models.Index(fields=["waybill", "direction"]),
            models.Index(fields=["direction", "risk_status"]),
        ]
        verbose_name = "费用记录"
        verbose_name_plural = "费用记录"

    def __str__(self) -> str:
        return f"{self.expense_item_code}:{self.amount}"


class PaymentRequest(BaseModel):
    request_no = models.CharField(max_length=64, unique=True)
    waybill = models.ForeignKey(
        "ops.Waybill", null=True, blank=True, on_delete=models.SET_NULL, related_name="payment_requests"
    )
    counterparty_type = models.CharField(max_length=32, blank=True)
    counterparty_ref = models.CharField(max_length=64, blank=True)
    amount = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    reason = models.CharField(max_length=255, blank=True)
    status = models.CharField(max_length=32, default="created")
    external_approval_no = models.CharField(max_length=64, blank=True)

    class Meta:
        db_table = "fin_payment_request"
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["status"])]
        verbose_name = "付款申请"
        verbose_name_plural = "付款申请"

    def __str__(self) -> str:
        return self.request_no


class PricingRule(BaseModel):
    PRICE_TYPE_INCOME = "income"
    PRICE_TYPE_COST = "cost"
    PRICE_TYPE_CHOICES = [(PRICE_TYPE_INCOME, "收入价"), (PRICE_TYPE_COST, "支出价")]

    name = models.CharField(max_length=120)
    price_type = models.CharField(max_length=16, choices=PRICE_TYPE_CHOICES)
    expense_item_code = models.CharField(max_length=64)
    # 匹配条件（留空表示通配）
    customer = models.ForeignKey("masterdata.Customer", null=True, blank=True, on_delete=models.CASCADE, related_name="pricing_rules")
    carrier = models.ForeignKey("masterdata.Carrier", null=True, blank=True, on_delete=models.CASCADE, related_name="pricing_rules")
    route_name = models.CharField(max_length=160, blank=True)
    vehicle_type = models.CharField(max_length=64, blank=True)
    # 计价
    base_price = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    price_per_ton = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    min_price = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    priority = models.IntegerField(default=0, help_text="数值大者优先")
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "fin_pricing_rule"
        ordering = ["-priority", "name"]
        indexes = [models.Index(fields=["price_type", "is_active"])]
        verbose_name = "报价规则"
        verbose_name_plural = "报价规则"

    def __str__(self) -> str:
        return f"{self.name}({self.price_type})"

    def quote(self, weight_ton) -> Decimal:
        amount = self.base_price + self.price_per_ton * Decimal(str(weight_ton or 0))
        return max(amount, self.min_price)


class Webhook(BaseModel):
    name = models.CharField(max_length=120)
    target_url = models.URLField()
    secret = models.CharField(max_length=80, blank=True)
    events = models.CharField(max_length=255, default="*", help_text="逗号分隔事件名；* 表示全部")
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "fin_webhook"
        ordering = ["-created_at"]
        verbose_name = "Webhook"
        verbose_name_plural = "Webhook"

    def __str__(self) -> str:
        return f"{self.name} -> {self.target_url}"

    def subscribes(self, event_type: str) -> bool:
        subs = {e.strip() for e in (self.events or "").split(",") if e.strip()}
        return "*" in subs or event_type in subs


class WebhookDelivery(BaseModel):
    webhook = models.ForeignKey(Webhook, on_delete=models.CASCADE, related_name="deliveries")
    event_type = models.CharField(max_length=64)
    payload = models.JSONField(default=dict, blank=True)
    status = models.CharField(max_length=16, default="pending", help_text="pending/success/failed")
    response_code = models.IntegerField(null=True, blank=True)
    attempts = models.IntegerField(default=0)

    class Meta:
        db_table = "fin_webhook_delivery"
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["status"])]
        verbose_name = "Webhook 投递"
        verbose_name_plural = "Webhook 投递"


class Statement(BaseModel):
    """对账单：按客户(应收)/承运商(应付)在账期内归集费用，供生成→确认→结算。"""

    DIRECTION_RECEIVABLE = "receivable"
    DIRECTION_PAYABLE = "payable"
    DIRECTION_CHOICES = [(DIRECTION_RECEIVABLE, "应收"), (DIRECTION_PAYABLE, "应付")]

    CP_CUSTOMER = "customer"
    CP_CARRIER = "carrier"
    CP_CHOICES = [(CP_CUSTOMER, "客户"), (CP_CARRIER, "承运商")]

    STATUS_DRAFT = "draft"
    STATUS_CONFIRMED = "confirmed"
    STATUS_SETTLED = "settled"
    STATUS_CHOICES = [(STATUS_DRAFT, "草稿"), (STATUS_CONFIRMED, "已确认"), (STATUS_SETTLED, "已结算")]

    statement_no = models.CharField(max_length=40, unique=True)
    direction = models.CharField(max_length=16, choices=DIRECTION_CHOICES)
    counterparty_type = models.CharField(max_length=16, choices=CP_CHOICES)
    counterparty_id = models.CharField(max_length=64, blank=True)
    counterparty_name = models.CharField(max_length=160, blank=True)
    period_start = models.DateField()
    period_end = models.DateField()
    total_amount = models.DecimalField(max_digits=14, decimal_places=2, default=0)
    item_count = models.IntegerField(default=0)
    external_total = models.DecimalField(max_digits=14, decimal_places=2, default=0, help_text="对方提供金额，用于差异稽核")
    status = models.CharField(max_length=16, choices=STATUS_CHOICES, default=STATUS_DRAFT)
    confirmed_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="confirmed_statements"
    )
    confirmed_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "fin_statement"
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["direction", "status"]), models.Index(fields=["counterparty_type", "counterparty_id"])]
        verbose_name = "对账单"
        verbose_name_plural = "对账单"

    @property
    def diff(self):
        return self.total_amount - self.external_total

    def __str__(self) -> str:
        return self.statement_no


class StatementLine(BaseModel):
    statement = models.ForeignKey(Statement, on_delete=models.CASCADE, related_name="lines")
    waybill_no = models.CharField(max_length=40, blank=True)
    expense_item_code = models.CharField(max_length=64, blank=True)
    amount = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    occurred_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "fin_statement_line"
        ordering = ["occurred_at"]
