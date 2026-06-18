from rest_framework.routers import DefaultRouter

from .views import NotificationViewSet

router = DefaultRouter(trailing_slash=False)
router.register("notifications", NotificationViewSet, basename="notification")

urlpatterns = router.urls
