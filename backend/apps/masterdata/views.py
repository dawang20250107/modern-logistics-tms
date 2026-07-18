from datetime import timedelta

from django.utils import timezone
from rest_framework import viewsets
from rest_framework.decorators import action
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.core.exceptions import AppError
from apps.iam.permissions import HasPermission

from .models import B2BPartner, Carrier, Customer, Driver, Route, Vehicle
from .serializers import (
    B2BPartnerSerializer,
    CarrierSerializer,
    CustomerSerializer,
    DriverSerializer,
    RouteSerializer,
    VehicleSerializer,
)

# 主数据统一权限：读=masterdata.view，写/自定义动作=masterdata.manage。
# 用 read/write 键覆盖全部安全/非安全方法，自定义动作按 HTTP 方法自动归类。
_MD_PERMS = {"read": "masterdata.view", "write": "masterdata.manage"}


class CustomerViewSet(viewsets.ModelViewSet):
    queryset = Customer.objects.all()
    serializer_class = CustomerSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = _MD_PERMS
    search_fields = ["code", "name", "contact_phone"]
    filterset_fields = ["is_active"]
    ordering_fields = ["code", "name", "created_at"]


class CarrierViewSet(viewsets.ModelViewSet):
    """承运商主数据 + 风控：分级 / 黑名单 / 账期。写操作受 carrier.manage 权限约束。"""

    queryset = Carrier.objects.all()
    serializer_class = CarrierSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {
        "list": "carrier.view", "retrieve": "carrier.view",
        "create": "carrier.manage", "update": "carrier.manage",
        "partial_update": "carrier.manage", "destroy": "carrier.manage",
        "blacklist": "carrier.manage",
    }
    search_fields = ["code", "name", "contact_phone"]
    filterset_fields = ["is_active", "grade", "blacklisted"]
    ordering_fields = ["code", "name", "grade", "created_at"]

    @action(detail=True, methods=["post"], url_path="blacklist")
    def blacklist(self, request, pk=None):
        """拉黑 / 解除拉黑承运商。body: {blacklisted: bool, reason?: str}"""
        carrier = self.get_object()
        blacklisted = bool(request.data.get("blacklisted", True))
        reason = (request.data.get("reason") or "").strip()
        if blacklisted and not reason:
            raise AppError("REASON_REQUIRED", "拉黑承运商需填写原因。", status=400)
        carrier.blacklisted = blacklisted
        carrier.blacklist_reason = reason if blacklisted else ""
        carrier.save(update_fields=["blacklisted", "blacklist_reason", "updated_at"])
        return Response(self.get_serializer(carrier).data)


class VehicleViewSet(viewsets.ModelViewSet):
    queryset = Vehicle.objects.select_related("carrier").all()
    serializer_class = VehicleSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = _MD_PERMS
    search_fields = ["plate_no", "vehicle_type"]
    filterset_fields = ["is_active", "carrier", "vehicle_class", "dispatch_source"]
    ordering_fields = ["plate_no", "created_at"]


class DriverViewSet(viewsets.ModelViewSet):
    queryset = Driver.objects.select_related("carrier").all()
    serializer_class = DriverSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = _MD_PERMS
    search_fields = ["name", "phone", "wechat"]
    filterset_fields = ["is_active", "carrier", "employment_type", "app_registered"]
    ordering_fields = ["name", "created_at", "cumulative_waybills", "cumulative_freight"]

    @action(detail=True, methods=["post"], url_path="refresh-stats")
    def refresh_stats(self, request, pk=None):
        """刷新该司机累计运单数与运费（司机库统计）。"""
        from apps.ops.stats import refresh_driver_stats

        driver = self.get_object()
        refresh_driver_stats(driver)
        driver.refresh_from_db()
        return Response(DriverSerializer(driver).data)

    @action(detail=False, methods=["get"], url_path="lookup")
    def lookup(self, request):
        """按 姓名 + 身份证后6位 自动带出司机档案与证件。?name=&id_tail="""
        from .credential_ocr import match_driver
        from .serializers import DriverCredentialSerializer

        name = (request.query_params.get("name") or "").strip()
        id_tail = (request.query_params.get("id_tail") or "").strip()
        driver = match_driver(name=name, id_tail=id_tail)
        if driver is None:
            return Response({"matched": False, "driver": None, "credentials": []})
        creds = DriverCredentialSerializer(driver.credentials.all(), many=True).data
        return Response({"matched": True, "driver": DriverSerializer(driver).data, "credentials": creds})


