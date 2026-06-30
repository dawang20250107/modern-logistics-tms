"""组织中台路由：/api/v1/org/*"""

from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    AccountHandoverViewSet,
    DepartmentViewSet,
    EmployeeGroupViewSet,
    EmployeeViewSet,
    OrganizationViewSet,
    OrgOverviewView,
    ServiceAreaViewSet,
)

router = DefaultRouter(trailing_slash=False)
router.register("organizations", OrganizationViewSet, basename="organization")
router.register("departments", DepartmentViewSet, basename="department")
router.register("employee-groups", EmployeeGroupViewSet, basename="employee-group")
router.register("employees", EmployeeViewSet, basename="employee")
router.register("service-areas", ServiceAreaViewSet, basename="service-area")
router.register("handovers", AccountHandoverViewSet, basename="account-handover")

urlpatterns = [
    path("overview", OrgOverviewView.as_view(), name="org-overview"),
    *router.urls,
]
