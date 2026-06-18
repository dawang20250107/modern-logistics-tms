from rest_framework.routers import DefaultRouter

from .views import AuditLogViewSet

router = DefaultRouter(trailing_slash=False)
router.register("audit-logs", AuditLogViewSet, basename="audit-log")

urlpatterns = router.urls
