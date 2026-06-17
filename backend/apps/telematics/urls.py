from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    AlertViewSet,
    DeviceViewSet,
    GeofenceViewSet,
    LiveVehicleView,
    TelemetryIngestView,
    WaybillTrajectoryView,
)

router = DefaultRouter(trailing_slash=False)
router.register("devices", DeviceViewSet, basename="device")
router.register("alerts", AlertViewSet, basename="alert")
router.register("geofences", GeofenceViewSet, basename="geofence")

urlpatterns = [
    path("vehicles/live", LiveVehicleView.as_view(), name="telematics-live"),
    path("waybills/<str:waybill_no>/trajectory", WaybillTrajectoryView.as_view(), name="telematics-trajectory"),
    path("ingest", TelemetryIngestView.as_view(), name="telematics-ingest"),
    *router.urls,
]
