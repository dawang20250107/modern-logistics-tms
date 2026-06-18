"""SLA 时效：完成判定准时/超时 + 临期扫描 + SLA 指标。"""

from datetime import timedelta

import pytest
from django.utils import timezone

from apps.analytics.registry import compute_metric
from apps.masterdata.models import Carrier
from apps.ops.intake import create_order_from_intake, pool_order
from apps.ops.models import Order, Waybill
from apps.ops.order_dispatch import dispatch_order
from apps.ops.services import sign_waybill
from apps.ops.tasks import scan_sla_breaches


def _deliver(order):
    carrier = Carrier.objects.create(code=f"C{order.order_no[-4:]}", name="承运")
    order.status = Order.STATUS_CONFIRMED
    order.save()
    pool_order(order)
    waybill = dispatch_order(order, dispatch_type=Waybill.DISPATCH_THIRD_PARTY, carrier=carrier)
    waybill.status = Waybill.STATUS_IN_TRANSIT
    waybill.save()
    sign_waybill(waybill, signatory="张三")
    order.refresh_from_db()
    return order


@pytest.mark.django_db
def test_on_time_when_delivered_before_due():
    order = create_order_from_intake(fields={
        "origin": "A", "destination": "B",
        "expected_delivery_at": (timezone.now() + timedelta(hours=2)).isoformat(),
    })
    _deliver(order)
    assert order.sla_status == Order.SLA_ON_TIME
    assert order.delivered_at is not None


@pytest.mark.django_db
def test_breached_when_delivered_after_due():
    order = create_order_from_intake(fields={
        "origin": "A", "destination": "B",
        "expected_delivery_at": (timezone.now() - timedelta(hours=2)).isoformat(),
    })
    _deliver(order)
    assert order.sla_status == Order.SLA_BREACHED


@pytest.mark.django_db
def test_scan_marks_breach_for_in_progress():
    order = create_order_from_intake(fields={
        "origin": "A", "destination": "B",
        "expected_delivery_at": (timezone.now() - timedelta(minutes=10)).isoformat(),
    })
    order.status = Order.STATUS_CONVERTED
    order.save()
    n = scan_sla_breaches()
    order.refresh_from_db()
    assert n == 1
    assert order.sla_status == Order.SLA_BREACHED


@pytest.mark.django_db
def test_sla_metric():
    o1 = create_order_from_intake(fields={"origin": "A", "destination": "B", "expected_delivery_at": (timezone.now() + timedelta(hours=2)).isoformat()})
    _deliver(o1)
    o2 = create_order_from_intake(fields={"origin": "A", "destination": "B", "expected_delivery_at": (timezone.now() - timedelta(hours=2)).isoformat()})
    _deliver(o2)
    result = compute_metric("order.sla_on_time_rate")
    assert result["value"] == 0.5
