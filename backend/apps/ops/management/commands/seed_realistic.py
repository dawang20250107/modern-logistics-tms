"""实战演示数据：贯穿全流程生成逼真数据，让每个页面都"满"。

驱动真实业务服务（建单→确认→进池→派单→合同→打卡推进→签收→费用→对账）+ 直接造数，
覆盖订单/运单/司机证件/合同/提醒/打卡/轨迹/异常/报销/付款/对账等全部模块。

用法：python manage.py seed_realistic [--orders 24] [--fresh]
"""

import random
from datetime import timedelta
from decimal import Decimal

from django.core.management.base import BaseCommand
from django.utils import timezone

_ROUTES = [
    ("上海", "成都", 1980), ("深圳", "武汉", 1150), ("北京", "广州", 2180),
    ("杭州", "重庆", 1700), ("苏州", "西安", 1250), ("宁波", "长沙", 980),
    ("青岛", "郑州", 760), ("天津", "沈阳", 680), ("无锡", "昆明", 2150),
]
_CARGO = ["电子配件", "汽车零部件", "锂电池组", "纺织面料", "机械设备", "日用百货", "冷链食品", "建材钢材"]
_NAMES = ["王建国", "李志强", "张伟", "刘洋", "陈鹏", "赵磊", "孙浩", "周强", "吴勇", "郑斌", "马超", "黄勇"]


