from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    ExceptionViewSet,
    OrderViewSet,
    PublicTrackingView,
    ReceiptViewSet,
    TrackingIngestView,
    WaybillViewSet,
    WorkbenchView,
)

router = DefaultRouter(trailing_slash=False)
router.register("waybills", WaybillViewSet, basename="waybill")
router.register("orders", OrderViewSet, basename="order")
router.register("exceptions", ExceptionViewSet, basename="exception")
router.register("receipts", ReceiptViewSet, basename="receipt")

urlpatterns = [
    *router.urls,
    path("tracking/points", TrackingIngestView.as_view(), name="tracking-ingest"),
    path("track", PublicTrackingView.as_view(), name="public-track"),
    path("workbench", WorkbenchView.as_view(), name="workbench"),
]
