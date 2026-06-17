"""车联网（GPS/IoT）实时监控与统一报警中心数据模型。

- Device：车载终端（GPS/北斗/温度/油耗/ETC/ADAS/DSM）。
- VehicleState：车辆实时状态（最新位置、在线/离线、温度/油量等），用于实时定位视图。
- Alert：统一报警（超速/疲劳/偏航/异常停车/围栏/温度/油量/离线）。
"""

from django.conf import settings
from django.db import models

from apps.core.models import BaseModel


class Device(BaseModel):
    TYPE_GPS = "gps"
    TYPE_BEIDOU = "beidou"
    TYPE_TEMPERATURE = "temperature"
    TYPE_FUEL = "fuel"
    TYPE_ETC = "etc"
    TYPE_ADAS = "adas"
    TYPE_DSM = "dsm"
    TYPE_CHOICES = [
        (TYPE_GPS, "GPS 定位"),
        (TYPE_BEIDOU, "北斗定位"),
        (TYPE_TEMPERATURE, "温度传感器"),
        (TYPE_FUEL, "油耗传感器"),
        (TYPE_ETC, "ETC"),
        (TYPE_ADAS, "ADAS 前向安全"),
        (TYPE_DSM, "DSM 驾驶行为"),
    ]

    STATUS_ONLINE = "online"
    STATUS_OFFLINE = "offline"
    STATUS_CHOICES = [(STATUS_ONLINE, "在线"), (STATUS_OFFLINE, "离线")]

    device_no = models.CharField(max_length=64, unique=True)
    device_type = models.CharField(max_length=16, choices=TYPE_CHOICES, default=TYPE_GPS)
    vehicle = models.ForeignKey(
        "masterdata.Vehicle", null=True, blank=True, on_delete=models.SET_NULL, related_name="devices"
    )
    sim_no = models.CharField(max_length=32, blank=True)
    status = models.CharField(max_length=16, choices=STATUS_CHOICES, default=STATUS_OFFLINE)
    last_seen_at = models.DateTimeField(null=True, blank=True)
    meta = models.JSONField(default=dict, blank=True)

    class Meta:
        db_table = "tel_device"
        ordering = ["device_no"]
        indexes = [models.Index(fields=["device_type", "status"])]
        verbose_name = "车载终端"
        verbose_name_plural = "车载终端"

    def __str__(self) -> str:
        return f"{self.device_no}({self.device_type})"


class VehicleState(BaseModel):
    """车辆实时状态：每车一条，随上报更新（实时定位视图的数据源）。"""

    vehicle = models.OneToOneField(
        "masterdata.Vehicle", on_delete=models.CASCADE, related_name="live_state"
    )
    waybill = models.ForeignKey(
        "ops.Waybill", null=True, blank=True, on_delete=models.SET_NULL, related_name="vehicle_states"
    )
    lng = models.DecimalField(max_digits=10, decimal_places=6, default=0)
    lat = models.DecimalField(max_digits=10, decimal_places=6, default=0)
    speed_kmh = models.DecimalField(max_digits=8, decimal_places=2, default=0)
    heading = models.IntegerField(default=0, help_text="方向角 0-359")
    mileage_km = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    temperature_c = models.DecimalField(max_digits=6, decimal_places=2, null=True, blank=True)
    fuel_pct = models.DecimalField(max_digits=5, decimal_places=2, null=True, blank=True)
    online = models.BooleanField(default=False, db_index=True)
    reported_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "tel_vehicle_state"
        ordering = ["-reported_at"]
        verbose_name = "车辆实时状态"
        verbose_name_plural = "车辆实时状态"

    def __str__(self) -> str:
        return f"{self.vehicle_id}@({self.lat},{self.lng})"


