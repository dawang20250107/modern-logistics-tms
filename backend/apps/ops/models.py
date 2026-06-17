"""运输执行：订单 / 运单 / 事件 / 轨迹 / 异常 / 回单。

运单是核心主链路。轨迹点为高频写入实体，经 Redis 队列削峰、批量异步落库；
此处建模与索引已按高并发读写设计。
"""

from django.conf import settings
from django.db import models

from apps.core.models import BaseModel, OrgScopedModel


class Order(BaseModel):
    order_no = models.CharField(max_length=40, unique=True)
    customer = models.ForeignKey(
        "masterdata.Customer", null=True, blank=True, on_delete=models.SET_NULL, related_name="orders"
    )
    source = models.CharField(max_length=32, blank=True)
    status = models.CharField(max_length=32, default="open")
    remark = models.CharField(max_length=255, blank=True)

    class Meta:
        db_table = "ops_order"
        ordering = ["-created_at"]
        verbose_name = "订单"
        verbose_name_plural = "订单"

    def __str__(self) -> str:
        return self.order_no


class Waybill(BaseModel, OrgScopedModel):
    # 运单状态机
    STATUS_DRAFT = "draft"
    STATUS_PENDING_DISPATCH = "pending_dispatch"
    STATUS_DISPATCHED = "dispatched"
    STATUS_LOADED = "loaded"
    STATUS_DEPARTED = "departed"
    STATUS_IN_TRANSIT = "in_transit"
    STATUS_ARRIVED = "arrived"
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

    waybill_no = models.CharField(max_length=40, unique=True)
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

    route_name = models.CharField(max_length=160)
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
    planned_arrival = models.DateTimeField(null=True, blank=True)
    estimated_arrival = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "ops_waybill"
        ordering = ["-created_at"]
        indexes = [
            models.Index(fields=["status", "risk_level"]),
            models.Index(fields=["receipt_status"]),
            models.Index(fields=["-eta_drift_minutes"]),
            models.Index(fields=["customer", "status"]),
        ]
        verbose_name = "运单"
        verbose_name_plural = "运单"

    def __str__(self) -> str:
        return self.waybill_no


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

    waybill = models.ForeignKey(
        Waybill, null=True, blank=True, on_delete=models.SET_NULL, related_name="exceptions"
    )
    exception_type = models.CharField(max_length=64)
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
