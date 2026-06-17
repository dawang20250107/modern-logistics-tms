from rest_framework.routers import DefaultRouter

from .views import CarrierViewSet, CustomerViewSet, DriverViewSet, VehicleViewSet

router = DefaultRouter(trailing_slash=False)
router.register("customers", CustomerViewSet, basename="customer")
router.register("carriers", CarrierViewSet, basename="carrier")
router.register("vehicles", VehicleViewSet, basename="vehicle")
router.register("drivers", DriverViewSet, basename="driver")

urlpatterns = router.urls
