"""运输执行：订单 / 运单 / 事件 / 轨迹 / 异常 / 回单。

运单是核心主链路。轨迹点为高频写入实体，经 Redis 队列削峰、批量异步落库；
此处建模与索引已按高并发读写设计。
"""

from django.conf import settings
from django.db import models
from django.utils import timezone

from apps.core.models import BaseModel, OrgScopedModel, SoftDeleteModel


class NumberCounter(models.Model):
    """单据号原子计数器（按 scope 维度，配合 select_for_update 保证并发唯一）。"""

    scope = models.CharField(max_length=64, unique=True)
    value = models.BigIntegerField(default=0)

    class Meta:
        db_table = "ops_number_counter"

    def __str__(self) -> str:
        return f"{self.scope}={self.value}"


class Order(BaseModel, SoftDeleteModel):
    # 建单渠道（多渠道统一入口）
    CHANNEL_CS = "cs"
    CHANNEL_SELF = "self"
    CHANNEL_MINIPROGRAM = "miniprogram"
    CHANNEL_WECHAT_GROUP = "wechat_group"
    CHANNEL_API = "api"
    CHANNEL_CHOICES = [
        (CHANNEL_CS, "客服代下"),
        (CHANNEL_SELF, "客户自助"),
        (CHANNEL_MINIPROGRAM, "小程序"),
        (CHANNEL_WECHAT_GROUP, "微信群"),
        (CHANNEL_API, "开放API"),
    ]

    # 客户来源类型
    SOURCE_INDIVIDUAL = "individual"
    SOURCE_ENTERPRISE = "enterprise"
    SOURCE_GOVERNMENT = "government"
    SOURCE_TYPE_CHOICES = [
        (SOURCE_INDIVIDUAL, "个人"),
        (SOURCE_ENTERPRISE, "企业"),
        (SOURCE_GOVERNMENT, "政府"),
    ]

    # 业务类型
    BIZ_FTL = "ftl"
    BIZ_LTL = "ltl"
    BIZ_EXPRESS = "express"
    BIZ_COLDCHAIN = "coldchain"
    BUSINESS_TYPE_CHOICES = [
        (BIZ_FTL, "整车"),
        (BIZ_LTL, "零担"),
        (BIZ_EXPRESS, "快递"),
        (BIZ_COLDCHAIN, "冷链"),
    ]

    PRIORITY_CHOICES = [("normal", "普通"), ("urgent", "加急"), ("vip", "VIP")]
    SETTLEMENT_CHOICES = [("monthly", "月结"), ("cash", "现结"), ("prepaid", "预付")]

    # 运费付款方式（中国货运核心：运费何时付）——与账期 settlement_type 正交
    FREIGHT_PREPAID = "prepaid"   # 现付/寄付：发货方提货时付
    FREIGHT_COLLECT = "collect"   # 到付：收货方送达时付
    FREIGHT_RECEIPT = "receipt"   # 回单付：回单收回后付
    FREIGHT_MONTHLY = "monthly"   # 月结：按账期结算
    FREIGHT_TERM_CHOICES = [
        (FREIGHT_PREPAID, "现付"),
        (FREIGHT_COLLECT, "到付"),
        (FREIGHT_RECEIPT, "回单付"),
        (FREIGHT_MONTHLY, "月结"),
    ]
    # 运费承担方（谁出这笔运费）
    PAYER_SHIPPER = "shipper"       # 发货方/寄付方
    PAYER_CONSIGNEE = "consignee"   # 收货方/到付方
    PAYER_THIRD = "third_party"     # 第三方
    FREIGHT_PAYER_CHOICES = [
        (PAYER_SHIPPER, "发货方"),
        (PAYER_CONSIGNEE, "收货方"),
        (PAYER_THIRD, "第三方"),
    ]
    # 代收货款 COD：司机代货主向收货人收取的货款（非运费），送达后回款给货主
    COD_NONE = "none"
    COD_PENDING = "pending"       # 待收
    COD_COLLECTED = "collected"   # 已收（司机已向收货人收妥）
    COD_REMITTED = "remitted"     # 已回款给货主
    COD_STATUS_CHOICES = [
        (COD_NONE, "无代收"),
        (COD_PENDING, "待代收"),
        (COD_COLLECTED, "已代收"),
        (COD_REMITTED, "已回款"),
    ]

    # 订单生命周期：建单→确认→进池→（调度认领）→派单转运单→完成→对账
    STATUS_DRAFT = "draft"
    STATUS_PENDING_CONFIRM = "pending_confirm"
    STATUS_CONFIRMED = "confirmed"
    STATUS_POOLED = "pooled"
    STATUS_DISPATCHING = "dispatching"
    STATUS_CONVERTED = "converted"  # 已派单（已转运单）
    STATUS_COMPLETED = "completed"
    STATUS_CANCELLED = "cancelled"
    STATUS_CHOICES = [
        (STATUS_DRAFT, "草稿"),
        (STATUS_PENDING_CONFIRM, "待确认"),
        (STATUS_CONFIRMED, "已确认"),
        (STATUS_POOLED, "订单池"),
        (STATUS_DISPATCHING, "调度中"),
        (STATUS_CONVERTED, "已派单"),
        (STATUS_COMPLETED, "已完成"),
        (STATUS_CANCELLED, "已取消"),
    ]

    order_no = models.CharField(max_length=40, unique=True)
    customer = models.ForeignKey(
        "masterdata.Customer", null=True, blank=True, on_delete=models.SET_NULL, related_name="orders"
    )
    channel = models.CharField(max_length=24, choices=CHANNEL_CHOICES, default=CHANNEL_CS, db_index=True)
    source = models.CharField(max_length=32, blank=True, help_text="渠道内来源标识，如群名/坐席")
    source_type = models.CharField(max_length=16, choices=SOURCE_TYPE_CHOICES, default=SOURCE_ENTERPRISE)
    business_type = models.CharField(max_length=16, choices=BUSINESS_TYPE_CHOICES, default=BIZ_FTL)
    priority = models.CharField(max_length=16, choices=PRIORITY_CHOICES, default="normal")
    settlement_type = models.CharField(max_length=16, choices=SETTLEMENT_CHOICES, default="monthly")
    freight_term = models.CharField(
        max_length=16, choices=FREIGHT_TERM_CHOICES, default=FREIGHT_PREPAID, help_text="运费付款方式"
    )
    freight_payer = models.CharField(
        max_length=16, choices=FREIGHT_PAYER_CHOICES, default=PAYER_SHIPPER, help_text="运费承担方"
    )
    cod_amount = models.DecimalField(
        max_digits=14, decimal_places=2, default=0, help_text="代收货款金额（司机代货主向收货人收取）"
    )
    cod_status = models.CharField(max_length=16, choices=COD_STATUS_CHOICES, default=COD_NONE)
    status = models.CharField(max_length=32, default=STATUS_PENDING_CONFIRM, db_index=True)

    # 联系人（兼容旧字段：通用联系人 = 发货联系人）
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)

    # 收发货（城市级 origin/destination 兼容既有；新增详细地址与两端联系人）
    origin = models.CharField(max_length=120, blank=True)
    destination = models.CharField(max_length=120, blank=True)
    pickup_address = models.CharField(max_length=255, blank=True)
    pickup_contact_name = models.CharField(max_length=64, blank=True)
    pickup_contact_phone = models.CharField(max_length=32, blank=True)
    delivery_address = models.CharField(max_length=255, blank=True)
    delivery_contact_name = models.CharField(max_length=64, blank=True)
    delivery_contact_phone = models.CharField(max_length=32, blank=True)

    # 货物
    cargo_desc = models.CharField(max_length=255, blank=True)
    cargo_quantity = models.IntegerField(default=0)
    cargo_weight_ton = models.DecimalField(max_digits=10, decimal_places=2, default=0)
    cargo_volume_cbm = models.DecimalField(max_digits=10, decimal_places=2, default=0)
    cargo_value = models.DecimalField(max_digits=14, decimal_places=2, default=0, help_text="货值（保险/风控）")
    package_type = models.CharField(max_length=32, blank=True)
    is_hazardous = models.BooleanField(default=False)
    temperature_range = models.CharField(max_length=32, blank=True, help_text="冷链温区，如 -18~0")

    # 报价（运营，不做利润看板）
    quoted_amount = models.DecimalField(max_digits=14, decimal_places=2, default=0)

    expected_pickup_at = models.DateTimeField(null=True, blank=True)
    expected_delivery_at = models.DateTimeField(null=True, blank=True)

    # SLA 时效
    SLA_PENDING = "pending"
    SLA_AT_RISK = "at_risk"
    SLA_ON_TIME = "on_time"
    SLA_BREACHED = "breached"
    SLA_CHOICES = [
        (SLA_PENDING, "进行中"),
        (SLA_AT_RISK, "临期"),
        (SLA_ON_TIME, "准时"),
        (SLA_BREACHED, "超时"),
    ]
    sla_status = models.CharField(max_length=16, choices=SLA_CHOICES, default=SLA_PENDING, db_index=True)
    delivered_at = models.DateTimeField(null=True, blank=True)

    # 审批流：高价值/特殊订单需主管审批后方可进池派单
    APPROVAL_NONE = "none"
    APPROVAL_PENDING = "pending"
    APPROVAL_APPROVED = "approved"
    APPROVAL_REJECTED = "rejected"
    APPROVAL_CHOICES = [
        (APPROVAL_NONE, "无需审批"),
        (APPROVAL_PENDING, "待审批"),
        (APPROVAL_APPROVED, "已通过"),
        (APPROVAL_REJECTED, "已驳回"),
    ]
    approval_status = models.CharField(max_length=16, choices=APPROVAL_CHOICES, default=APPROVAL_NONE, db_index=True)
    approval_remark = models.CharField(max_length=255, blank=True)
    approved_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="approved_orders"
    )
    approved_at = models.DateTimeField(null=True, blank=True)

    # 调度池认领（多调度并发，乐观+悲观锁保护）
    claimed_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="claimed_orders"
    )
    claimed_at = models.DateTimeField(null=True, blank=True)
    pooled_at = models.DateTimeField(null=True, blank=True)

    created_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="created_orders"
    )
    raw_text = models.TextField(blank=True, help_text="原始消息（微信群/自然语言建单）")
    ai_conversation_id = models.CharField(max_length=64, blank=True, db_index=True, help_text="AI会话ID（关联智能客服对话）")
    parse_meta = models.JSONField(default=dict, blank=True, help_text="AI 解析来源与置信信息")
    remark = models.CharField(max_length=255, blank=True)

    class Meta:
        db_table = "ops_order"
        ordering = ["-created_at"]
        indexes = [
            models.Index(fields=["channel", "status"]),
            models.Index(fields=["status", "priority"]),
            models.Index(fields=["created_by", "status"]),
            models.Index(fields=["claimed_by", "status"]),
            models.Index(fields=["status", "-created_at"]),  # 列表默认排序 + 状态筛选
        ]
        verbose_name = "订单"
        verbose_name_plural = "订单"

    def __str__(self) -> str:
        return self.order_no


