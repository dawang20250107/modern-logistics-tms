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


class Reimbursement(BaseModel):
    """内部简易报销：勾选订单/运单提交报销 → 审批 → 下游付款（计入经营结果）。"""

    STATUS_SUBMITTED = "submitted"
    STATUS_APPROVED = "approved"
    STATUS_REJECTED = "rejected"
    STATUS_PAID = "paid"
    STATUS_CHOICES = [
        (STATUS_SUBMITTED, "已提交"),
        (STATUS_APPROVED, "已审批"),
        (STATUS_REJECTED, "已驳回"),
        (STATUS_PAID, "已付款"),
    ]
    CATEGORY_CHOICES = [
        ("freight_advance", "运费垫付"),
        ("toll", "过路费"),
        ("fuel", "油费"),
        ("loading", "装卸费"),
        ("lodging", "食宿"),
        ("other", "其他"),
    ]

    reimb_no = models.CharField(max_length=40, unique=True)
    waybill = models.ForeignKey(
        "ops.Waybill", null=True, blank=True, on_delete=models.SET_NULL, related_name="reimbursements"
    )
    order_no = models.CharField(max_length=40, blank=True, help_text="关联订单号（勾选订单提交时带入）")
    category = models.CharField(max_length=20, choices=CATEGORY_CHOICES, default="other", db_index=True)
    amount = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    reason = models.CharField(max_length=255, blank=True)
    status = models.CharField(max_length=16, choices=STATUS_CHOICES, default=STATUS_SUBMITTED, db_index=True)
    submitted_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="submitted_reimbursements"
    )
    approved_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="approved_reimbursements"
    )
    approved_at = models.DateTimeField(null=True, blank=True)
    paid_at = models.DateTimeField(null=True, blank=True)
    payment_request = models.ForeignKey(
        "finance.PaymentRequest", null=True, blank=True, on_delete=models.SET_NULL, related_name="reimbursements"
    )
    remark = models.CharField(max_length=255, blank=True)

    class Meta:
        db_table = "fin_reimbursement"
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["status"]), models.Index(fields=["waybill", "status"])]
        verbose_name = "报销"
        verbose_name_plural = "报销"

    def __str__(self) -> str:
        return self.reimb_no


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
    
    # 基础与阶梯计价 (Tiered Pricing)
    base_price = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    min_price = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    tier_prices = models.JSONField(
        default=list, 
        blank=True, 
        help_text="阶梯报价: [{'min_ton': 0, 'max_ton': 5, 'price': 200}, {'min_ton': 5, 'max_ton': 999, 'price': 180}]"
    )
    
    # 抛重与杂费换算规则 (Volumetric & Surcharges)
    volumetric_factor = models.DecimalField(max_digits=8, decimal_places=4, default=Decimal("0.3333"), help_text="重抛比，如 1方=333kg，填 0.3333")
    fuel_surcharge_pct = models.DecimalField(max_digits=6, decimal_places=4, default=0, help_text="燃油附加费率，如 2.5% 填 0.025")
    
    priority = models.IntegerField(default=0, help_text="数值大者优先")
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "fin_pricing_rule"
        ordering = ["-priority", "name"]
        indexes = [models.Index(fields=["price_type", "is_active"])]
        verbose_name = "多维报价规则"
        verbose_name_plural = "多维报价规则"

    def __str__(self) -> str:
        return f"{self.name}({self.price_type})"

    def quote(self, weight_ton, volume_cbm=0) -> dict:
        """
        执行多维阶梯报价计算：
        1. 计算计费重（Chargeable Weight）= max(物理重, 体积 * 重抛比)
        2. 匹配阶梯单价
        3. 计算基础运费 + 燃油附加费
        返回明细字典供外部调阅。
        """
        w = Decimal(str(weight_ton or 0))
        v = Decimal(str(volume_cbm or 0))
        
        # 1. 抛重计算
        vol_weight = v * self.volumetric_factor
        chargeable_weight = max(w, vol_weight)
        
        # 2. 阶梯匹配
        matched_price_per_ton = Decimal("0")
        if self.tier_prices:
            for tier in self.tier_prices:
                min_t = Decimal(str(tier.get("min_ton", 0)))
                max_t = Decimal(str(tier.get("max_ton", 999999)))
                if min_t <= chargeable_weight <= max_t:
                    matched_price_per_ton = Decimal(str(tier.get("price", 0)))
                    break
                    
        # 3. 基础运费
        freight_amount = self.base_price + (matched_price_per_ton * chargeable_weight)
        freight_amount = max(freight_amount, self.min_price)
        
        # 4. 燃油附加费
        fuel_surcharge = freight_amount * self.fuel_surcharge_pct
        
        total_amount = round(freight_amount + fuel_surcharge, 2)
        
        return {
            "amount": total_amount,
            "chargeable_weight": round(chargeable_weight, 3),
            "by_volume": vol_weight > w,
            "freight_amount": round(freight_amount, 2),
            "fuel_surcharge": round(fuel_surcharge, 2)
        }


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
    audited_at = models.DateTimeField(null=True, blank=True, help_text="最近一次 AI 异常审计时间")

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
    expense_record = models.ForeignKey(
        ExpenseRecord, null=True, blank=True, on_delete=models.SET_NULL, related_name="statement_lines"
    )
    waybill_no = models.CharField(max_length=40, blank=True)
    expense_item_code = models.CharField(max_length=64, blank=True)
    amount = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    occurred_at = models.DateTimeField(null=True, blank=True)
    # AI 异常审计结果（由 services.audit_statement 按同科目历史均值计算回填，非模拟）
    is_anomaly = models.BooleanField(default=False)
    baseline_avg = models.DecimalField(max_digits=12, decimal_places=2, null=True, blank=True)
    deviation_pct = models.DecimalField(max_digits=8, decimal_places=1, null=True, blank=True)

    class Meta:
        db_table = "fin_statement_line"
        ordering = ["occurred_at"]
