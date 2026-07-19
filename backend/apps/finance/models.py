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

    # ── 价格来源与规则快照（对账可解释：即使规则/价库后来改了，历史对账仍可复原） ──
    price_source = models.CharField(
        max_length=32, blank=True, db_index=True,
        help_text="价格来源：recommended(综合推荐)/cheapest(最低价)/lane_price(线路价库)/manual(人工)/platform(平台)/rule(规则)",
    )
    quote_id = models.CharField(max_length=64, blank=True, help_text="报价/线路价库条目标识")
    pricing_rule_id = models.CharField(max_length=64, blank=True)
    pricing_rule_name = models.CharField(max_length=120, blank=True)
    charge_method = models.CharField(max_length=32, blank=True, help_text="计费方式快照")
    matched_condition = models.CharField(max_length=255, blank=True, help_text="命中的匹配条件（客户/承运商/线路/车型）")
    input_snapshot = models.JSONField(default=dict, blank=True, help_text="计费输入快照（重量/体积/件数/里程）")
    calculation_detail = models.JSONField(default=dict, blank=True, help_text="计算明细（各段金额/附加费）")
    rule_snapshot = models.JSONField(default=dict, blank=True, help_text="规则字段快照（当时的单价/阶梯/下限等）")

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

    # 计费方式（覆盖公路货运主流口径）
    METHOD_TIERED_WEIGHT = "tiered_weight"  # 按重量阶梯（零担/泡重取大）
    METHOD_FLAT = "flat"                    # 整车一口价（一趟固定价，含包车）
    METHOD_PER_VOLUME = "per_volume"        # 按方（单价 × 计费方数）
    METHOD_PER_PIECE = "per_piece"          # 按件（单价 × 件数）
    METHOD_PER_KM = "per_km"                # 按公里（单价 × 里程）
    METHOD_PER_TON_KM = "per_ton_km"        # 吨公里（单价 × 计费吨 × 里程）
    CHARGE_METHOD_CHOICES = [
        (METHOD_TIERED_WEIGHT, "按重量阶梯"),
        (METHOD_FLAT, "整车一口价"),
        (METHOD_PER_VOLUME, "按方计费"),
        (METHOD_PER_PIECE, "按件计费"),
        (METHOD_PER_KM, "按公里计费"),
        (METHOD_PER_TON_KM, "吨公里计费"),
    ]

    name = models.CharField(max_length=120)
    price_type = models.CharField(max_length=16, choices=PRICE_TYPE_CHOICES)
    charge_method = models.CharField(
        max_length=16, choices=CHARGE_METHOD_CHOICES, default=METHOD_TIERED_WEIGHT
    )
    expense_item_code = models.CharField(max_length=64)
    # 匹配条件（留空表示通配）
    customer = models.ForeignKey("masterdata.Customer", null=True, blank=True, on_delete=models.CASCADE, related_name="pricing_rules")
    carrier = models.ForeignKey("masterdata.Carrier", null=True, blank=True, on_delete=models.CASCADE, related_name="pricing_rules")
    route_name = models.CharField(max_length=160, blank=True)
    vehicle_type = models.CharField(max_length=64, blank=True)
    
    # 基础与阶梯计价 (Tiered Pricing)
    base_price = models.DecimalField(max_digits=12, decimal_places=2, default=0, help_text="起步价/固定价")
    min_price = models.DecimalField(max_digits=12, decimal_places=2, default=0, help_text="最低计费额（金额下限）")
    unit_price = models.DecimalField(
        max_digits=12, decimal_places=2, default=0, help_text="按方/件/公里/吨公里的单价"
    )
    min_charge_qty = models.DecimalField(
        max_digits=12, decimal_places=3, default=0, help_text="最低计费量（方/件/吨，不足按此计）"
    )
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

    def quote(self, weight_ton, volume_cbm=0, quantity=0, distance_km=0) -> dict:
        """按计费方式（整车/阶梯重/方/件/公里/吨公里）计算运费。

        统一先算计费重（max(物理重, 体积×重抛比)），再按 charge_method 分支计价：
        基础运费 = 起步价 base_price + 计量部分；最后取 min_price 下限并叠加燃油附加费。
        min_charge_qty 为计量维度（方/件/吨）的最低计费量，不足按最低量计。
        返回明细字典供外部调阅。
        """
        w = Decimal(str(weight_ton or 0))
        v = Decimal(str(volume_cbm or 0))
        qty = Decimal(str(quantity or 0))
        dist = Decimal(str(distance_km or 0))
        floor_qty = Decimal(str(self.min_charge_qty or 0))

        vol_weight = v * self.volumetric_factor
        chargeable_weight = max(w, vol_weight)
        detail: dict = {}

        if self.charge_method == self.METHOD_FLAT:
            # 整车一口价：固定价，忽略计量
            freight_amount = self.base_price
        elif self.charge_method == self.METHOD_PER_VOLUME:
            billable = max(v, floor_qty)
            detail["billable_volume"] = float(round(billable, 3))
            freight_amount = self.base_price + self.unit_price * billable
        elif self.charge_method == self.METHOD_PER_PIECE:
            billable = max(qty, floor_qty)
            detail["billable_pieces"] = float(round(billable, 3))
            freight_amount = self.base_price + self.unit_price * billable
        elif self.charge_method == self.METHOD_PER_KM:
            detail["distance_km"] = float(round(dist, 2))
            freight_amount = self.base_price + self.unit_price * dist
        elif self.charge_method == self.METHOD_PER_TON_KM:
            billable_w = max(chargeable_weight, floor_qty)
            detail["ton_km"] = float(round(billable_w * dist, 2))
            freight_amount = self.base_price + self.unit_price * billable_w * dist
        else:
            # 按重量阶梯（默认）：匹配阶梯单价 × 计费重
            billable_w = max(chargeable_weight, floor_qty)
            matched_price_per_ton = Decimal("0")
            for tier in self.tier_prices or []:
                min_t = Decimal(str(tier.get("min_ton", 0)))
                max_t = Decimal(str(tier.get("max_ton", 999999)))
                if min_t <= billable_w <= max_t:
                    matched_price_per_ton = Decimal(str(tier.get("price", 0)))
                    break
            freight_amount = self.base_price + matched_price_per_ton * billable_w

        freight_amount = max(freight_amount, self.min_price)
        fuel_surcharge = freight_amount * self.fuel_surcharge_pct
        total_amount = round(freight_amount + fuel_surcharge, 2)

        return {
            "amount": total_amount,
            "charge_method": self.charge_method,
            "chargeable_weight": round(chargeable_weight, 3),
            "by_volume": vol_weight > w,
            "freight_amount": round(freight_amount, 2),
            "fuel_surcharge": round(fuel_surcharge, 2),
            **detail,
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
