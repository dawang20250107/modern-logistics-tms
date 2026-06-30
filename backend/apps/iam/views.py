import secrets

from django.db.models import Count, Q
from rest_framework import status, viewsets
from rest_framework.decorators import action
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from .models import (
    AccountHandover,
    Department,
    Employee,
    EmployeeGroup,
    Organization,
    ServiceArea,
)
from .serializers import (
    AccountHandoverSerializer,
    DepartmentSerializer,
    EmployeeGroupSerializer,
    EmployeeSerializer,
    OrganizationSerializer,
    ServiceAreaSerializer,
)
from .services import build_org_tree, handover_account, resolve_coverage


class MeView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request):
        user = request.user
        roles = list(
            user.role_assignments.select_related("role").values_list("role__code", flat=True).distinct()
        )
        return Response(
            {
                "id": str(user.id),
                "username": user.username,
                "nickname": user.nickname,
                "phone": user.phone,
                "is_staff": user.is_staff,
                "is_superuser": user.is_superuser,
                "organization_id": str(user.organization_id) if user.organization_id else None,
                "roles": roles,
            }
        )


class OrganizationViewSet(viewsets.ModelViewSet):
    queryset = Organization.objects.select_related("parent").all()
    serializer_class = OrganizationSerializer
    search_fields = ["code", "name", "short_name", "manager_name"]
    filterset_fields = ["type", "org_property", "is_active", "parent"]
    ordering_fields = ["sort_order", "code", "created_at"]

    @action(detail=False, methods=["get"])
    def tree(self, request):
        """组织树（含各组织自身+子树在职人头），一棵树看清编制分布——G7 所无。"""
        orgs = list(Organization.objects.filter(is_active=True))
        counts = dict(
            Employee.objects.filter(status="active")
            .values_list("organization_id")
            .annotate(n=Count("id"))
        )
        tree = build_org_tree(orgs, headcount=counts)
        return Response({"tree": tree, "total": len(orgs)})


class DepartmentViewSet(viewsets.ModelViewSet):
    queryset = Department.objects.select_related("organization", "manager", "parent").all()
    serializer_class = DepartmentSerializer
    search_fields = ["code", "name"]
    filterset_fields = ["organization", "is_active", "parent"]
    ordering_fields = ["sort_order", "code"]


class EmployeeGroupViewSet(viewsets.ModelViewSet):
    queryset = EmployeeGroup.objects.prefetch_related("roles").all()
    serializer_class = EmployeeGroupSerializer
    search_fields = ["code", "name"]
    filterset_fields = ["is_active"]
    ordering_fields = ["code"]


class ServiceAreaViewSet(viewsets.ModelViewSet):
    queryset = ServiceArea.objects.select_related("organization").all()
    serializer_class = ServiceAreaSerializer
    search_fields = ["region_name", "region_code", "city", "province"]
    filterset_fields = ["organization", "area_type", "is_active", "province", "city"]
    ordering_fields = ["priority", "region_name"]


