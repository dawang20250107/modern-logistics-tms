from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    CarrierViewSet,
    CustomerViewSet,
    DriverCredentialViewSet,
    DriverViewSet,
    ExpiringCredentialsView,
    RouteViewSet,
    VehicleViewSet,
)

router = DefaultRouter(trailing_slash=False)
router.register("customers", CustomerViewSet, basename="customer")
router.register("carriers", CarrierViewSet, basename="carrier")
router.register("vehicles", VehicleViewSet, basename="vehicle")
router.register("drivers", DriverViewSet, basename="driver")
router.register("driver-credentials", DriverCredentialViewSet, basename="driver-credential")
router.register("routes", RouteViewSet, basename="route")

urlpatterns = [
    path("credentials/expiring", ExpiringCredentialsView.as_view(), name="credentials-expiring"),
    *router.urls,
]
