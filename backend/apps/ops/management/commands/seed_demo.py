from django.contrib.auth import get_user_model
from django.core.management.base import BaseCommand
from django.utils.dateparse import parse_datetime

from apps.ai.models import AgentSuggestion
from apps.finance.models import ExpenseItem, ExpenseRecord, PricingRule
from apps.iam.models import Organization, Permission, Role, RoleAssignment
from apps.masterdata.models import Carrier, Customer, Driver, Vehicle
from apps.ops.models import ExceptionRecord, Waybill, WaybillEvent


def dt(value):
    return parse_datetime(value)


class Command(BaseCommand):
    help = "Seed development logistics demo data (主数据 + 运单 + 事件 + 费用 + 异常 + AI 建议)。"

    def handle(self, *args, **options):
        customer, _ = Customer.objects.update_or_create(
            code="CUST_DEMO", defaults={"name": "示例客户", "is_active": True}
        )
        carrier, _ = Carrier.objects.update_or_create(
            code="CAR_DEMO", defaults={"name": "示例承运商", "is_active": True}
        )
        backup_carrier, _ = Carrier.objects.update_or_create(
            code="CAR_BACKUP", defaults={"name": "备用承运商", "is_active": True}
        )

        for code, name, direction in [
            ("TRANSPORT_INCOME", "运输收入", ExpenseItem.DIRECTION_RECEIVABLE),
            ("TRANSPORT_COST", "运输成本", ExpenseItem.DIRECTION_PAYABLE),
            ("TOLL_FEE", "过路费", ExpenseItem.DIRECTION_EXTERNAL),
        ]:
            ExpenseItem.objects.update_or_create(code=code, defaults={"name": name, "direction": direction})

        PricingRule.objects.update_or_create(
            name="默认收入价",
            defaults={"price_type": "income", "expense_item_code": "TRANSPORT_INCOME",
                      "base_price": "5000", "price_per_ton": "120", "priority": 1},
        )
        PricingRule.objects.update_or_create(
            name="默认支出价",
            defaults={"price_type": "cost", "expense_item_code": "TRANSPORT_COST",
                      "base_price": "3500", "price_per_ton": "90", "priority": 1},
        )

        self._seed_rbac()

        vehicle_1, _ = Vehicle.objects.update_or_create(
            plate_no="川A****1", defaults={"vehicle_type": "17.5m", "carrier": carrier}
        )
        vehicle_2, _ = Vehicle.objects.update_or_create(
            plate_no="沪B****2", defaults={"vehicle_type": "冷链", "carrier": carrier}
        )
        driver_1, _ = Driver.objects.update_or_create(
            phone="13800000001", defaults={"name": "示例司机A", "carrier": carrier}
        )
        driver_2, _ = Driver.objects.update_or_create(
            phone="13800000002", defaults={"name": "示例司机B", "carrier": backup_carrier}
        )

        waybills = [
            {
                "waybill_no": "YD2606040010", "route_name": "宜宾 -> 临港", "origin": "宜宾",
                "destination": "临港", "status": Waybill.STATUS_IN_TRANSIT, "dispatch_status": "accepted",
                "risk_level": Waybill.RISK_HIGH, "receipt_status": "pending", "eta_drift_minutes": 1920,
                "vehicle": vehicle_1, "driver": driver_1, "cargo_quantity": 120,
                "cargo_weight_ton": "28.50", "cargo_volume_cbm": "80.00",
                "planned_arrival": dt("2026-06-05T18:00:00+08:00"), "estimated_arrival": dt("2026-06-07T02:00:00+08:00"),
            },
            {
                "waybill_no": "SH2606047705", "route_name": "上海 -> 长沙", "origin": "上海",
                "destination": "长沙", "status": Waybill.STATUS_IN_TRANSIT, "dispatch_status": "accepted",
                "risk_level": Waybill.RISK_MEDIUM, "receipt_status": "not_due", "eta_drift_minutes": 360,
                "vehicle": vehicle_2, "driver": driver_2, "cargo_quantity": 42,
                "cargo_weight_ton": "18.20", "cargo_volume_cbm": "54.00",
                "planned_arrival": dt("2026-06-05T12:00:00+08:00"), "estimated_arrival": dt("2026-06-05T18:00:00+08:00"),
            },
            {
                "waybill_no": "YD2606048158", "route_name": "青岛 -> 宜宾", "origin": "青岛",
                "destination": "宜宾", "status": Waybill.STATUS_DELIVERED, "dispatch_status": "accepted",
                "risk_level": Waybill.RISK_LOW, "receipt_status": "pending", "eta_drift_minutes": 0,
                "vehicle": vehicle_1, "driver": driver_1, "cargo_quantity": 80,
                "cargo_weight_ton": "22.00", "cargo_volume_cbm": "68.00",
                "planned_arrival": dt("2026-06-04T16:00:00+08:00"), "estimated_arrival": dt("2026-06-04T15:50:00+08:00"),
            },
            {
                "waybill_no": "SH2606039981", "route_name": "深圳 -> 成都", "origin": "深圳",
                "destination": "成都", "status": Waybill.STATUS_IN_TRANSIT, "dispatch_status": "accepted",
                "risk_level": Waybill.RISK_NONE, "receipt_status": "not_due", "eta_drift_minutes": 0,
                "vehicle": vehicle_2, "driver": driver_2, "cargo_quantity": 64,
                "cargo_weight_ton": "19.40", "cargo_volume_cbm": "58.00",
                "planned_arrival": dt("2026-06-06T09:00:00+08:00"), "estimated_arrival": dt("2026-06-06T08:55:00+08:00"),
            },
        ]

        seeded = []
        for item in waybills:
            waybill_no = item.pop("waybill_no")
            org = self.org_sh if waybill_no.startswith("SH") else self.org_cd
            waybill, _ = Waybill.objects.update_or_create(
                waybill_no=waybill_no,
                defaults={
                    **item,
                    "customer": customer,
                    "carrier": item["vehicle"].carrier,
                    "organization": org,
                },
            )
            seeded.append(waybill)
            self._refresh_related(waybill)

        self.stdout.write(self.style.SUCCESS(f"Seeded {len(seeded)} waybills."))
        self.stdout.write("Demo 用户：dispatcher/Dispatch123!（上海网点，可管运单）、viewer/Viewer123!（只读）。")

    def _seed_rbac(self):
        user_model = get_user_model()

        # 组织树：集团 → 华东公司 → 上海网点 / 成都网点
        group, _ = Organization.objects.update_or_create(
            code="JT", defaults={"name": "示例集团", "type": "group", "parent": None}
        )
        east, _ = Organization.objects.update_or_create(
            code="EAST", defaults={"name": "华东公司", "type": "company", "parent": group}
        )
        self.org_sh, _ = Organization.objects.update_or_create(
            code="SH001", defaults={"name": "上海网点", "type": "station", "parent": east}
        )
        self.org_cd, _ = Organization.objects.update_or_create(
            code="CD001", defaults={"name": "成都网点", "type": "station", "parent": east}
        )

        # 权限点
        perms = {}
        for code, name in [("waybill.view", "查看运单"), ("waybill.manage", "管理运单"), ("finance.view", "查看费用")]:
            perms[code], _ = Permission.objects.update_or_create(
                code=code, defaults={"name": name, "module": code.split(".")[0]}
            )

        # 角色
        dispatcher_role, _ = Role.objects.update_or_create(
            code="dispatcher", defaults={"name": "调度员", "data_scope": "org_sub"}
        )
        dispatcher_role.permissions.set([perms["waybill.view"], perms["waybill.manage"]])
        viewer_role, _ = Role.objects.update_or_create(
            code="viewer", defaults={"name": "只读", "data_scope": "org_sub"}
        )
        viewer_role.permissions.set([perms["waybill.view"]])

        # 演示用户（非超管，用于演示权限点与数据域）
        dispatcher = self._ensure_user(user_model, "dispatcher", "Dispatch123!", self.org_sh, "上海调度")
        viewer = self._ensure_user(user_model, "viewer", "Viewer123!", self.org_sh, "只读用户")
        RoleAssignment.objects.update_or_create(user=dispatcher, role=dispatcher_role, organization=self.org_sh)
        RoleAssignment.objects.update_or_create(user=viewer, role=viewer_role, organization=self.org_sh)

    @staticmethod
    def _ensure_user(user_model, username, password, org, nickname=""):
        user, created = user_model.objects.get_or_create(
            username=username, defaults={"nickname": nickname, "organization": org}
        )
        changed = False
        if user.organization_id != org.id:
            user.organization = org
            changed = True
        if created:
            user.set_password(password)
            changed = True
        if changed:
            user.save()
        return user

    def _refresh_related(self, waybill):
        waybill.events.all().delete()
        waybill.expenses.all().delete()
        waybill.exceptions.all().delete()
        waybill.agent_suggestions.all().delete()

        for event_type, event_time in [
            ("order_confirmed", "2026-06-04T09:10:00+08:00"),
            ("dispatched", "2026-06-04T10:20:00+08:00"),
            ("loaded", "2026-06-04T16:12:00+08:00"),
            ("in_transit", "2026-06-04T17:58:00+08:00"),
        ]:
            WaybillEvent.objects.create(
                waybill=waybill, event_type=event_type, event_time=dt(event_time),
                resource=waybill.waybill_no, source="seed", payload={"source": "seed"},
            )

        ExpenseRecord.objects.create(
            waybill=waybill, direction=ExpenseRecord.DIRECTION_RECEIVABLE,
            expense_item_code="TRANSPORT_INCOME", amount="8500.00", risk_status="normal",
        )
        ExpenseRecord.objects.create(
            waybill=waybill, direction=ExpenseRecord.DIRECTION_PAYABLE,
            expense_item_code="TRANSPORT_COST", amount="6200.00", risk_status="normal",
        )

        if waybill.risk_level in {Waybill.RISK_HIGH, Waybill.RISK_MEDIUM}:
            AgentSuggestion.objects.create(
                waybill=waybill,
                suggestion_type="eta_or_route",
                title="处理" + ("ETA 风险" if waybill.risk_level == Waybill.RISK_MEDIUM else "路线偏移"),
                body="建议确认偏航或拥堵原因，并向客户同步 ETA。",
                tool_name="logistics.eta_risk_analysis",
                evidence={"waybill_no": waybill.waybill_no, "eta_drift_minutes": waybill.eta_drift_minutes},
            )

        if waybill.receipt_status == "pending":
            ExceptionRecord.objects.create(
                waybill=waybill, exception_type="receipt_pending", description="电子回单未确认",
                status="pending_handle", responsibility_party="carrier", amount="0.00",
            )
