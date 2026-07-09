import json

from django.conf import settings
from django.utils import timezone
from rest_framework import mixins, viewsets
from rest_framework.decorators import action
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.core.exceptions import AppError
from apps.core.redis import get_redis
from apps.iam.permissions import HasPermission

from .geo import analyze_trajectory
from .models import Alert, Device, Geofence, VehicleState
from .serializers import AlertSerializer, DeviceSerializer, GeofenceSerializer, VehicleStateSerializer
from .tasks import TELEMETRY_QUEUE, flush_telemetry

# 车联网数据（GPS 轨迹/实时定位/报警/设备）敏感：读需查看权，写需管理权
PERM_VIEW = "telematics.view"
PERM_MANAGE = "telematics.manage"


class _TelematicsReadView(APIView):
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = PERM_VIEW


class DeviceViewSet(viewsets.ModelViewSet):
    queryset = Device.objects.select_related("vehicle").all()
    serializer_class = DeviceSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_VIEW, "write": PERM_MANAGE}
    filterset_fields = ["device_type", "status", "vehicle"]
    search_fields = ["device_no", "sim_no"]


class GeofenceViewSet(viewsets.ModelViewSet):
    queryset = Geofence.objects.all()
    serializer_class = GeofenceSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_VIEW, "write": PERM_MANAGE}
    filterset_fields = ["shape", "purpose", "is_active"]
    search_fields = ["name"]


class WaybillTrajectoryView(_TelematicsReadView):
    """轨迹回放：返回运单历史轨迹点 + 停留点 + 超速段。"""

    def get(self, request, waybill_no):
        from apps.ops.models import TrackingPoint, Waybill

        if not Waybill.objects.filter(waybill_no=waybill_no).exists():
            raise AppError("WAYBILL_NOT_FOUND", "运单不存在。", status=404)
        qs = TrackingPoint.objects.filter(waybill__waybill_no=waybill_no).order_by("reported_at")
        start, end = request.query_params.get("from"), request.query_params.get("to")
        if start:
            qs = qs.filter(reported_at__gte=start)
        if end:
            qs = qs.filter(reported_at__lte=end)
        points = [
            {"lng": float(p.lng), "lat": float(p.lat), "speed_kmh": float(p.speed_kmh), "reported_at": p.reported_at}
            for p in qs
        ]
        speed_limit = float(request.query_params.get("speed_limit") or settings.ALERT_SPEED_LIMIT_KMH)
        analysis = analyze_trajectory(points, speed_limit=speed_limit)
        return Response({
            "waybill_no": waybill_no,
            "points": [{**p, "reported_at": p["reported_at"].isoformat()} for p in points],
            "stops": [{**s, "from": s["from"].isoformat(), "to": s["to"].isoformat()} for s in analysis["stops"]],
            "overspeed_segments": [
                {**s, "from": s["from"].isoformat(), "to": s["to"].isoformat()} for s in analysis["overspeed_segments"]
            ],
            "total_points": analysis["total_points"],
        })


class CommandCenterSummaryView(_TelematicsReadView):
    """调度指挥中心摘要：在线运力 / 待调度 / 在途 / 报警 一屏 KPI。"""

    def get(self, request):
        from apps.ops.models import Waybill

        return Response({
            "online_vehicles": VehicleState.objects.filter(online=True).count(),
            "offline_vehicles": VehicleState.objects.filter(online=False).count(),
            "pending_dispatch": Waybill.objects.filter(status=Waybill.STATUS_PENDING_DISPATCH).count(),
            "in_transit": Waybill.objects.filter(status=Waybill.STATUS_IN_TRANSIT).count(),
            "open_alerts": Alert.objects.filter(status=Alert.STATUS_OPEN).count(),
            "high_alerts": Alert.objects.filter(status=Alert.STATUS_OPEN, level=Alert.LEVEL_HIGH).count(),
        })


class LiveVehicleView(_TelematicsReadView):
    """实时车辆位置列表（实时定位视图数据源）。?online=true 仅看在线。"""

    def get(self, request):
        qs = VehicleState.objects.select_related("vehicle", "waybill").all()
        online = request.query_params.get("online")
        if online is not None:
            qs = qs.filter(online=(online.lower() in ("1", "true", "yes")))
        return Response({"vehicles": VehicleStateSerializer(qs, many=True).data})


class AlertViewSet(mixins.ListModelMixin, mixins.RetrieveModelMixin, viewsets.GenericViewSet):
    queryset = Alert.objects.select_related("vehicle", "device", "waybill").all()
    serializer_class = AlertSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_VIEW, "acknowledge": PERM_MANAGE, "close": PERM_MANAGE}
    filterset_fields = ["alert_type", "level", "status", "vehicle", "waybill"]
    search_fields = ["message"]

    @action(detail=True, methods=["post"], url_path="ack")
    def acknowledge(self, request, pk=None):
        return self._transition(request, Alert.STATUS_ACK)

    @action(detail=True, methods=["post"], url_path="close")
    def close(self, request, pk=None):
        return self._transition(request, Alert.STATUS_CLOSED)

    def _transition(self, request, status_value):
        alert = self.get_object()
        alert.status = status_value
        alert.handled_by = request.user if request.user.is_authenticated else None
        alert.handled_at = timezone.now()
        alert.save(update_fields=["status", "handled_by", "handled_at", "updated_at"])
        return Response(AlertSerializer(alert).data)


class TelemetryIngestView(APIView):
    """设备上报批量入口（高并发写热点）。

    削峰：仅把上报压入 Redis 队列并触发异步落库，由 telematics.flush_telemetry
    批量落库 + 规则引擎触发报警（beat 周期兜底）。
    """

    def post(self, request):
        reports = request.data.get("reports", []) or []
        if not isinstance(reports, list):
            raise AppError("INVALID_REPORTS", "reports 必须是数组。", status=400)
        redis = get_redis()
        pipe = redis.pipeline()
        queued = 0
        for report in reports:
            if not (report.get("device_no") or report.get("vehicle_plate")):
                continue
            pipe.rpush(TELEMETRY_QUEUE, json.dumps(report, default=str))
            queued += 1
        pipe.execute()
        if queued:
            flush_telemetry.delay()
        return Response({"queued": queued, "status": "queued_for_async_persist"}, status=202)