class OrderCargoItem(BaseModel):
    """订单货物明细行：支持一单多品类/多件型，汇总回写订单货量。"""

    order = models.ForeignKey(Order, on_delete=models.CASCADE, related_name="cargo_items")
    seq = models.PositiveIntegerField(default=1)
    name = models.CharField(max_length=120)
    quantity = models.IntegerField(default=0)
    weight_ton = models.DecimalField(max_digits=10, decimal_places=2, default=0)
    volume_cbm = models.DecimalField(max_digits=10, decimal_places=2, default=0)
    package_type = models.CharField(max_length=32, blank=True)
    temperature_range = models.CharField(max_length=32, blank=True)
    remark = models.CharField(max_length=255, blank=True)

    class Meta:
        db_table = "ops_order_cargo_item"
        ordering = ["seq"]
        verbose_name = "货物明细"
        verbose_name_plural = "货物明细"

    def __str__(self) -> str:
        return f"{self.name} x{self.quantity}"


class OrderStop(BaseModel):
    """订单装卸站点：支持多提多送（多装多卸），按 seq 排序。"""

    STOP_PICKUP = "pickup"
    STOP_DELIVERY = "delivery"
    STOP_TYPE_CHOICES = [(STOP_PICKUP, "提货"), (STOP_DELIVERY, "送货")]

    order = models.ForeignKey(Order, on_delete=models.CASCADE, related_name="stops")
    seq = models.PositiveIntegerField(default=1)
    stop_type = models.CharField(max_length=12, choices=STOP_TYPE_CHOICES, default=STOP_PICKUP)
    city = models.CharField(max_length=80, blank=True)
    address = models.CharField(max_length=255, blank=True)
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)
    expected_start = models.DateTimeField(null=True, blank=True)
    expected_end = models.DateTimeField(null=True, blank=True)
    cargo_note = models.CharField(max_length=255, blank=True)

    class Meta:
        db_table = "ops_order_stop"
        ordering = ["seq"]
        verbose_name = "装卸站点"
        verbose_name_plural = "装卸站点"

    def __str__(self) -> str:
        return f"{self.get_stop_type_display()}#{self.seq} {self.city}"


