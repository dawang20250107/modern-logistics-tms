from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    DriverReminderViewSet,
    ExceptionViewSet,
    IntegrationStatusView,
    OrderTemplateViewSet,
    OrderViewSet,
    PublicOrderIntakeView,
    PublicTrackingView,
    ReceiptViewSet,
    ReminderTemplateViewSet,
    TrackingIngestView,
    WaybillViewSet,
    WorkbenchView,
)

router = DefaultRouter(trailing_slash=False)
router.register("waybills", WaybillViewSet, basename="waybill")
router.register("orders", OrderViewSet, basename="order")
router.register("order-templates", OrderTemplateViewSet, basename="order-template")
router.register("exceptions", ExceptionViewSet, basename="exception")
router.register("reminder-templates", ReminderTemplateViewSet, basename="reminder-template")
router.register("reminders", DriverReminderViewSet, basename="driver-reminder")
router.register("receipts", ReceiptViewSet, basename="receipt")

urlpatterns = [
    *router.urls,
    path("tracking/points", TrackingIngestView.as_view(), name="tracking-ingest"),
    path("track", PublicTrackingView.as_view(), name="public-track"),
    path("public/orders", PublicOrderIntakeView.as_view(), name="public-order-intake"),
    path("workbench", WorkbenchView.as_view(), name="workbench"),
    path("integrations/status", IntegrationStatusView.as_view(), name="integration-status"),
]
