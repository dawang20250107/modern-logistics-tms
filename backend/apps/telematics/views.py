import json

from django.utils import timezone
from rest_framework import mixins, viewsets
from rest_framework.decorators import action
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.core.exceptions import AppError
from apps.core.redis import get_redis

from .models import Alert, Device, VehicleState
from .serializers import AlertSerializer, DeviceSerializer, VehicleStateSerializer
from .tasks import TELEMETRY_QUEUE, flush_telemetry


class DeviceViewSet(viewsets.ModelViewSet):
    queryset = Device.objects.select_related("vehicle").all()
    serializer_class = DeviceSerializer
    filterset_fields = ["device_type", "status", "vehicle"]
    search_fields = ["device_no", "sim_no"]


class LiveVehicleView(APIView):
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