class OrderTemplate(BaseModel, SoftDeleteModel):
    """录单模板：保存常用订单（字段+货物明细+站点）为模板，一键套用建单。"""

    name = models.CharField(max_length=120)
    created_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="order_templates"
    )
    payload = models.JSONField(default=dict, help_text="订单字段 + 货物明细 + 站点 的快照")

    class Meta:
        db_table = "ops_order_template"
        ordering = ["-created_at"]
        verbose_name = "录单模板"
        verbose_name_plural = "录单模板"

    def __str__(self) -> str:
        return self.name


class OrderAttachment(BaseModel):
    """订单附件：合同 / 委托书 / 货物照片 / 其他单据。"""

    KIND_CONTRACT = "contract"
    KIND_AUTHORIZATION = "authorization"
    KIND_PHOTO = "photo"
    KIND_OTHER = "other"
    KIND_CHOICES = [
        (KIND_CONTRACT, "合同"),
        (KIND_AUTHORIZATION, "委托书"),
        (KIND_PHOTO, "货物照片"),
        (KIND_OTHER, "其他"),
    ]

    order = models.ForeignKey(Order, on_delete=models.CASCADE, related_name="attachments")
    kind = models.CharField(max_length=16, choices=KIND_CHOICES, default=KIND_OTHER)
    name = models.CharField(max_length=160, blank=True)
    file = models.FileField(upload_to="order_attachments/", null=True, blank=True)
    file_url = models.URLField(blank=True, help_text="外部已上传文件 URL")
    uploaded_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="order_attachments"
    )

    class Meta:
        db_table = "ops_order_attachment"
        ordering = ["-created_at"]
        verbose_name = "订单附件"
        verbose_name_plural = "订单附件"

    def __str__(self) -> str:
        return f"{self.get_kind_display()} {self.name}"


