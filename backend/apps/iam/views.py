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
    Permission,
    Role,
    RoleAssignment,
    ServiceArea,
)
from .permissions import HasPermission
from .serializers import (
    AccountHandoverSerializer,
    DepartmentSerializer,
    EmployeeGroupSerializer,
    EmployeeSerializer,
    OrganizationSerializer,
    PermissionSerializer,
    RoleAssignmentSerializer,
    RoleSerializer,
    ServiceAreaSerializer,
)
from .services import build_org_tree, effective_permissions, handover_account, resolve_coverage

# 组织中台权限点：查看/组织维护/员工维护/角色权限管理（最敏感）
PERM_VIEW = "org.view"
PERM_MANAGE = "org.manage"
PERM_EMPLOYEE = "org.employee"
PERM_RBAC = "org.rbac"


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
                # 前端据此收敛导航与操作入口（超管为 ["*"]）
                "permissions": sorted(effective_permissions(user)),
            }
        )


class OrganizationViewSet(viewsets.ModelViewSet):
    queryset = Organization.objects.select_related("parent").all()
    serializer_class = OrganizationSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_VIEW, "write": PERM_MANAGE, "tree": PERM_VIEW, "export": PERM_VIEW}
    search_fields = ["code", "name", "short_name", "manager_name"]
    filterset_fields = ["type", "org_property", "is_active", "parent"]
    ordering_fields = ["sort_order", "code", "created_at"]

    @action(detail=False, methods=["get"])
    def tree(self, request):
        """组织树（含各组织自身+子树在职人头），用于查看编制分布。"""
        orgs = list(Organization.objects.filter(is_active=True))
        counts = dict(
            Employee.objects.filter(status="active")
            .values_list("organization_id")
            .annotate(n=Count("id"))
        )
        tree = build_org_tree(orgs, headcount=counts)
        return Response({"tree": tree, "total": len(orgs)})

    @action(detail=False, methods=["get"], url_path="export")
    def export(self, request):
        """导出组织为 CSV（可作为组织数据迁移的中转格式）。"""
        import csv

        from django.http import HttpResponse

        qs = self.filter_queryset(self.get_queryset())[:5000]
        resp = HttpResponse(content_type="text/csv; charset=utf-8-sig")
        resp["Content-Disposition"] = 'attachment; filename="organizations.csv"'
        resp.write("﻿")
        writer = csv.writer(resp)
        writer.writerow(["编码", "名称", "简称", "类型", "经营属性", "上级编码", "负责人", "负责人电话", "城市", "启用"])
        for o in qs:
            writer.writerow([
                o.code, o.name, o.short_name, o.get_type_display(), o.get_org_property_display(),
                o.parent.code if o.parent else "", o.manager_name, o.manager_phone, o.city,
                "是" if o.is_active else "否",
            ])
        return resp


class DepartmentViewSet(viewsets.ModelViewSet):
    queryset = Department.objects.select_related("organization", "manager", "parent").all()
    serializer_class = DepartmentSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_VIEW, "write": PERM_MANAGE}
    search_fields = ["code", "name"]
    filterset_fields = ["organization", "is_active", "parent"]
    ordering_fields = ["sort_order", "code"]


class EmployeeGroupViewSet(viewsets.ModelViewSet):
    queryset = EmployeeGroup.objects.prefetch_related("roles").all()
    serializer_class = EmployeeGroupSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_VIEW, "write": PERM_MANAGE}
    search_fields = ["code", "name"]
    filterset_fields = ["is_active"]
    ordering_fields = ["code"]


