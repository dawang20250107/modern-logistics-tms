from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    DriverReminderViewSet,
    ExceptionViewSet,
    IntegrationStatusView,
    LookupView,
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

def _driver_portal_urls():
    from .driver_portal import (
        DriverAckReminderView,
        DriverCheckinView,
        DriverCredentialUploadView,
        DriverLoginView,
        DriverTasksView,
    )

    return [
        path("driver/login", DriverLoginView.as_view(), name="driver-login"),
        path("driver/tasks", DriverTasksView.as_view(), name="driver-tasks"),
        path("driver/reminders/<uuid:reminder_id>/ack", DriverAckReminderView.as_view(), name="driver-ack"),
        path("driver/checkin", DriverCheckinView.as_view(), name="driver-checkin"),
        path("driver/credentials", DriverCredentialUploadView.as_view(), name="driver-credential-upload"),
    ]


urlpatterns = [
    *router.urls,
    path("tracking/points", TrackingIngestView.as_view(), name="tracking-ingest"),
    path("track", PublicTrackingView.as_view(), name="public-track"),
    path("public/orders", PublicOrderIntakeView.as_view(), name="public-order-intake"),
    path("workbench", WorkbenchView.as_view(), name="workbench"),
    path("lookup", LookupView.as_view(), name="lookup"),
    path("integrations/status", IntegrationStatusView.as_view(), name="integration-status"),
    *_driver_portal_urls(),
]