class OrderEvent(BaseModel):
    """订单全生命周期事件溯源：建单/确认/进池/认领/派单/完成/取消等留痕。"""

    order = models.ForeignKey(Order, on_delete=models.CASCADE, related_name="events")
    event_type = models.CharField(max_length=48)
    from_status = models.CharField(max_length=32, blank=True)
    to_status = models.CharField(max_length=32, blank=True)
    actor = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="order_events"
    )
    source = models.CharField(max_length=24, blank=True, help_text="cs/dispatch/system/ai")
    payload = models.JSONField(default=dict, blank=True)
    event_time = models.DateTimeField(default=timezone.now, db_index=True)

    class Meta:
        db_table = "ops_order_event"
        ordering = ["event_time"]
        indexes = [models.Index(fields=["order", "event_time"])]
        verbose_name = "订单事件"
        verbose_name_plural = "订单事件"

    def __str__(self) -> str:
        return f"{self.order_id}:{self.event_type}"


class Waybill(BaseModel, OrgScopedModel):
    # 运单状态机
    STATUS_DRAFT = "draft"
    STATUS_PENDING_DISPATCH = "pending_dispatch"
    STATUS_DISPATCHED = "dispatched"
    STATUS_LOADED = "loaded"
    STATUS_DEPARTED = "departed"
    STATUS_IN_TRANSIT = "in_transit"
    STATUS_ARRIVED = "arrived"
    STATUS_PARTIALLY_SIGNED = "partially_signed"
    STATUS_REJECTED = "rejected"
    STATUS_SIGNED = "signed"
    STATUS_DELIVERED = "delivered"
    STATUS_SETTLED = "settled"
    STATUS_CANCELLED = "cancelled"
    STATUS_VOIDED = "voided"
    STATUS_CHOICES = [
        (STATUS_DRAFT, "草稿"),
        (STATUS_PENDING_DISPATCH, "待调度"),
        (STATUS_DISPATCHED, "已派车"),
        (STATUS_LOADED, "已装车"),
        (STATUS_DEPARTED, "已发车"),
        (STATUS_IN_TRANSIT, "运输中"),
        (STATUS_ARRIVED, "已到达"),
        (STATUS_PARTIALLY_SIGNED, "部分签收"),
        (STATUS_REJECTED, "已拒收"),
        (STATUS_SIGNED, "已签收"),
        (STATUS_DELIVERED, "已送达"),
        (STATUS_SETTLED, "已结算"),
        (STATUS_CANCELLED, "已取消"),
        (STATUS_VOIDED, "已作废"),
    ]

    RISK_HIGH = "high"
    RISK_MEDIUM = "medium"
    RISK_LOW = "low"
    RISK_NONE = "none"
    RISK_CHOICES = [(RISK_HIGH, "高"), (RISK_MEDIUM, "中"), (RISK_LOW, "低"), (RISK_NONE, "无")]

    # 派单类型：自有单车 / 自有车队 / 三方承运商
    # 承运三通道：自营（自有单车/车队）、外包承运商、网货平台（接第三方平台）
    DISPATCH_OWN = "own_vehicle"
    DISPATCH_FLEET = "fleet"
    DISPATCH_THIRD_PARTY = "third_party"
    DISPATCH_PLATFORM = "platform"
    DISPATCH_TYPE_CHOICES = [
        (DISPATCH_OWN, "自营单车"),
        (DISPATCH_FLEET, "自营车队"),
        (DISPATCH_THIRD_PARTY, "外包承运商"),
        (DISPATCH_PLATFORM, "网货平台"),
    ]
    # 承运通道大类（自营 / 外包 / 网货），便于分通道对账与利润
    CHANNEL_LABELS = {
        DISPATCH_OWN: "自营", DISPATCH_FLEET: "自营",
        DISPATCH_THIRD_PARTY: "外包", DISPATCH_PLATFORM: "网货",
    }

    waybill_no = models.CharField(max_length=40, unique=True)
    dispatch_type = models.CharField(max_length=16, choices=DISPATCH_TYPE_CHOICES, blank=True)
    # 网货平台通道：对接第三方平台（满帮/路歌等），合规由平台承担，我方只记录对接信息
    platform_name = models.CharField(max_length=64, blank=True, help_text="网货平台名称，如 满帮/路歌")
    platform_order_no = models.CharField(max_length=64, blank=True, help_text="平台侧运单/订单号")
    order = models.ForeignKey(
        Order, null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills"
    )
    customer = models.ForeignKey(
        "masterdata.Customer", null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills"
    )
    carrier = models.ForeignKey(
        "masterdata.Carrier", null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills"
    )
    vehicle = models.ForeignKey(
        "masterdata.Vehicle", null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills"
    )
    driver = models.ForeignKey(
        "masterdata.Driver", null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills"
    )
    trailer = models.ForeignKey(
        "masterdata.Vehicle", null=True, blank=True, on_delete=models.SET_NULL, related_name="trailer_waybills",
        help_text="挂车（牵引车 vehicle + 挂车 trailer）",
    )

    route_name = models.CharField(max_length=160)
    ai_conversation_id = models.CharField(max_length=64, blank=True, db_index=True, help_text="AI会话ID（沿订单带入）")
    planned_route = models.ForeignKey(
        "masterdata.Route", null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills"
    )
    parent = models.ForeignKey(
        "self", null=True, blank=True, on_delete=models.SET_NULL, related_name="children",
        help_text="拆单/合单血缘：拆出的子单指向原单；合并的源单指向合并单",
    )
    origin = models.CharField(max_length=80, blank=True)
    destination = models.CharField(max_length=80, blank=True)
    status = models.CharField(max_length=32, default=STATUS_PENDING_DISPATCH, choices=STATUS_CHOICES)
    dispatch_status = models.CharField(max_length=32, default="pending_accept")
    risk_level = models.CharField(max_length=16, default=RISK_NONE, choices=RISK_CHOICES)
    receipt_status = models.CharField(max_length=32, default="not_due")
    eta_drift_minutes = models.IntegerField(default=0)

    cargo_quantity = models.IntegerField(default=0)
    cargo_weight_ton = models.DecimalField(max_digits=10, decimal_places=2, default=0)
    cargo_volume_cbm = models.DecimalField(max_digits=10, decimal_places=2, default=0)

    # 运费付款方式与代收货款（自订单带入；司机端到付/代收依据）
    freight_term = models.CharField(
        max_length=16, choices=Order.FREIGHT_TERM_CHOICES, default=Order.FREIGHT_PREPAID
    )
    freight_payer = models.CharField(
        max_length=16, choices=Order.FREIGHT_PAYER_CHOICES, default=Order.PAYER_SHIPPER
    )
    cod_amount = models.DecimalField(max_digits=14, decimal_places=2, default=0)
    cod_status = models.CharField(max_length=16, choices=Order.COD_STATUS_CHOICES, default=Order.COD_NONE)
    cod_collected_at = models.DateTimeField(null=True, blank=True)
    cod_remitted_at = models.DateTimeField(null=True, blank=True)

    planned_arrival = models.DateTimeField(null=True, blank=True)
    estimated_arrival = models.DateTimeField(null=True, blank=True)
    # 关键里程碑实际时间（从状态流转/围栏物化，便于 SLA 与查询）
    loaded_at = models.DateTimeField(null=True, blank=True)
    departed_at = models.DateTimeField(null=True, blank=True)
    arrived_at = models.DateTimeField(null=True, blank=True)
    signed_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "ops_waybill"
        ordering = ["-created_at"]
        indexes = [
            models.Index(fields=["status", "risk_level"]),
            models.Index(fields=["receipt_status"]),
            models.Index(fields=["-eta_drift_minutes"]),
            models.Index(fields=["customer", "status"]),
            models.Index(fields=["status", "-created_at"]),  # 列表默认排序 + 状态筛选
            models.Index(fields=["driver", "status"]),  # 司机端在途任务查询
        ]
        verbose_name = "运单"
        verbose_name_plural = "运单"

    def __str__(self) -> str:
        return self.waybill_no


