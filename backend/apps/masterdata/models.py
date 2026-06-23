"""主数据：客户 / 承运商 / 车辆 / 司机。"""

from django.db import models

from apps.core.models import BaseModel, SoftDeleteModel


class Customer(BaseModel, SoftDeleteModel):
    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=120)
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)
    wechat_group = models.CharField(max_length=120, blank=True, help_text="所属微信群聊（需求入口）")
    settlement_type = models.CharField(max_length=32, blank=True)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_customer"
        ordering = ["code"]
        verbose_name = "客户"
        verbose_name_plural = "客户"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"


class Carrier(BaseModel, SoftDeleteModel):
    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=120)
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)
    settlement_type = models.CharField(max_length=32, blank=True)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_carrier"
        ordering = ["code"]
        verbose_name = "承运商"
        verbose_name_plural = "承运商"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"


class Vehicle(BaseModel, SoftDeleteModel):
    CLASS_TRACTOR = "tractor"
    CLASS_TRAILER = "trailer"
    CLASS_RIGID = "rigid"
    VEHICLE_CLASS_CHOICES = [
        (CLASS_TRACTOR, "牵引车"),
        (CLASS_TRAILER, "挂车"),
        (CLASS_RIGID, "单体车"),
    ]

    DISPATCH_OWN = "own"
    DISPATCH_EXTERNAL = "external"
    DISPATCH_PLATFORM = "platform"
    DISPATCH_SOURCE_CHOICES = [
        (DISPATCH_OWN, "自有"),
        (DISPATCH_EXTERNAL, "外调"),
        (DISPATCH_PLATFORM, "平台"),
    ]

    plate_no = models.CharField(max_length=32, unique=True)
    vehicle_class = models.CharField(
        max_length=16, choices=VEHICLE_CLASS_CHOICES, default=CLASS_RIGID, db_index=True,
        help_text="牵引车/挂车/单体车",
    )
    dispatch_source = models.CharField(
        max_length=16, choices=DISPATCH_SOURCE_CHOICES, default=DISPATCH_OWN, db_index=True,
        help_text="调车来源：自有/外调/平台",
    )
    vehicle_type = models.CharField(max_length=64, blank=True)
    ownership_type = models.CharField(max_length=32, blank=True)
    load_capacity_ton = models.DecimalField(max_digits=10, decimal_places=2, default=0, help_text="核载吨位")
    volume_capacity_cbm = models.DecimalField(max_digits=10, decimal_places=2, default=0, help_text="容积(方)")
    carrier = models.ForeignKey(
        Carrier, null=True, blank=True, on_delete=models.SET_NULL, related_name="vehicles"
    )
    # 证件 / 维保
    road_transport_cert_no = models.CharField(max_length=64, blank=True, help_text="道路运输证号")
    inspection_expiry = models.DateField(null=True, blank=True, help_text="年检到期日")
    insurance_expiry = models.DateField(null=True, blank=True, help_text="保险到期日")
    maintenance_due_date = models.DateField(null=True, blank=True, help_text="下次维保日期")
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_vehicle"
        ordering = ["plate_no"]
        verbose_name = "车辆"
        verbose_name_plural = "车辆"

    def __str__(self) -> str:
        return self.plate_no


class Driver(BaseModel, SoftDeleteModel):
    EMP_EMPLOYEE = "employee"
    EMP_OUTSOURCED = "outsourced"
    EMP_CARRIER = "carrier_driver"
    EMP_TEMP = "temp"
    EMPLOYMENT_CHOICES = [
        (EMP_EMPLOYEE, "自有员工"),
        (EMP_OUTSOURCED, "外协外调"),
        (EMP_CARRIER, "承运商司机"),
        (EMP_TEMP, "临时"),
    ]

    name = models.CharField(max_length=64)
    employment_type = models.CharField(
        max_length=16, choices=EMPLOYMENT_CHOICES, default=EMP_EMPLOYEE, db_index=True,
        help_text="雇佣关系：员工/外调/承运商司机/临时，决定结算路径",
    )
    phone = models.CharField(max_length=32, blank=True, db_index=True)
    wechat = models.CharField(max_length=64, blank=True, help_text="微信号")
    app_registered = models.BooleanField(default=False, db_index=True, help_text="司机App注册状态")
    app_registered_at = models.DateTimeField(null=True, blank=True)
    # 累计统计（运单签收/结算时刷新）
    cumulative_waybills = models.IntegerField(default=0, help_text="累计运单数")
    cumulative_freight = models.DecimalField(max_digits=14, decimal_places=2, default=0, help_text="累计运费")
    id_no = models.CharField(max_length=32, blank=True)
    license_no = models.CharField(max_length=32, blank=True)
    license_type = models.CharField(max_length=16, blank=True, help_text="准驾车型，如 A2")
    license_expiry = models.DateField(null=True, blank=True, help_text="驾照到期日")
    qualification_cert_no = models.CharField(max_length=64, blank=True, help_text="从业资格证号")
    qualification_expiry = models.DateField(null=True, blank=True, help_text="从业资格证到期日")
    carrier = models.ForeignKey(
        Carrier, null=True, blank=True, on_delete=models.SET_NULL, related_name="drivers"
    )
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_driver"
        ordering = ["name"]
        verbose_name = "司机"
        verbose_name_plural = "司机"

    def __str__(self) -> str:
        return self.name


class Route(BaseModel, SoftDeleteModel):
    """线路：规划路径与允许偏航走廊，用于偏航判定与 ETA。"""

    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=160)
    origin = models.CharField(max_length=80, blank=True)
    destination = models.CharField(max_length=80, blank=True)
    waypoints = models.JSONField(default=list, blank=True, help_text="规划路径点 [[lng,lat], ...]")
    corridor_m = models.DecimalField(max_digits=10, decimal_places=2, default=2000, help_text="允许偏航走廊(米)")
    distance_km = models.DecimalField(max_digits=10, decimal_places=2, default=0)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_route"
        ordering = ["code"]
        verbose_name = "线路"
        verbose_name_plural = "线路"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"