class ServiceAreaViewSet(viewsets.ModelViewSet):
    queryset = ServiceArea.objects.select_related("organization").all()
    serializer_class = ServiceAreaSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_VIEW, "write": PERM_MANAGE}
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
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {
        "read": PERM_VIEW, "write": PERM_EMPLOYEE,
        "disable": PERM_EMPLOYEE, "enable": PERM_EMPLOYEE,
        "reset_password": PERM_EMPLOYEE, "handover": PERM_EMPLOYEE, "import_csv": PERM_EMPLOYEE,
        "roles": PERM_RBAC,  # 授予角色是最敏感动作，须角色权限管理权
    }
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

    @action(detail=True, methods=["get", "post"])
    def roles(self, request, pk=None):
        """查看/设置员工角色（落到其登录账号的 RoleAssignment，按所属组织授予）。"""
        emp = self.get_object()
        if not emp.user_id:
            return Response({"detail": "该员工尚未绑定登录账号"}, status=status.HTTP_400_BAD_REQUEST)
        if request.method == "POST":
            role_ids = request.data.get("roles", [])
            roles = list(Role.objects.filter(id__in=role_ids))
            RoleAssignment.objects.filter(user=emp.user).delete()
            RoleAssignment.objects.bulk_create([
                RoleAssignment(user=emp.user, role=r, organization=emp.organization) for r in roles
            ])
        assignments = RoleAssignment.objects.filter(user=emp.user).select_related("role", "organization")
        return Response(RoleAssignmentSerializer(assignments, many=True).data)

    @action(detail=False, methods=["get"], url_path="export")
    def export(self, request):
        """导出员工为 CSV，列与导入格式一致，支持往返。"""
        import csv

        from django.http import HttpResponse

        qs = self.filter_queryset(self.get_queryset())[:5000]
        resp = HttpResponse(content_type="text/csv; charset=utf-8-sig")
        resp["Content-Disposition"] = 'attachment; filename="employees.csv"'
        resp.write("﻿")
        writer = csv.writer(resp)
        writer.writerow(["工号", "姓名", "手机", "组织编码", "职位", "直接上级工号", "状态"])
        for e in qs:
            writer.writerow([
                e.employee_no, e.name, e.phone, e.organization.code if e.organization else "",
                e.position, e.supervisor.employee_no if e.supervisor else "", e.get_status_display(),
            ])
        return resp

    @action(detail=False, methods=["post"], url_path="import")
    def import_csv(self, request):
        """批量导入员工 CSV（迁移工具）：按工号 upsert，组织/上级按编码/工号解析。

        列：工号,姓名,手机,组织编码,职位,直接上级工号（首行表头可有可无）。
        两遍处理：先 upsert 全部，再回填直接上级，保证上级已存在。
        """
        import csv
        import io

        upload = request.FILES.get("file")
        if not upload:
            return Response({"detail": "缺少文件 file"}, status=status.HTTP_400_BAD_REQUEST)
        text = upload.read().decode("utf-8-sig", errors="replace")
        reader = csv.reader(io.StringIO(text))
        org_by_code = {o.code: o for o in Organization.objects.all()}
        created, updated, errors = 0, 0, []
        sup_links: list[tuple[str, str]] = []
        for idx, row in enumerate(reader, start=1):
            if not row or not any(c.strip() for c in row):
                continue
            cells = (row + [""] * 6)[:6]
            employee_no, name, phone, org_code, position, sup_no = (c.strip() for c in cells)
            if employee_no in ("工号", "employee_no") and idx == 1:
                continue  # 跳过表头
            if not employee_no or not name:
                errors.append({"row": idx, "error": "工号与姓名必填"})
                continue
            org = org_by_code.get(org_code) if org_code else None
            if org_code and org is None:
                errors.append({"row": idx, "error": f"组织编码不存在：{org_code}"})
                continue
            _, is_new = Employee.objects.update_or_create(
                employee_no=employee_no,
                defaults={"name": name, "phone": phone, "organization": org, "position": position},
            )
            created += int(is_new)
            updated += int(not is_new)
            if sup_no:
                sup_links.append((employee_no, sup_no))
        # 回填直接上级
        emp_by_no = {e.employee_no: e for e in Employee.objects.all()}
        for emp_no, sup_no in sup_links:
            emp, sup = emp_by_no.get(emp_no), emp_by_no.get(sup_no)
            if emp and sup and emp.id != sup.id:
                emp.supervisor = sup
                emp.save(update_fields=["supervisor", "updated_at"])
        return Response({"created": created, "updated": updated, "errors": errors})


class PermissionViewSet(viewsets.ReadOnlyModelViewSet):
    queryset = Permission.objects.all()
    serializer_class = PermissionSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_RBAC}
    search_fields = ["code", "name", "module"]
    filterset_fields = ["module"]
    ordering_fields = ["module", "code"]


class RoleViewSet(viewsets.ModelViewSet):
    queryset = Role.objects.prefetch_related("permissions").all()
    serializer_class = RoleSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_RBAC, "write": PERM_RBAC, "set_permissions": PERM_RBAC}
    search_fields = ["code", "name"]
    filterset_fields = ["is_active", "data_scope"]
    ordering_fields = ["code"]

    @action(detail=True, methods=["post"], url_path="set-permissions")
    def set_permissions(self, request, pk=None):
        """覆盖式设置角色的权限点（接收 permission id 列表）。"""
        role = self.get_object()
        perm_ids = request.data.get("permissions", [])
        role.permissions.set(Permission.objects.filter(id__in=perm_ids))
        return Response(self.get_serializer(role).data)


class RbacMatrixView(APIView):
    """角色 × 权限点矩阵：集中查看各角色的权限授予情况。"""

    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = PERM_RBAC

    def get(self, request):
        perms = list(Permission.objects.all())
        modules: dict = {}
        for p in perms:
            modules.setdefault(p.module or "通用", []).append(
                {"id": str(p.id), "code": p.code, "name": p.name}
            )
        roles = []
        for role in Role.objects.prefetch_related("permissions").all():
            roles.append({
                "id": str(role.id), "code": role.code, "name": role.name,
                "data_scope": role.data_scope, "is_active": role.is_active,
                "permission_codes": list(role.permissions.values_list("code", flat=True)),
            })
        return Response({
            "modules": [{"module": m, "permissions": ps} for m, ps in sorted(modules.items())],
            "roles": roles,
            "permission_total": len(perms),
        })


class AccountHandoverViewSet(viewsets.ReadOnlyModelViewSet):
    queryset = AccountHandover.objects.select_related(
        "from_employee", "to_employee", "operator"
    ).all()
    serializer_class = AccountHandoverSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_VIEW}
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
    """组织总览看板：组织 / 人员 / 区划多维 KPI 经营视角。"""

    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = PERM_VIEW

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