class WaybillDriver(BaseModel):
    """运单司机分配：支持多司机同行（主驾/副驾/接力）。primary driver 仍保留在 Waybill.driver。"""

    ROLE_MAIN = "main"
    ROLE_CO = "co"
    ROLE_RELAY = "relay"
    ROLE_CHOICES = [
        (ROLE_MAIN, "主驾"),
        (ROLE_CO, "副驾"),
        (ROLE_RELAY, "接力"),
    ]

    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="driver_assignments")
    driver = models.ForeignKey("masterdata.Driver", on_delete=models.CASCADE, related_name="waybill_assignments")
    role = models.CharField(max_length=12, choices=ROLE_CHOICES, default=ROLE_MAIN, db_index=True)
    note = models.CharField(max_length=120, blank=True, help_text="负责区间/备注，如 武汉→成都")

    class Meta:
        db_table = "ops_waybill_driver"
        ordering = ["role", "created_at"]
        constraints = [
            models.UniqueConstraint(fields=["waybill", "driver"], name="uniq_waybill_driver"),
        ]
        verbose_name = "运单司机"
        verbose_name_plural = "运单司机"

    def __str__(self) -> str:
        return f"{self.waybill_no}:{self.driver_id}:{self.role}"

    @property
    def waybill_no(self) -> str:
        return self.waybill.waybill_no if self.waybill_id else ""