class DriverCredentialViewSet(viewsets.ModelViewSet):
    """司机证件库：上传(自传/代上传) → OCR 自动识别建档。"""

    serializer_class = None  # set below
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = _MD_PERMS
    filterset_fields = ["driver", "cred_type", "ocr_status"]
    ordering_fields = ["created_at", "expiry_date"]

    def get_queryset(self):
        from .models import DriverCredential

        return DriverCredential.objects.select_related("driver").all()

    def get_serializer_class(self):
        from .serializers import DriverCredentialSerializer

        return DriverCredentialSerializer

    def perform_create(self, serializer):
        from .credential_ocr import apply_ocr

        user = self.request.user if getattr(self.request.user, "is_authenticated", False) else None
        cred = serializer.save(uploaded_by=user)
        apply_ocr(cred)  # 上传即触发 OCR 识别建档

    @action(detail=True, methods=["post"], url_path="ocr")
    def ocr(self, request, pk=None):
        """重新触发 OCR 识别。"""
        from .credential_ocr import apply_ocr

        cred = self.get_object()
        apply_ocr(cred)
        return Response(self.get_serializer(cred).data)


class RouteViewSet(viewsets.ModelViewSet):
    queryset = Route.objects.all()
    serializer_class = RouteSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = _MD_PERMS
    search_fields = ["code", "name", "origin", "destination"]
    filterset_fields = ["is_active"]
    ordering_fields = ["code", "created_at"]


class ExpiringCredentialsView(APIView):
    """证件到期预警：返回 N 天内到期（或已过期）的车辆/司机证件。?days=30

    每条含 days_left（负数=已过期）与 severity（expired/critical/warning），
    并按紧迫度（已过期/天数升序）排序，便于车队合规台一眼锁定风险。
    """

    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = "masterdata.view"

    def get(self, request):
        days = int(request.query_params.get("days") or 30)
        today = timezone.localdate()
        deadline = today + timedelta(days=days)

        def severity(days_left: int) -> str:
            if days_left < 0:
                return "expired"
            if days_left <= 7:
                return "critical"
            return "warning"

        vehicles = []
        for v in Vehicle.objects.filter(is_active=True):
            for field, label in [
                ("inspection_expiry", "年检"),
                ("insurance_expiry", "保险"),
                ("maintenance_due_date", "维保"),
            ]:
                expiry = getattr(v, field)
                if expiry and expiry <= deadline:
                    days_left = (expiry - today).days
                    vehicles.append({
                        "subject": v.plate_no,
                        "plate_no": v.plate_no,
                        "credential": label,
                        "expiry": expiry.isoformat(),
                        "days_left": days_left,
                        "severity": severity(days_left),
                    })

        drivers = []
        for d in Driver.objects.filter(is_active=True):
            for field, label in [("license_expiry", "驾照"), ("qualification_expiry", "从业资格")]:
                expiry = getattr(d, field)
                if expiry and expiry <= deadline:
                    days_left = (expiry - today).days
                    drivers.append({
                        "subject": d.name,
                        "name": d.name,
                        "credential": label,
                        "expiry": expiry.isoformat(),
                        "days_left": days_left,
                        "severity": severity(days_left),
                    })

        carriers = []
        for c in Carrier.objects.filter(is_active=True, qualification_expiry__isnull=False):
            expiry = c.qualification_expiry
            if expiry and expiry <= deadline:
                days_left = (expiry - today).days
                carriers.append({
                    "subject": c.name,
                    "name": c.name,
                    "credential": "承运资质",
                    "expiry": expiry.isoformat(),
                    "days_left": days_left,
                    "severity": severity(days_left),
                })

        vehicles.sort(key=lambda r: r["days_left"])
        drivers.sort(key=lambda r: r["days_left"])
        carriers.sort(key=lambda r: r["days_left"])
        rows = vehicles + drivers + carriers
        summary = {
            "total": len(rows),
            "expired": sum(1 for r in rows if r["severity"] == "expired"),
            "critical": sum(1 for r in rows if r["severity"] == "critical"),
            "warning": sum(1 for r in rows if r["severity"] == "warning"),
        }
        return Response({
            "days": days, "summary": summary,
            "vehicles": vehicles, "drivers": drivers, "carriers": carriers,
        })


class B2BPartnerViewSet(viewsets.ModelViewSet):
    """B2B 业务伙伴/发货方/收货方/供应商视图集。"""

    queryset = B2BPartner.objects.all()
    serializer_class = B2BPartnerSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = _MD_PERMS
    search_fields = ["code", "name", "contact_phone", "city"]
    filterset_fields = ["partner_type", "is_active"]
    ordering_fields = ["code", "name", "created_at"]
