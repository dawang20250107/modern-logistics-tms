from datetime import timedelta

from django.utils import timezone
from rest_framework import viewsets
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
    filterset_fields = ["is_active", "carrier"]
    ordering_fields = ["plate_no", "created_at"]


class DriverViewSet(viewsets.ModelViewSet):
    queryset = Driver.objects.select_related("carrier").all()
    serializer_class = DriverSerializer
    search_fields = ["name", "phone"]
    filterset_fields = ["is_active", "carrier"]
    ordering_fields = ["name", "created_at"]


class RouteViewSet(viewsets.ModelViewSet):
    queryset = Route.objects.all()
    serializer_class = RouteSerializer
    search_fields = ["code", "name", "origin", "destination"]
    filterset_fields = ["is_active"]
    ordering_fields = ["code", "created_at"]


class ExpiringCredentialsView(APIView):
    """证件到期预警：返回 N 天内到期（或已过期）的车辆/司机证件。?days=30"""

    def get(self, request):
        days = int(request.query_params.get("days") or 30)
        deadline = timezone.localdate() + timedelta(days=days)

        vehicles = []
        vq = Vehicle.objects.filter(is_active=True)
        for v in vq:
            for field, label in [
                ("inspection_expiry", "年检"),
                ("insurance_expiry", "保险"),
                ("maintenance_due_date", "维保"),
            ]:
                expiry = getattr(v, field)
                if expiry and expiry <= deadline:
                    vehicles.append({"plate_no": v.plate_no, "credential": label, "expiry": expiry.isoformat()})

        drivers = []
        for d in Driver.objects.filter(is_active=True):
            for field, label in [("license_expiry", "驾照"), ("qualification_expiry", "从业资格")]:
                expiry = getattr(d, field)
                if expiry and expiry <= deadline:
                    drivers.append({"name": d.name, "credential": label, "expiry": expiry.isoformat()})

        return Response({"days": days, "vehicles": vehicles, "drivers": drivers})