class WaybillStop(BaseModel):
    """运单执行点位：从订单点位拷贝进执行层，记录计划/实际到达离开时间（GPS 围栏自动盖戳）。"""

    STOP_PICKUP = "pickup"
    STOP_DELIVERY = "delivery"
    STOP_TYPE_CHOICES = [(STOP_PICKUP, "提货"), (STOP_DELIVERY, "送货")]

    STATUS_PENDING = "pending"
    STATUS_ARRIVED = "arrived"
    STATUS_DEPARTED = "departed"
    STATUS_CHOICES = [
        (STATUS_PENDING, "待到达"),
        (STATUS_ARRIVED, "已到达"),
        (STATUS_DEPARTED, "已离开"),
    ]

    SRC_GPS = "gps"
    SRC_MANUAL = "manual"
    SRC_SMS = "sms"

    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="stops")
    seq = models.PositiveIntegerField(default=1)
    stop_type = models.CharField(max_length=12, choices=STOP_TYPE_CHOICES, default=STOP_PICKUP)
    city = models.CharField(max_length=80, blank=True)
    address = models.CharField(max_length=255, blank=True)
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)
    # 围栏中心与半径（设了坐标才能自动盖戳）
    lat = models.DecimalField(max_digits=10, decimal_places=6, null=True, blank=True)
    lng = models.DecimalField(max_digits=10, decimal_places=6, null=True, blank=True)
    radius_m = models.PositiveIntegerField(default=800, help_text="到达围栏半径(米)")
    planned_eta = models.DateTimeField(null=True, blank=True)
    actual_arrival_at = models.DateTimeField(null=True, blank=True)
    actual_depart_at = models.DateTimeField(null=True, blank=True)
    arrival_source = models.CharField(max_length=12, blank=True, help_text="gps/manual/sms")
    status = models.CharField(max_length=12, choices=STATUS_CHOICES, default=STATUS_PENDING, db_index=True)
    note = models.CharField(max_length=255, blank=True)

    class Meta:
        db_table = "ops_waybill_stop"
        ordering = ["waybill", "seq"]
        indexes = [models.Index(fields=["waybill", "seq"])]
        verbose_name = "运单点位"
        verbose_name_plural = "运单点位"

    def __str__(self) -> str:
        return f"{self.waybill_id}:{self.seq}:{self.stop_type}"


class Contract(BaseModel):
    """合同库：司机承运合同生成/发送/确认/PDF归档（对应方案"发合同→引导注册"节点）。"""

    STATUS_PENDING = "pending"      # 待发送
    STATUS_SENT = "sent"            # 已发送待确认
    STATUS_CONFIRMED = "confirmed"  # 司机已确认
    STATUS_REJECTED = "rejected"    # 司机拒签
    STATUS_CHOICES = [
        (STATUS_PENDING, "待发送"),
        (STATUS_SENT, "已发送"),
        (STATUS_CONFIRMED, "已确认"),
        (STATUS_REJECTED, "已拒签"),
    ]

    contract_no = models.CharField(max_length=40, unique=True)
    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="contracts")
    driver = models.ForeignKey(
        "masterdata.Driver", null=True, blank=True, on_delete=models.SET_NULL, related_name="contracts"
    )
    template_code = models.CharField(max_length=32, default="standard")
    content = models.TextField(blank=True, help_text="合同内容（文本，PDF 由此生成）")
    sent_at = models.DateTimeField(null=True, blank=True, help_text="AI发送时间")
    driver_reply = models.CharField(max_length=255, blank=True, help_text="司机回复")
    confirm_status = models.CharField(max_length=16, choices=STATUS_CHOICES, default=STATUS_PENDING, db_index=True)
    confirmed_at = models.DateTimeField(null=True, blank=True)
    pdf = models.FileField(upload_to="contracts/", null=True, blank=True, help_text="自动生成的合同PDF")

    class Meta:
        db_table = "ops_contract"
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["waybill", "confirm_status"])]
        verbose_name = "合同"
        verbose_name_plural = "合同"

    def __str__(self) -> str:
        return self.contract_no


class WaybillEvent(BaseModel):
    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="events")
    event_type = models.CharField(max_length=64)
    event_time = models.DateTimeField()
    resource = models.CharField(max_length=80, blank=True)
    source = models.CharField(max_length=32, blank=True)
    payload = models.JSONField(default=dict, blank=True)

    class Meta:
        db_table = "ops_waybill_event"
        ordering = ["event_time"]
        indexes = [
            models.Index(fields=["waybill", "event_time"]),
            models.Index(fields=["event_type", "event_time"]),
        ]
        verbose_name = "运单事件"
        verbose_name_plural = "运单事件"

    def __str__(self) -> str:
        return f"{self.waybill_id} {self.event_type}"


class TrackingPoint(BaseModel):
    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="tracking_points")
    lng = models.DecimalField(max_digits=10, decimal_places=6)
    lat = models.DecimalField(max_digits=10, decimal_places=6)
    speed_kmh = models.DecimalField(max_digits=8, decimal_places=2, default=0)
    reported_at = models.DateTimeField()
    provider = models.CharField(max_length=32, blank=True)

    class Meta:
        db_table = "ops_tracking_point"
        ordering = ["reported_at"]
        indexes = [models.Index(fields=["waybill", "reported_at"])]
        verbose_name = "轨迹点"
        verbose_name_plural = "轨迹点"


