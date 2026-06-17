from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import AlertViewSet, DeviceViewSet, LiveVehicleView, TelemetryIngestView

router = DefaultRouter(trailing_slash=False)
router.register("devices", DeviceViewSet, basename="device")
router.register("alerts", AlertViewSet, basename="alert")

urlpatterns = [
    path("vehicles/live", LiveVehicleView.as_view(), name="telematics-live"),
    path("ingest", TelemetryIngestView.as_view(), name="telematics-ingest"),
    *router.urls,
]