class Geofence(BaseModel):
    SHAPE_CIRCLE = "circle"
    SHAPE_POLYGON = "polygon"
    SHAPE_CHOICES = [(SHAPE_CIRCLE, "圆形"), (SHAPE_POLYGON, "多边形")]

    PURPOSE_WAREHOUSE = "warehouse"
    PURPOSE_ROUTE = "route"
    PURPOSE_RESTRICTED = "restricted"
    PURPOSE_CHOICES = [
        (PURPOSE_WAREHOUSE, "仓库/卸货点"),
        (PURPOSE_ROUTE, "线路区域"),
        (PURPOSE_RESTRICTED, "限行区域"),
    ]

    name = models.CharField(max_length=120)
    shape = models.CharField(max_length=16, choices=SHAPE_CHOICES, default=SHAPE_CIRCLE)
    purpose = models.CharField(max_length=16, choices=PURPOSE_CHOICES, default=PURPOSE_WAREHOUSE)
    center_lng = models.DecimalField(max_digits=10, decimal_places=6, null=True, blank=True)
    center_lat = models.DecimalField(max_digits=10, decimal_places=6, null=True, blank=True)
    radius_m = models.DecimalField(max_digits=10, decimal_places=2, default=0)
    polygon = models.JSONField(default=list, blank=True, help_text="多边形顶点 [[lng,lat], ...]")
    is_active = models.BooleanField(default=True, db_index=True)

    class Meta:
        db_table = "tel_geofence"
        ordering = ["name"]
        verbose_name = "电子围栏"
        verbose_name_plural = "电子围栏"

    def __str__(self) -> str:
        return self.name


class GeofenceState(BaseModel):
    """车辆相对围栏的进出状态，用于检测进出跳变。"""

    vehicle = models.ForeignKey(
        "masterdata.Vehicle", on_delete=models.CASCADE, related_name="geofence_states"
    )
    geofence = models.ForeignKey(Geofence, on_delete=models.CASCADE, related_name="vehicle_states")
    inside = models.BooleanField(default=False)
    since = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "tel_geofence_state"
        unique_together = [("vehicle", "geofence")]
        verbose_name = "围栏进出状态"
        verbose_name_plural = "围栏进出状态"


class Alert(BaseModel):
    TYPE_OVERSPEED = "overspeed"
    TYPE_FATIGUE = "fatigue"
    TYPE_DEVIATION = "deviation"
    TYPE_ABNORMAL_STOP = "abnormal_stop"
    TYPE_GEOFENCE = "geofence"
    TYPE_TEMPERATURE = "temperature"
    TYPE_FUEL = "fuel"
    TYPE_OFFLINE = "offline"
    TYPE_CHOICES = [
        (TYPE_OVERSPEED, "超速"),
        (TYPE_FATIGUE, "疲劳驾驶"),
        (TYPE_DEVIATION, "偏航"),
        (TYPE_ABNORMAL_STOP, "异常停车"),
        (TYPE_GEOFENCE, "围栏进出"),
        (TYPE_TEMPERATURE, "温度异常"),
        (TYPE_FUEL, "油量异常"),
        (TYPE_OFFLINE, "设备离线"),
    ]

    LEVEL_INFO = "info"
    LEVEL_MEDIUM = "medium"
    LEVEL_HIGH = "high"
    LEVEL_CHOICES = [(LEVEL_INFO, "提示"), (LEVEL_MEDIUM, "中"), (LEVEL_HIGH, "高")]

    STATUS_OPEN = "open"
    STATUS_ACK = "acknowledged"
    STATUS_CLOSED = "closed"
    STATUS_CHOICES = [(STATUS_OPEN, "待处理"), (STATUS_ACK, "已确认"), (STATUS_CLOSED, "已关闭")]

    alert_type = models.CharField(max_length=24, choices=TYPE_CHOICES)
    level = models.CharField(max_length=16, choices=LEVEL_CHOICES, default=LEVEL_MEDIUM)
    status = models.CharField(max_length=16, choices=STATUS_CHOICES, default=STATUS_OPEN)
    vehicle = models.ForeignKey(
        "masterdata.Vehicle", null=True, blank=True, on_delete=models.SET_NULL, related_name="alerts"
    )
    device = models.ForeignKey(
        Device, null=True, blank=True, on_delete=models.SET_NULL, related_name="alerts"
    )
    waybill = models.ForeignKey(
        "ops.Waybill", null=True, blank=True, on_delete=models.SET_NULL, related_name="alerts"
    )
    message = models.CharField(max_length=255, blank=True)
    value = models.DecimalField(max_digits=12, decimal_places=2, null=True, blank=True)
    threshold = models.DecimalField(max_digits=12, decimal_places=2, null=True, blank=True)
    detail = models.JSONField(default=dict, blank=True)
    triggered_at = models.DateTimeField()
    handled_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, null=True, blank=True, on_delete=models.SET_NULL, related_name="handled_alerts"
    )
    handled_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = "tel_alert"
        ordering = ["-triggered_at"]
        indexes = [
            models.Index(fields=["alert_type", "status"]),
            models.Index(fields=["vehicle", "status"]),
        ]
        verbose_name = "报警"
        verbose_name_plural = "报警"

    def __str__(self) -> str:
        return f"{self.alert_type}:{self.message}"