class ExceptionRecord(BaseModel):
    LEVEL_CHOICES = [("low", "低"), ("medium", "中"), ("high", "高")]
    STATUS_PENDING = "pending_handle"
    STATUS_HANDLING = "handling"
    STATUS_PENDING_AUDIT = "pending_audit"
    STATUS_CLOSED = "closed"
    STATUS_REJECTED = "rejected"
    # 人工提报类型（与前端 EXC_TYPE_LABEL 对齐）+ 车联网告警自动生成类型（与 telematics.Alert.TYPE_CHOICES 对齐）
    EXCEPTION_TYPE_CHOICES = [
        ("transit_delay", "在途超时"),
        ("route_deviation", "偏航/路线异常"),
        ("cargo_damage", "货损货差"),
        ("vehicle_breakdown", "车辆故障"),
        ("detained", "扣车扣货"),
        ("customer_complaint", "客户投诉"),
        ("temperature", "冷链温度异常"),
        ("fuel", "油耗/漏油异常"),
        ("overspeed", "超速驾驶"),
        ("fatigue", "疲劳驾驶"),
        ("deviation", "偏航（车联网）"),
        ("abnormal_stop", "异常停车"),
        ("geofence", "围栏进出"),
        ("offline", "设备离线"),
        ("receipt_pending", "回单待确认"),
        ("other", "其他"),
    ]

    waybill = models.ForeignKey(
        Waybill, null=True, blank=True, on_delete=models.SET_NULL, related_name="exceptions"
    )
    exception_type = models.CharField(max_length=64, choices=EXCEPTION_TYPE_CHOICES)
    level = models.CharField(max_length=16, choices=LEVEL_CHOICES, default="medium")
    source = models.CharField(max_length=32, default="manual", help_text="manual/track/ocr/customer")
    description = models.TextField(blank=True)
    status = models.CharField(max_length=32, default=STATUS_PENDING)
    assignee = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="assigned_exceptions"
    )
    responsibility_party = models.CharField(max_length=80, blank=True)
    amount = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    resolution = models.TextField(blank=True)

    class Meta:
        db_table = "ops_exception"
        ordering = ["-created_at"]
        indexes = [
            models.Index(fields=["exception_type", "status"]),
            models.Index(fields=["level", "status"]),
        ]
        verbose_name = "异常"
        verbose_name_plural = "异常"

    def __str__(self) -> str:
        return f"{self.exception_type}:{self.status}"


class ExceptionEvent(BaseModel):
    """异常处置事件溯源：立案/认领/AI 诊断/驳回/闭环等留痕（与 OrderEvent/WaybillEvent 同构）。"""

    exception = models.ForeignKey(ExceptionRecord, on_delete=models.CASCADE, related_name="events")
    event_type = models.CharField(max_length=48)
    from_status = models.CharField(max_length=32, blank=True)
    to_status = models.CharField(max_length=32, blank=True)
    actor = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="exception_events"
    )
    note = models.CharField(max_length=255, blank=True)
    payload = models.JSONField(default=dict, blank=True)
    event_time = models.DateTimeField(default=timezone.now, db_index=True)

    class Meta:
        db_table = "ops_exception_event"
        ordering = ["event_time"]
        indexes = [models.Index(fields=["exception", "event_time"])]
        verbose_name = "异常处置事件"
        verbose_name_plural = "异常处置事件"

    def __str__(self) -> str:
        return f"{self.exception_id}:{self.event_type}"


class Receipt(BaseModel):
    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="receipts")
    receipt_type = models.CharField(max_length=32, default="signed_pod")
    status = models.CharField(max_length=32, default="uploaded", help_text="uploaded/confirmed/rejected")
    file = models.FileField(upload_to="receipts/", null=True, blank=True)
    file_url = models.URLField(blank=True, help_text="外部已上传文件 URL")
    ocr_status = models.CharField(max_length=16, default="pending", help_text="pending/processing/done/failed")
    ocr_result = models.JSONField(default=dict, blank=True)
    signatory = models.CharField(max_length=80, blank=True)
    signed_at = models.DateTimeField(null=True, blank=True)
    signature = models.TextField(blank=True, help_text="电子签名（dataURL/base64）")
    sign_source = models.CharField(max_length=16, blank=True, help_text="driver/customer")
    # 签收结果：整签 / 部分签收 / 拒收（货损货差记数量差，供理赔与对账用）
    OUTCOME_FULL = "full"
    OUTCOME_PARTIAL = "partial"
    OUTCOME_REJECTED = "rejected"
    OUTCOME_CHOICES = [(OUTCOME_FULL, "整签"), (OUTCOME_PARTIAL, "部分签收"), (OUTCOME_REJECTED, "拒收")]
    outcome = models.CharField(max_length=16, choices=OUTCOME_CHOICES, default=OUTCOME_FULL)
    total_quantity = models.DecimalField(max_digits=12, decimal_places=2, default=0, help_text="应收件数/数量")
    signed_quantity = models.DecimalField(max_digits=12, decimal_places=2, default=0, help_text="实收件数/数量")
    damaged_quantity = models.DecimalField(max_digits=12, decimal_places=2, default=0, help_text="货损数量")
    shortage_quantity = models.DecimalField(max_digits=12, decimal_places=2, default=0, help_text="货差（短少）数量")
    rejection_reason = models.CharField(max_length=255, blank=True, help_text="拒收/异常原因")
    uploaded_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="uploaded_receipts"
    )

    class Meta:
        db_table = "ops_receipt"
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["waybill", "status"]), models.Index(fields=["ocr_status"])]
        verbose_name = "回单"
        verbose_name_plural = "回单"

    def __str__(self) -> str:
        return f"{self.waybill_id}:{self.receipt_type}"


