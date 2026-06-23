from django.core.management.base import BaseCommand
from django.utils.dateparse import parse_datetime

from apps.api.models import (
    AgentSuggestion,
    Carrier,
    Customer,
    Driver,
    ExceptionRecord,
    ExpenseRecord,
    Vehicle,
    Waybill,
    WaybillEvent,
)


def dt(value):
    return parse_datetime(value)


class Command(BaseCommand):
    help = "Seed development logistics data."

    def handle(self, *args, **options):
        customer, _ = Customer.objects.update_or_create(
            code="CUST_DEMO",
            defaults={"name": "\u793a\u4f8b\u5ba2\u6237", "is_active": True},
        )
        carrier, _ = Carrier.objects.update_or_create(
            code="CAR_DEMO",
            defaults={"name": "\u793a\u4f8b\u627f\u8fd0\u5546", "is_active": True},
        )
        backup_carrier, _ = Carrier.objects.update_or_create(
            code="CAR_BACKUP",
            defaults={"name": "\u5907\u7528\u627f\u8fd0\u5546", "is_active": True},
        )

        vehicle_1, _ = Vehicle.objects.update_or_create(
            plate_no="\u5dddA****1",
            defaults={"vehicle_type": "17.5m", "vehicle_class": Vehicle.CLASS_TRACTOR,
                      "dispatch_source": Vehicle.DISPATCH_OWN, "carrier": carrier},
        )
        vehicle_2, _ = Vehicle.objects.update_or_create(
            plate_no="\u6caaB****2",
            defaults={"vehicle_type": "\u51b7\u94fe", "vehicle_class": Vehicle.CLASS_RIGID,
                      "dispatch_source": Vehicle.DISPATCH_PLATFORM, "carrier": carrier},
        )
        Vehicle.objects.update_or_create(
            plate_no="\u5dddA****1\u6302",
            defaults={"vehicle_type": "13m \u5e73\u677f\u6302", "vehicle_class": Vehicle.CLASS_TRAILER,
                      "dispatch_source": Vehicle.DISPATCH_OWN, "carrier": carrier},
        )
        driver_1, _ = Driver.objects.update_or_create(
            phone="13800000001",
            defaults={"name": "\u793a\u4f8b\u53f8\u673aA", "employment_type": Driver.EMP_EMPLOYEE, "carrier": carrier,
                      "wechat": "driverA_wx", "app_registered": True},
        )
        driver_2, _ = Driver.objects.update_or_create(
            phone="13800000002",
            defaults={"name": "\u793a\u4f8b\u53f8\u673aB", "employment_type": Driver.EMP_OUTSOURCED, "carrier": backup_carrier,
                      "wechat": "driverB_wx", "app_registered": False},
        )

        waybills = [
            {
                "waybill_no": "YD2606040010",
                "route_name": "\u5b9c\u5bbe -> \u4e34\u6e2f",
                "origin": "\u5b9c\u5bbe",
                "destination": "\u4e34\u6e2f",
                "status": Waybill.STATUS_IN_TRANSIT,
                "dispatch_status": "accepted",
                "risk_level": Waybill.RISK_HIGH,
                "receipt_status": "pending",
                "eta_drift_minutes": 1920,
                "vehicle": vehicle_1,
                "driver": driver_1,
                "cargo_quantity": 120,
                "cargo_weight_ton": "28.50",
                "cargo_volume_cbm": "80.00",
                "planned_arrival": dt("2026-06-05T18:00:00+08:00"),
                "estimated_arrival": dt("2026-06-07T02:00:00+08:00"),
            },
            {
                "waybill_no": "SH2606047705",
                "route_name": "\u4e0a\u6d77 -> \u957f\u6c99",
                "origin": "\u4e0a\u6d77",
                "destination": "\u957f\u6c99",
                "status": Waybill.STATUS_IN_TRANSIT,
                "dispatch_status": "accepted",
                "risk_level": Waybill.RISK_MEDIUM,
                "receipt_status": "not_due",
                "eta_drift_minutes": 360,
                "vehicle": vehicle_2,
                "driver": driver_2,
                "cargo_quantity": 42,
                "cargo_weight_ton": "18.20",
                "cargo_volume_cbm": "54.00",
                "planned_arrival": dt("2026-06-05T12:00:00+08:00"),
                "estimated_arrival": dt("2026-06-05T18:00:00+08:00"),
            },
            {
                "waybill_no": "YD2606048158",
                "route_name": "\u9752\u5c9b -> \u5b9c\u5bbe",
                "origin": "\u9752\u5c9b",
                "destination": "\u5b9c\u5bbe",
                "status": Waybill.STATUS_DELIVERED,
                "dispatch_status": "accepted",
                "risk_level": Waybill.RISK_LOW,
                "receipt_status": "pending",
                "eta_drift_minutes": 0,
                "vehicle": vehicle_1,
                "driver": driver_1,
                "cargo_quantity": 80,
                "cargo_weight_ton": "22.00",
                "cargo_volume_cbm": "68.00",
                "planned_arrival": dt("2026-06-04T16:00:00+08:00"),
                "estimated_arrival": dt("2026-06-04T15:50:00+08:00"),
            },
            {
                "waybill_no": "SH2606039981",
                "route_name": "\u6df1\u5733 -> \u6210\u90fd",
                "origin": "\u6df1\u5733",
                "destination": "\u6210\u90fd",
                "status": Waybill.STATUS_IN_TRANSIT,
                "dispatch_status": "accepted",
                "risk_level": Waybill.RISK_NONE,
                "receipt_status": "not_due",
                "eta_drift_minutes": 0,
                "vehicle": vehicle_2,
                "driver": driver_2,
                "cargo_quantity": 64,
                "cargo_weight_ton": "19.40",
                "cargo_volume_cbm": "58.00",
                "planned_arrival": dt("2026-06-06T09:00:00+08:00"),
                "estimated_arrival": dt("2026-06-06T08:55:00+08:00"),
            },
        ]

        seeded = []
        for item in waybills:
            waybill_no = item.pop("waybill_no")
            waybill, _ = Waybill.objects.update_or_create(
                waybill_no=waybill_no,
                defaults={**item, "customer": customer, "carrier": item["vehicle"].carrier},
            )
            seeded.append(waybill)
            self.refresh_related_data(waybill)

        self.stdout.write(self.style.SUCCESS(f"Seeded {len(seeded)} waybills."))

    def refresh_related_data(self, waybill):
        waybill.events.all().delete()
        waybill.expenses.all().delete()
        waybill.exceptions.all().delete()
        waybill.agent_suggestions.all().delete()

        base_events = [
            ("order_confirmed", "2026-06-04T09:10:00+08:00"),
            ("dispatched", "2026-06-04T10:20:00+08:00"),
            ("loaded", "2026-06-04T16:12:00+08:00"),
            ("in_transit", "2026-06-04T17:58:00+08:00"),
        ]
        for event_type, event_time in base_events:
            WaybillEvent.objects.create(
                waybill=waybill,
                event_type=event_type,
                event_time=dt(event_time),
                resource=waybill.waybill_no,
                payload={"source": "seed"},
            )

        ExpenseRecord.objects.create(
            waybill=waybill,
            direction=ExpenseRecord.DIRECTION_RECEIVABLE,
            expense_item_code="TRANSPORT_INCOME",
            amount="8500.00",
            risk_status="normal",
        )
        ExpenseRecord.objects.create(
            waybill=waybill,
            direction=ExpenseRecord.DIRECTION_PAYABLE,
            expense_item_code="TRANSPORT_COST",
            amount="6200.00",
            risk_status="normal",
        )

        if waybill.risk_level in {Waybill.RISK_HIGH, Waybill.RISK_MEDIUM}:
            AgentSuggestion.objects.create(
                waybill=waybill,
                suggestion_type="eta_or_route",
                title="\u5904\u7406" + ("ETA \u98ce\u9669" if waybill.risk_level == Waybill.RISK_MEDIUM else "\u8def\u7ebf\u504f\u79fb"),
                body="\u5efa\u8bae\u786e\u8ba4\u504f\u822a\u6216\u62e5\u5835\u539f\u56e0\uff0c\u5e76\u5411\u5ba2\u6237\u540c\u6b65 ETA\u3002",
                evidence={"waybill_no": waybill.waybill_no, "eta_drift_minutes": waybill.eta_drift_minutes},
            )

        if waybill.receipt_status == "pending":
            ExceptionRecord.objects.create(
                waybill=waybill,
                exception_type="receipt_pending",
                description="\u7535\u5b50\u56de\u5355\u672a\u786e\u8ba4",
                status="pending_handle",
                responsibility_party="carrier",
                amount="0.00",
            )