class EmployeeViewSet(viewsets.ModelViewSet):
    queryset = (
        Employee.objects.select_related("organization", "department", "supervisor", "user")
        .prefetch_related("groups")
        .all()
    )
    serializer_class = EmployeeSerializer
    search_fields = ["employee_no", "name", "phone", "position"]
    filterset_fields = ["organization", "department", "status", "supervisor"]
    ordering_fields = ["employee_no", "hire_date", "created_at"]

    def _toggle_account(self, employee, *, active: bool):
        if employee.user_id:
            employee.user.is_active = active
            employee.user.save(update_fields=["is_active"])

    @action(detail=True, methods=["post"])
    def disable(self, request, pk=None):
        """停用：账号禁登 + 状态置停用。"""
        emp = self.get_object()
        self._toggle_account(emp, active=False)
        emp.status = "disabled"
        emp.save(update_fields=["status", "updated_at"])
        return Response(self.get_serializer(emp).data)

    @action(detail=True, methods=["post"])
    def enable(self, request, pk=None):
        """启用：恢复账号登录 + 状态置在职。"""
        emp = self.get_object()
        self._toggle_account(emp, active=True)
        emp.status = "active"
        emp.save(update_fields=["status", "updated_at"])
        return Response(self.get_serializer(emp).data)

    @action(detail=True, methods=["post"], url_path="reset-password")
    def reset_password(self, request, pk=None):
        """重置密码：生成强随机口令并返回一次（仅对已绑定登录账号的员工有效）。"""
        emp = self.get_object()
        if not emp.user_id:
            return Response(
                {"detail": "该员工尚未绑定登录账号"}, status=status.HTTP_400_BAD_REQUEST
            )
        new_pwd = secrets.token_urlsafe(9)
        emp.user.set_password(new_pwd)
        emp.user.save(update_fields=["password"])
        return Response({"employee_no": emp.employee_no, "username": emp.user.username, "password": new_pwd})

    @action(detail=True, methods=["post"])
    def handover(self, request, pk=None):
        """账号移交：下属与所辖部门改挂接收人，可选停用原账号（留痕）。"""
        emp = self.get_object()
        to_id = request.data.get("to_employee")
        if not to_id:
            return Response({"detail": "缺少 to_employee"}, status=status.HTTP_400_BAD_REQUEST)
        try:
            to_emp = Employee.objects.get(pk=to_id)
        except Employee.DoesNotExist:
            return Response({"detail": "接收人不存在"}, status=status.HTTP_404_NOT_FOUND)
        try:
            record = handover_account(
                emp, to_emp,
                operator=request.user,
                reason=request.data.get("reason", ""),
                disable=bool(request.data.get("disable", True)),
            )
        except ValueError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(AccountHandoverSerializer(record).data, status=status.HTTP_201_CREATED)


class AccountHandoverViewSet(viewsets.ReadOnlyModelViewSet):
    queryset = AccountHandover.objects.select_related(
        "from_employee", "to_employee", "operator"
    ).all()
    serializer_class = AccountHandoverSerializer
    filterset_fields = ["from_employee", "to_employee"]
    ordering_fields = ["created_at"]


class CoverageResolveView(APIView):
    """智能区划路由：给定目的地，解析负责网点（覆盖匹配 + 排他 + 优先级仲裁）。"""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        result = resolve_coverage(
            province=request.query_params.get("province", ""),
            city=request.query_params.get("city", ""),
            district=request.query_params.get("district", ""),
        )
        return Response(result)


class OrgOverviewView(APIView):
    """组织中台总览看板：组织/人员/区划多维 KPI——G7 没有的经营视角。"""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        org_by_property = dict(
            Organization.objects.filter(is_active=True)
            .values_list("org_property")
            .annotate(n=Count("id"))
        )
        org_by_type = dict(
            Organization.objects.filter(is_active=True)
            .values_list("type")
            .annotate(n=Count("id"))
        )
        emp_by_status = dict(
            Employee.objects.values_list("status").annotate(n=Count("id"))
        )
        area_by_type = dict(
            ServiceArea.objects.filter(is_active=True)
            .values_list("area_type")
            .annotate(n=Count("id"))
        )
        no_account = Employee.objects.filter(status="active").filter(
            Q(user__isnull=True) | Q(user__is_active=False)
        ).count()
        return Response(
            {
                "organizations": {
                    "total": Organization.objects.filter(is_active=True).count(),
                    "by_property": org_by_property,
                    "by_type": org_by_type,
                },
                "employees": {
                    "total": Employee.objects.count(),
                    "active": emp_by_status.get("active", 0),
                    "by_status": emp_by_status,
                    "active_without_account": no_account,
                },
                "departments": Department.objects.filter(is_active=True).count(),
                "service_areas": {
                    "total": ServiceArea.objects.filter(is_active=True).count(),
                    "by_type": area_by_type,
                },
            }
        )