class ReminderTemplate(BaseModel, SoftDeleteModel):
    """作业提醒富文本回复库：调度可维护常用提醒模板（装货要求/在途打卡/回单寄回/安全等）。"""

    name = models.CharField(max_length=120)
    category = models.CharField(max_length=32, blank=True, help_text="分类，如 装货/打卡/回单/安全")
    content = models.TextField(help_text="富文本内容（HTML/纯文本）")
    is_active = models.BooleanField(default=True)
    created_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="reminder_templates"
    )

    class Meta:
        db_table = "ops_reminder_template"
        ordering = ["category", "name"]
        verbose_name = "提醒模板"
        verbose_name_plural = "提醒模板"

    def __str__(self) -> str:
        return self.name


class DriverReminder(BaseModel):
    """下发给司机的作业提醒：司机端强制弹窗，点击「确认收到」后记录确认时间。"""

    STATUS_PENDING = "pending"
    STATUS_ACKNOWLEDGED = "acknowledged"

    LEVEL_NORMAL = "normal"
    LEVEL_IMPORTANT = "important"
    LEVEL_URGENT = "urgent"
    LEVEL_CHOICES = [
        (LEVEL_NORMAL, "普通"),
        (LEVEL_IMPORTANT, "重要"),
        (LEVEL_URGENT, "紧急"),
    ]

    waybill = models.ForeignKey(Waybill, null=True, blank=True, on_delete=models.CASCADE, related_name="reminders")
    driver = models.ForeignKey(
        "masterdata.Driver", null=True, blank=True, on_delete=models.SET_NULL, related_name="reminders"
    )
    template = models.ForeignKey(
        ReminderTemplate, null=True, blank=True, on_delete=models.SET_NULL, related_name="reminders"
    )
    title = models.CharField(max_length=120, default="作业提醒")
    content = models.TextField()
    level = models.CharField(max_length=16, choices=LEVEL_CHOICES, default=LEVEL_IMPORTANT, help_text="普通/重要/紧急")
    ack_required = models.BooleanField(default=True, help_text="是否强制确认")
    status = models.CharField(max_length=16, default=STATUS_PENDING, db_index=True)
    sent_at = models.DateTimeField(default=timezone.now)
    acknowledged_at = models.DateTimeField(null=True, blank=True)
    sent_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="sent_reminders"
    )

    class Meta:
        db_table = "ops_driver_reminder"
        ordering = ["-sent_at"]
        indexes = [models.Index(fields=["driver", "status"]), models.Index(fields=["waybill", "status"])]
        verbose_name = "司机提醒"
        verbose_name_plural = "司机提醒"

    def __str__(self) -> str:
        return f"{self.driver_id}:{self.title}:{self.status}"


class DriverCheckin(BaseModel):
    """司机端打卡签到：各流程节点自动定位 + 上传水印照片。"""

    NODE_CHOICES = [
        ("depart", "出发"),
        ("arrive_pickup", "到达装货地"),
        ("queuing", "排队"),
        ("loading", "装货"),
        ("depart_loaded", "发车"),
        ("in_transit", "在途打卡"),
        ("arrive_delivery", "到达卸货地"),
        ("unloading", "卸货"),
        ("receipt", "回单"),
        ("finish", "订单结束"),
    ]

    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="checkins")
    driver = models.ForeignKey(
        "masterdata.Driver", null=True, blank=True, on_delete=models.SET_NULL, related_name="checkins"
    )
    node = models.CharField(max_length=20, choices=NODE_CHOICES, db_index=True)
    lat = models.DecimalField(max_digits=10, decimal_places=6, null=True, blank=True)
    lng = models.DecimalField(max_digits=10, decimal_places=6, null=True, blank=True)
    photo = models.FileField(upload_to="checkins/", null=True, blank=True, help_text="水印照片")
    note = models.CharField(max_length=255, blank=True)
    checkin_at = models.DateTimeField(default=timezone.now)

    class Meta:
        db_table = "ops_driver_checkin"
        ordering = ["-checkin_at"]
        indexes = [models.Index(fields=["waybill", "node"])]
        verbose_name = "司机打卡"
        verbose_name_plural = "司机打卡"

    def __str__(self) -> str:
        return f"{self.waybill_id}:{self.node}"