class Command(BaseCommand):
    help = "生成贯穿全流程的实战演示数据（订单/运单/合同/打卡/轨迹/异常/报销/对账）。"

    def add_arguments(self, parser):
        parser.add_argument("--orders", type=int, default=24)
        parser.add_argument("--fresh", action="store_true", help="先清除上次的演示数据")

    def handle(self, *args, **opts):
        random.seed(20260101)
        self.now = timezone.now()
        if opts["fresh"]:
            self._purge()
        cust, carriers = self._masterdata()
        vehicles, drivers = self._fleet(carriers)
        self._pricing(cust, carriers)
        self._templates()

        n = opts["orders"]
        completed = max(2, n // 3)
        transit = max(2, n // 3)
        stats = {"completed": 0, "transit": 0, "early": 0, "exceptions": 0, "reimbursements": 0, "statements": 0}

        vpool, dpool = list(vehicles), list(drivers)
        for i in range(completed):
            self._full_flow(cust, carriers, vpool[i % len(vpool)], dpool[i % len(dpool)], final="completed", stats=stats)
        for i in range(transit):
            self._full_flow(cust, carriers, vpool[(i + 3) % len(vpool)], dpool[(i + 3) % len(dpool)], final="transit", stats=stats)
        for _ in range(n - completed - transit):
            self._early_order(cust, stats)

        self._statements(carriers, stats)
        self.stdout.write(self.style.SUCCESS(
            f"实战数据已生成：完成单 {stats['completed']}、在途单 {stats['transit']}、"
            f"早期单 {stats['early']}、异常 {stats['exceptions']}、报销 {stats['reimbursements']}、对账 {stats['statements']}"
        ))

    # ── 主数据 / 车队 / 价表 / 模板 ──────────────────────────
    def _masterdata(self):
        from apps.masterdata.models import Carrier, Customer

        # 复活上次 --fresh 软删除的演示主数据，避免 update_or_create 撞唯一键
        Customer.all_objects.filter(code__startswith="DEMO_C").update(is_deleted=False, deleted_at=None)
        Carrier.all_objects.filter(code__startswith="DEMO_CAR").update(is_deleted=False, deleted_at=None)
        custs = []
        for i, (name, grp) in enumerate([
            ("比亚迪供应链", "比亚迪华东物流群"), ("宁德时代物流", "宁德时代调度群"),
            ("海尔智家", "海尔成都专线群"), ("美的集团", "美的华南运输群"),
        ]):
            c, _ = Customer.objects.update_or_create(
                code=f"DEMO_C{i}", defaults={"name": name, "wechat_group": grp,
                                             "contact_name": random.choice(_NAMES), "contact_phone": _phone(),
                                             "settlement_type": "monthly", "is_active": True})
            custs.append(c)
        carriers = []
        for i, name in enumerate(["顺丰承运", "德邦物流", "安能物流", "百世快运"]):
            c, _ = Carrier.objects.update_or_create(
                code=f"DEMO_CAR{i}", defaults={"name": name, "contact_phone": _phone(), "is_active": True})
            carriers.append(c)
        return custs, carriers

    def _fleet(self, carriers):
        from apps.masterdata.models import Driver, DriverCredential, Vehicle

        vehicles = []
        for i in range(12):
            cls = Vehicle.CLASS_TRACTOR if i % 3 else Vehicle.CLASS_RIGID
            v, _ = Vehicle.objects.update_or_create(
                plate_no=f"沪D{10000 + i}", defaults={
                    "vehicle_class": cls, "vehicle_type": random.choice(["17.5米", "13米", "9.6米", "冷藏车"]),
                    "dispatch_source": random.choice([Vehicle.DISPATCH_OWN, Vehicle.DISPATCH_OWN, Vehicle.DISPATCH_EXTERNAL]),
                    "load_capacity_ton": Decimal("32"), "volume_capacity_cbm": Decimal("120"),
                    "carrier": carriers[i % len(carriers)], "is_active": True})
            vehicles.append(v)
        for i in range(4):
            Vehicle.objects.update_or_create(
                plate_no=f"沪D{10000 + i}挂", defaults={
                    "vehicle_class": Vehicle.CLASS_TRAILER, "vehicle_type": "13米平板挂",
                    "load_capacity_ton": Decimal("40"), "carrier": carriers[i % len(carriers)], "is_active": True})
        drivers = []
        emps = [Driver.EMP_EMPLOYEE, Driver.EMP_OUTSOURCED, Driver.EMP_CARRIER]
        for i in range(12):
            d, _ = Driver.objects.update_or_create(
                phone=f"138{random.randint(10000000, 99999999)}", defaults={
                    "name": _NAMES[i % len(_NAMES)], "employment_type": emps[i % 3], "wechat": f"driver_{i:02d}",
                    "app_registered": i % 4 != 0, "license_type": "A2",
                    "id_no": f"3101{random.randint(10, 99)}19{random.randint(70, 99)}{random.randint(100000, 999999)}",
                    "is_active": True})
            drivers.append(d)
            for ct in ["id_card", "driving_license", "vehicle_license"]:
                cred, created = DriverCredential.objects.get_or_create(
                    driver=d, cred_type=ct, side="main", defaults={"self_uploaded": bool(i % 2)})
                if created:
                    from apps.masterdata.credential_ocr import apply_ocr
                    apply_ocr(cred)
        return vehicles, drivers

    def _pricing(self, custs, carriers):
        from apps.finance.models import ExpenseItem, PricingRule

        for code, name, d in [("TRANSPORT_INCOME", "运输收入", "receivable"),
                              ("TRANSPORT_COST", "运输成本", "payable"), ("TOLL", "过路费", "external")]:
            ExpenseItem.objects.update_or_create(code=code, defaults={"name": name, "direction": d})
        PricingRule.objects.update_or_create(name="华东整车收入价", defaults={
            "price_type": "income", "expense_item_code": "TRANSPORT_INCOME", "customer": custs[0],
            "base_price": Decimal("800"), "min_price": Decimal("1500"),
            "tier_prices": [{"min_ton": 0, "max_ton": 10, "price": 220}, {"min_ton": 10, "max_ton": 99, "price": 180}],
            "volumetric_factor": "0.33", "fuel_surcharge_pct": "0.05", "priority": 10, "is_active": True})
        PricingRule.objects.update_or_create(name="承运成本价", defaults={
            "price_type": "cost", "expense_item_code": "TRANSPORT_COST", "carrier": carriers[0],
            "base_price": Decimal("600"), "tier_prices": [{"min_ton": 0, "max_ton": 99, "price": 150}],
            "priority": 10, "is_active": True})

    def _templates(self):
        from apps.ops.models import ReminderTemplate

        ReminderTemplate.objects.update_or_create(name="标准作业提醒", defaults={
            "category": "装货", "is_active": True,
            "content": "装货带三角木/反光背心；每天 9:30、16:30 发定位；接好单再发车；回单签字后立即拍照寄回。"})

    # ── 全流程单（完成 / 在途）──────────────────────────────
    def _full_flow(self, custs, carriers, vehicle, driver, *, final, stats):
        from apps.finance.models import ExpenseRecord
        from apps.ops.contracts import confirm_contract
        from apps.ops.intake import confirm_order, create_order_from_intake, pool_order
        from apps.ops.models import DriverCheckin, DriverReminder, ReminderTemplate
        from apps.ops.order_dispatch import dispatch_order
        from apps.ops.services import sign_waybill
        from apps.ops.workflow import advance_from_checkin

        origin, dest, dist = random.choice(_ROUTES)
        weight = round(random.uniform(8, 26), 1)
        quoted = Decimal(str(round(dist * weight * 0.9 + 800, -1)))
        days_ago = random.randint(2, 14)
        order = create_order_from_intake(
            fields={"origin": origin, "destination": dest, "cargo_desc": random.choice(_CARGO),
                    "cargo_weight_ton": weight, "cargo_quantity": random.randint(20, 200),
                    "contact_name": random.choice(_NAMES), "contact_phone": _phone(), "quoted_amount": quoted},
            channel=random.choice(["cs", "wechat_group", "miniprogram"]),
            source=custs[0].wechat_group, customer=random.choice(custs),
            cargo_items=[{"name": random.choice(_CARGO), "quantity": random.randint(10, 100), "weight_ton": weight}],
            stops=[{"stop_type": "pickup", "city": origin, "address": f"{origin}市{random.choice(['江宁', '高新', '经开'])}区仓库"},
                   {"stop_type": "delivery", "city": dest, "address": f"{dest}市物流园 {random.randint(1, 30)} 号"}],
        )
        _backdate(order, days_ago)
        order.approval_status = order.APPROVAL_NONE
        order.save(update_fields=["approval_status"])
        confirm_order(order)
        pool_order(order)
        wb = dispatch_order(order, dispatch_type="own_vehicle", vehicle=vehicle, driver=driver, carrier=carriers[0])

        # 合同（派单已自动生成）→ 司机确认
        contract = wb.contracts.first()
        if contract:
            confirm_contract(contract, accepted=True, reply="同意承运")

        # 执行点位 + 轨迹
        self._stops_and_track(wb, origin, dest)

        # 打卡推进
        nodes = ["depart", "arrive_pickup", "loading", "depart_loaded", "in_transit", "arrive_delivery"]
        if final == "transit":
            nodes = nodes[:random.randint(3, 5)]
        for node in nodes:
            DriverCheckin.objects.create(waybill=wb, driver=driver, node=node,
                                         lat=Decimal("31.23"), lng=Decimal("121.47"),
                                         note=f"{driver.name} 打卡")
            advance_from_checkin(wb, node)

        # 费用（应收/应付）
        ExpenseRecord.objects.create(waybill=wb, direction="receivable", expense_item_code="TRANSPORT_INCOME",
                                     amount=quoted, payee_type="customer", source_system="pricing")
        ExpenseRecord.objects.create(waybill=wb, direction="payable", expense_item_code="TRANSPORT_COST",
                                     amount=quoted * Decimal("0.7"), payee_type="driver",
                                     payee_ref=driver.name, source_system="pricing")

        # 提醒
        tpl = ReminderTemplate.objects.filter(name="标准作业提醒").first()
        rem = DriverReminder.objects.create(waybill=wb, driver=driver, template=tpl, title="标准作业提醒",
                                            content=tpl.content if tpl else "按要求打卡装卸", ack_required=True)
        if random.random() < 0.6:
            rem.status = DriverReminder.STATUS_ACKNOWLEDGED
            rem.acknowledged_at = self.now - timedelta(hours=random.randint(1, 40))
            rem.save(update_fields=["status", "acknowledged_at"])

        if final == "completed":
            wb.refresh_from_db()
            if wb.status in ("in_transit", "arrived"):
                sign_waybill(wb, signatory=random.choice(_NAMES), sign_source="driver")
            _backdate(wb, days_ago - 1)
            stats["completed"] += 1
        else:
            self._maybe_exception(wb, stats)
            self._maybe_reimbursement(wb, stats)
            stats["transit"] += 1

    def _stops_and_track(self, wb, origin, dest):
        from apps.ops.geofence import process_point
        from apps.ops.models import TrackingPoint, WaybillStop

        WaybillStop.objects.create(waybill=wb, seq=1, stop_type="pickup", city=origin,
                                   lat=Decimal("31.230400"), lng=Decimal("121.473700"), radius_m=800,
                                   planned_eta=self.now)
        WaybillStop.objects.create(waybill=wb, seq=2, stop_type="delivery", city=dest,
                                   lat=Decimal("30.572800"), lng=Decimal("104.066500"), radius_m=800,
                                   planned_eta=self.now + timedelta(hours=18))
        # 轨迹折线
        for k in range(8):
            t = self.now - timedelta(hours=8 - k)
            TrackingPoint.objects.create(
                waybill=wb, lat=Decimal(str(round(31.23 - k * 0.08, 6))),
                lng=Decimal(str(round(121.47 - k * 2.1, 6))), speed_kmh=Decimal("78"), reported_at=t)
        process_point(wb, 31.2305, 121.4738)  # 进围栏盖到达戳

    def _maybe_exception(self, wb, stats):
        from apps.ops.models import ExceptionRecord

        if random.random() < 0.5:
            ExceptionRecord.objects.create(
                waybill=wb, exception_type=random.choice(["transit_delay", "route_deviation", "vehicle_breakdown"]),
                level=random.choice(["low", "medium", "high"]), source="track",
                description=random.choice(["高速拥堵预计延误2小时", "偏离规划路线", "车辆故障路边维修"]),
                status=random.choice(["pending_handle", "handling"]))
            stats["exceptions"] += 1

    def _maybe_reimbursement(self, wb, stats):
        from apps.finance.reimbursement import approve_reimbursement, submit_reimbursement

        if random.random() < 0.6:
            r = submit_reimbursement(waybill=wb, category=random.choice(["toll", "fuel", "loading"]),
                                     amount=Decimal(str(random.randint(200, 1200))), reason="在途垫付")
            if random.random() < 0.6:
                approve_reimbursement(r)
            stats["reimbursements"] += 1

    # ── 早期单（草稿/待确认/已确认/进池/取消）──────────────
    _EARLY_STATES = ["draft", "pending_confirm", "confirmed", "pooled", "pooled", "cancelled"]

    def _early_order(self, custs, stats):
        from apps.ops.intake import create_order_from_intake
        from apps.ops.models import Order

        origin, dest, dist = random.choice(_ROUTES)
        weight = round(random.uniform(5, 24), 1)
        order = create_order_from_intake(
            fields={"origin": origin, "destination": dest, "cargo_desc": random.choice(_CARGO),
                    "cargo_weight_ton": weight, "cargo_quantity": random.randint(10, 120),
                    "contact_phone": _phone(), "quoted_amount": Decimal(str(round(dist * weight, -1)))},
            channel=random.choice(["cs", "self", "api", "wechat_group"]),
            customer=random.choice(custs), status=Order.STATUS_DRAFT)
        # 直接落各阶段状态，保证演示分布丰富
        state = self._EARLY_STATES[stats["early"] % len(self._EARLY_STATES)]
        order.status = state
        order.approval_status = order.APPROVAL_NONE
        if state == "pooled":
            order.pooled_at = self.now - timedelta(hours=random.randint(1, 30))
        order.save(update_fields=["status", "approval_status", "pooled_at", "updated_at"])
        _backdate(order, random.randint(0, 6))
        stats["early"] += 1

    def _statements(self, carriers, stats):
        from apps.finance.services import generate_statement

        end = self.now.date()
        start = end - timedelta(days=30)
        for direction, cp_type, cp in [("receivable", "customer", ""), ("payable", "carrier", str(carriers[0].id))]:
            try:
                st = generate_statement(direction=direction, counterparty_type=cp_type,
                                        counterparty_id=cp, start=start, end=end)
                if st:
                    stats["statements"] += 1
            except Exception:  # noqa: BLE001
                pass

    def _purge(self):
        from apps.ops.models import Order, Waybill

        # 先硬删演示运单（释放车辆/司机占用，级联合同/打卡/费用等），再软删订单
        Waybill.objects.filter(order__customer__code__startswith="DEMO_C").delete()
        Order.objects.filter(customer__code__startswith="DEMO_C").delete()


def _phone():
    return f"1{random.choice('3578')}{random.randint(100000000, 999999999)}"


def _backdate(obj, days):
    type(obj).objects.filter(pk=obj.pk).update(created_at=timezone.now() - timedelta(days=days))
