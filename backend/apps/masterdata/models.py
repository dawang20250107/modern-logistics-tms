"""主数据：客户 / 承运商 / 车辆 / 司机。"""

from django.conf import settings
from django.db import models

from apps.core.models import BaseModel, SoftDeleteModel


class Customer(BaseModel, SoftDeleteModel):
    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=120)
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)
    wechat_group = models.CharField(max_length=120, blank=True, help_text="所属微信群聊（需求入口）")
    settlement_type = models.CharField(max_length=32, blank=True)
    # 月结账期与信用管理
    credit_limit = models.DecimalField(
        max_digits=14, decimal_places=2, default=0, help_text="授信额度（0 表示不限）"
    )
    credit_days = models.IntegerField(default=30, help_text="账期天数（账单日后 N 天到期）")
    billing_day = models.IntegerField(default=1, help_text="账单日（每月几号出账，1-28）")
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_customer"
        ordering = ["code"]
        verbose_name = "客户"
        verbose_name_plural = "客户"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"


class Carrier(BaseModel, SoftDeleteModel):
    GRADE_CHOICES = [
        ("A", "A · 优质"),
        ("B", "B · 良好"),
        ("C", "C · 关注"),
        ("D", "D · 高风险"),
    ]

    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=120)
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)
    settlement_type = models.CharField(max_length=32, blank=True)
    is_active = models.BooleanField(default=True)

    # ── 风控：分级 / 黑名单 ──
    grade = models.CharField(
        max_length=1, choices=GRADE_CHOICES, default="B", help_text="承运商综合评级"
    )
    blacklisted = models.BooleanField(default=False, help_text="拉黑后不可被派单")
    blacklist_reason = models.CharField(max_length=255, blank=True)

    # ── 承运资质与到期（营业执照 / 道路运输经营许可）──
    business_license_no = models.CharField(max_length=64, blank=True, help_text="营业执照号")
    qualification_expiry = models.DateField(
        null=True, blank=True, help_text="承运资质/经营许可到期日；过期不可派单"
    )

    # ── 账期与信用（应付侧，镜像客户；供对账与结算参考）──
    credit_limit = models.DecimalField(
        max_digits=14, decimal_places=2, default=0, help_text="对我方授信额度（0 表示不限）"
    )
    credit_days = models.IntegerField(default=30, help_text="账期天数（账单日后 N 天到期）")
    billing_day = models.IntegerField(default=1, help_text="账单日（每月几号出账，1-28）")

    class Meta:
        db_table = "md_carrier"
        ordering = ["code"]
        verbose_name = "承运商"
        verbose_name_plural = "承运商"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"

    def dispatch_block_reason(self, today=None, *, block_on_expired: bool = True) -> str:
        """返回不可派单的原因；可派单时返回空串。集中承运商风控硬阻断规则。

        - 已拉黑 / 已停用：始终拦截。
        - 承运资质过期：受 block_on_expired 开关控制（与车辆证件硬阻断一致）。
        """
        if self.blacklisted:
            return f"承运商 {self.name} 已列入黑名单" + (f"（{self.blacklist_reason}）" if self.blacklist_reason else "")
        if not self.is_active:
            return f"承运商 {self.name} 已停用"
        if block_on_expired and self.qualification_expiry:
            from django.utils import timezone

            today = today or timezone.localdate()
            if self.qualification_expiry < today:
                return f"承运商 {self.name} 承运资质已于 {self.qualification_expiry:%Y-%m-%d} 到期"
        return ""


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

    # 车厢结构（决定能装什么货：冷链货必须冷藏车、危险品必须危运/罐式车）
    BODY_STAKE = "stake"        # 高栏
    BODY_FLATBED = "flatbed"    # 平板
    BODY_VAN = "van"            # 厢式
    BODY_REEFER = "reefer"      # 冷藏
    BODY_HAZMAT = "hazmat"      # 危运
    BODY_FENCE = "fence"        # 仓栅
    BODY_WING = "wing"          # 飞翼
    BODY_TANK = "tank"          # 罐式
    BODY_CHOICES = [
        (BODY_STAKE, "高栏"),
        (BODY_FLATBED, "平板"),
        (BODY_VAN, "厢式"),
        (BODY_REEFER, "冷藏"),
        (BODY_HAZMAT, "危运"),
        (BODY_FENCE, "仓栅"),
        (BODY_WING, "飞翼"),
        (BODY_TANK, "罐式"),
    ]

    plate_no = models.CharField(max_length=32, unique=True)
    vehicle_class = models.CharField(
        max_length=16, choices=VEHICLE_CLASS_CHOICES, default=CLASS_RIGID, db_index=True,
        help_text="牵引车/挂车/单体车",
    )
    body_type = models.CharField(
        max_length=16, choices=BODY_CHOICES, blank=True, help_text="车厢结构：高栏/平板/厢式/冷藏/危运…",
    )
    vehicle_length_m = models.DecimalField(
        max_digits=4, decimal_places=1, default=0, help_text="车长(米)，如 4.2/6.8/9.6/13/17.5",
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


class DriverCredential(BaseModel):
    """司机证件库：行驶证(车头/车挂主副页)、驾驶证(主副页)、运输证、身份证(正反面)。

    支持司机自传或代上传；OCR 自动识别带出姓名/证号/有效期建档。
    """

    CRED_VEHICLE_LICENSE = "vehicle_license"   # 车头行驶证
    CRED_TRAILER_LICENSE = "trailer_license"   # 车挂行驶证
    CRED_DRIVING_LICENSE = "driving_license"   # 驾驶证
    CRED_TRANSPORT_CERT = "transport_cert"     # 道路运输证
    CRED_ID_CARD = "id_card"                   # 身份证
    CRED_TYPE_CHOICES = [
        (CRED_VEHICLE_LICENSE, "车头行驶证"),
        (CRED_TRAILER_LICENSE, "车挂行驶证"),
        (CRED_DRIVING_LICENSE, "驾驶证"),
        (CRED_TRANSPORT_CERT, "道路运输证"),
        (CRED_ID_CARD, "身份证"),
    ]
    SIDE_MAIN = "main"   # 主页 / 正面
    SIDE_BACK = "back"   # 副页 / 反面
    SIDE_CHOICES = [(SIDE_MAIN, "主页/正面"), (SIDE_BACK, "副页/反面")]

    driver = models.ForeignKey(Driver, on_delete=models.CASCADE, related_name="credentials")
    cred_type = models.CharField(max_length=24, choices=CRED_TYPE_CHOICES, db_index=True)
    side = models.CharField(max_length=8, choices=SIDE_CHOICES, default=SIDE_MAIN)
    file = models.FileField(upload_to="credentials/", null=True, blank=True)
    file_url = models.URLField(blank=True)
    ocr_status = models.CharField(max_length=16, default="pending", help_text="pending/processing/done/failed")
    ocr_result = models.JSONField(default=dict, blank=True)
    holder_name = models.CharField(max_length=64, blank=True, help_text="识别出的姓名/车牌")
    cert_no = models.CharField(max_length=64, blank=True, help_text="识别出的证号")
    expiry_date = models.DateField(null=True, blank=True, help_text="有效期至")
    uploaded_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="uploaded_credentials"
    )
    self_uploaded = models.BooleanField(default=False, help_text="司机自传(True)或代上传(False)")

    class Meta:
        db_table = "md_driver_credential"
        ordering = ["driver", "cred_type", "side"]
        indexes = [models.Index(fields=["driver", "cred_type"]), models.Index(fields=["expiry_date"])]
        verbose_name = "司机证件"
        verbose_name_plural = "司机证件"

    def __str__(self) -> str:
        return f"{self.driver_id}:{self.cred_type}:{self.side}"


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


class B2BPartner(BaseModel, SoftDeleteModel):
    """B2B 上下游业务伙伴/发货方/收货方/供应商。"""

    PARTNER_SHIPPER = "shipper"      # 发货方
    PARTNER_CONSIGNEE = "consignee"  # 收货方
    PARTNER_SUPPLIER = "supplier"    # 供应商/承运商
    PARTNER_TYPE_CHOICES = [
        (PARTNER_SHIPPER, "发货方"),
        (PARTNER_CONSIGNEE, "收货方"),
        (PARTNER_SUPPLIER, "供应商/承运商"),
    ]

    partner_type = models.CharField(max_length=16, choices=PARTNER_TYPE_CHOICES, db_index=True)
    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=120)
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)
    address = models.CharField(max_length=255, blank=True, help_text="详细物理地址")
    city = models.CharField(max_length=64, blank=True, help_text="归属城市（标准化城市名，如无锡）")
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_b2b_partner"
        ordering = ["code"]
        verbose_name = "上下游伙伴"
        verbose_name_plural = "上下游伙伴"

    def __str__(self) -> str:
        return f"[{self.get_partner_type_display()}] {self.code} {self.name}"
