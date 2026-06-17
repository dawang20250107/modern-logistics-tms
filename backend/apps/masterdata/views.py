from rest_framework import viewsets

from .models import Carrier, Customer, Driver, Vehicle
from .serializers import CarrierSerializer, CustomerSerializer, DriverSerializer, VehicleSerializer


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
