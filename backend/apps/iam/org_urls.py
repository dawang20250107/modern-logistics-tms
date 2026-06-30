"""组织中台路由：/api/v1/org/*"""

from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    AccountHandoverViewSet,
    CoverageResolveView,
    DepartmentViewSet,
    EmployeeGroupViewSet,
    EmployeeViewSet,
    OrganizationViewSet,
    OrgOverviewView,
    PermissionViewSet,
    RbacMatrixView,
    RoleViewSet,
    ServiceAreaViewSet,
)

router = DefaultRouter(trailing_slash=False)
router.register("organizations", OrganizationViewSet, basename="organization")
router.register("departments", DepartmentViewSet, basename="department")
router.register("employee-groups", EmployeeGroupViewSet, basename="employee-group")
router.register("employees", EmployeeViewSet, basename="employee")
router.register("service-areas", ServiceAreaViewSet, basename="service-area")
router.register("handovers", AccountHandoverViewSet, basename="account-handover")
router.register("roles", RoleViewSet, basename="role")
router.register("permissions", PermissionViewSet, basename="permission")

urlpatterns = [
    path("overview", OrgOverviewView.as_view(), name="org-overview"),
    path("route-resolve", CoverageResolveView.as_view(), name="coverage-resolve"),
    path("rbac/matrix", RbacMatrixView.as_view(), name="rbac-matrix"),
    *router.urls,
]
