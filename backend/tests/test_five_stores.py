"""五大核心数据库字段补全：司机/客户/车辆派生统计 + AI会话ID。"""

import pytest

from apps.masterdata.models import Customer, Driver, Vehicle
from apps.ops.intake import convert_order_to_waybill, create_order_from_intake
from apps.ops.models import Order, Waybill


@pytest.mark.django_db
def test_ai_conversation_id_flows_order_to_waybill():
    order = create_order_from_intake(
        fields={"origin": "上海", "destination": "成都", "ai_conversation_id": "conv-123"},
        status=Order.STATUS_CONFIRMED,
    )
    assert order.ai_conversation_id == "conv-123"
    wb = convert_order_to_waybill(order)
    assert wb.ai_conversation_id == "conv-123"


@pytest.mark.django_db
def test_customer_history_aggregates(admin_client):
    cust = Customer.objects.create(code="CH1", name="比亚迪")
    create_order_from_intake(fields={"origin": "上海", "destination": "成都"}, customer=cust)
    create_order_from_intake(fields={"origin": "上海", "destination": "成都"}, customer=cust)
    create_order_from_intake(fields={"origin": "深圳", "destination": "北京"}, customer=cust)
    resp = admin_client.get(f"/api/v1/customers/{cust.id}")
    assert resp.status_code == 200, resp.content
    hist = resp.json()["data"]["history"]
    assert hist["order_count"] == 3
    assert hist["common_routes"][0] == "上海→成都"


@pytest.mark.django_db
def test_driver_cumulative_stats_refresh():
    from apps.finance.models import ExpenseRecord
    from apps.ops.stats import refresh_driver_stats

    drv = Driver.objects.create(name="老王", phone="13900000000")
    wb = Waybill.objects.create(waybill_no="DS1", route_name="r", driver=drv, status=Waybill.STATUS_SIGNED)
    ExpenseRecord.objects.create(
        waybill=wb, direction=ExpenseRecord.DIRECTION_PAYABLE, expense_item_code="TRANSPORT_COST",
        amount=5000, payee_type="driver",
    )
    refresh_driver_stats(drv)
    drv.refresh_from_db()
    assert drv.cumulative_waybills == 1
    assert float(drv.cumulative_freight) == 5000.0


@pytest.mark.django_db
def test_vehicle_freight_total_in_serializer(admin_client):
    from apps.finance.models import ExpenseRecord

    veh = Vehicle.objects.create(plate_no="沪Z0001", dispatch_source=Vehicle.DISPATCH_EXTERNAL)
    wb = Waybill.objects.create(waybill_no="VF1", route_name="r", vehicle=veh)
    ExpenseRecord.objects.create(
        waybill=wb, direction=ExpenseRecord.DIRECTION_PAYABLE, expense_item_code="TRANSPORT_COST", amount=3200,
    )
    resp = admin_client.get(f"/api/v1/vehicles/{veh.id}")
    data = resp.json()["data"]
    assert data["dispatch_source_label"] == "外调"
    assert data["freight_total"] == 3200.0


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="store_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "store_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c
