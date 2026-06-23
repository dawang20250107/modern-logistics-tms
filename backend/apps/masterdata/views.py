from datetime import timedelta

from django.utils import timezone
from rest_framework import viewsets
from rest_framework.decorators import action
from rest_framework.response import Response
from rest_framework.views import APIView

from .models import Carrier, Customer, Driver, Route, Vehicle
from .serializers import (
    CarrierSerializer,
    CustomerSerializer,
    DriverSerializer,
    RouteSerializer,
    VehicleSerializer,
)


class CustomerViewSet(viewsets.ModelViewSet):
    queryset = Customer.objects.all()
    serializer_class = CustomerSerializer
    search_fields = ["code", "name", "contact_phone"]
    filterset_fields = ["is_active"]
    ordering_fields = ["code", "name", "created_at"]


class CarrierViewSet(viewsets.ModelViewSet):
    queryset = Carrier.objects.all()
    serializer_class = CarrierSerializer
    search_fields = ["code", "name", "contact_phone"]
    filterset_fields = ["is_active"]
    ordering_fields = ["code", "name", "created_at"]


class VehicleViewSet(viewsets.ModelViewSet):
    queryset = Vehicle.objects.select_related("carrier").all()
    serializer_class = VehicleSerializer
    search_fields = ["plate_no", "vehicle_type"]
    filterset_fields = ["is_active", "carrier", "vehicle_class", "dispatch_source"]
    ordering_fields = ["plate_no", "created_at"]


class DriverViewSet(viewsets.ModelViewSet):
    queryset = Driver.objects.select_related("carrier").all()
    serializer_class = DriverSerializer
    search_fields = ["name", "phone", "wechat"]
    filterset_fields = ["is_active", "carrier", "employment_type", "app_registered"]
    ordering_fields = ["name", "created_at", "cumulative_waybills", "cumulative_freight"]

    @action(detail=True, methods=["post"], url_path="refresh-stats")
    def refresh_stats(self, request, pk=None):
        """刷新该司机累计运单数与运费（司机库统计）。"""
        from apps.ops.stats import refresh_driver_stats

        driver = self.get_object()
        refresh_driver_stats(driver)
        driver.refresh_from_db()
        return Response(DriverSerializer(driver).data)


class RouteViewSet(viewsets.ModelViewSet):
    queryset = Route.objects.all()
    serializer_class = RouteSerializer
    search_fields = ["code", "name", "origin", "destination"]
    filterset_fields = ["is_active"]
    ordering_fields = ["code", "created_at"]


class ExpiringCredentialsView(APIView):
    """证件到期预警：返回 N 天内到期（或已过期）的车辆/司机证件。?days=30

    每条含 days_left（负数=已过期）与 severity（expired/critical/warning），
    并按紧迫度（已过期/天数升序）排序，便于车队合规台一眼锁定风险。
    """

    def get(self, request):
        days = int(request.query_params.get("days") or 30)
        today = timezone.localdate()
        deadline = today + timedelta(days=days)

        def severity(days_left: int) -> str:
            if days_left < 0:
                return "expired"
            if days_left <= 7:
                return "critical"
            return "warning"

        vehicles = []
        for v in Vehicle.objects.filter(is_active=True):
            for field, label in [
                ("inspection_expiry", "年检"),
                ("insurance_expiry", "保险"),
                ("maintenance_due_date", "维保"),
            ]:
                expiry = getattr(v, field)
                if expiry and expiry <= deadline:
                    days_left = (expiry - today).days
                    vehicles.append({
                        "subject": v.plate_no,
                        "plate_no": v.plate_no,
                        "credential": label,
                        "expiry": expiry.isoformat(),
                        "days_left": days_left,
                        "severity": severity(days_left),
                    })

        drivers = []
        for d in Driver.objects.filter(is_active=True):
            for field, label in [("license_expiry", "驾照"), ("qualification_expiry", "从业资格")]:
                expiry = getattr(d, field)
                if expiry and expiry <= deadline:
                    days_left = (expiry - today).days
                    drivers.append({
                        "subject": d.name,
                        "name": d.name,
                        "credential": label,
                        "expiry": expiry.isoformat(),
                        "days_left": days_left,
                        "severity": severity(days_left),
                    })

        vehicles.sort(key=lambda r: r["days_left"])
        drivers.sort(key=lambda r: r["days_left"])
        rows = vehicles + drivers
        summary = {
            "total": len(rows),
            "expired": sum(1 for r in rows if r["severity"] == "expired"),
            "critical": sum(1 for r in rows if r["severity"] == "critical"),
            "warning": sum(1 for r in rows if r["severity"] == "warning"),
        }
        return Response({"days": days, "summary": summary, "vehicles": vehicles, "drivers": drivers})
