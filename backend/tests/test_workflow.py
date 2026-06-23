"""工作流编排打通：派单→自动合同、打卡→自动推进状态、合同确认→司机注册、全流程总览。"""

import pytest

from apps.masterdata.models import Driver, Vehicle
from apps.ops.intake import create_order_from_intake
from apps.ops.models import Contract, Order, Waybill
from apps.ops.order_dispatch import dispatch_order


def _pooled_order(**kw):
    o = create_order_from_intake(fields={"origin": "上海", "destination": "成都", **kw})
    o.status = Order.STATUS_POOLED
    o.save(update_fields=["status"])
    return o


@pytest.mark.django_db
def test_dispatch_auto_generates_contract():
    veh = Vehicle.objects.create(plate_no="沪WF001", load_capacity_ton=30)
    drv = Driver.objects.create(name="王师傅", phone="13900001234")
    order = _pooled_order(cargo_weight_ton=8)
    wb = dispatch_order(order, dispatch_type=Waybill.DISPATCH_OWN, vehicle=veh, driver=drv)
    # 派单即出合同
    assert Contract.objects.filter(waybill=wb).exists()


@pytest.mark.django_db
def test_contract_confirm_marks_driver_onboarded():
    from apps.ops.contracts import confirm_contract, generate_contract

    drv = Driver.objects.create(name="李师傅", phone="13900005678")
    wb = Waybill.objects.create(waybill_no="WFWB1", route_name="r", driver=drv)
    c = generate_contract(wb)
    assert drv.app_registered is False
    confirm_contract(c, accepted=True)
    drv.refresh_from_db()
    assert drv.app_registered is True
    assert drv.app_registered_at is not None


@pytest.mark.django_db
def test_checkin_advances_waybill_status():
    from apps.ops.workflow import advance_from_checkin

    wb = Waybill.objects.create(waybill_no="WFWB2", route_name="r", status=Waybill.STATUS_PENDING_DISPATCH)
    # 装货打卡 → 自动推进到已装车（途中跨过已派车）
    advance_from_checkin(wb, "loading")
    wb.refresh_from_db()
    assert wb.status == Waybill.STATUS_LOADED
    assert wb.loaded_at is not None
    # 发车 → 已发车
    advance_from_checkin(wb, "depart_loaded")
    wb.refresh_from_db()
    assert wb.status == Waybill.STATUS_DEPARTED
    # 到达卸货地 → 在途→已到达
    advance_from_checkin(wb, "arrive_delivery")
    wb.refresh_from_db()
    assert wb.status == Waybill.STATUS_ARRIVED


@pytest.mark.django_db
def test_order_workflow_overview(admin_client):
    veh = Vehicle.objects.create(plate_no="沪WF777", load_capacity_ton=30)
    drv = Driver.objects.create(name="赵师傅", phone="13900007777")
    order = _pooled_order(cargo_weight_ton=6)
    dispatch_order(order, dispatch_type=Waybill.DISPATCH_OWN, vehicle=veh, driver=drv)
    resp = admin_client.get(f"/api/v1/orders/{order.id}/workflow")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    keys = [s["key"] for s in data["stages"]]
    assert keys[:4] == ["created", "confirmed", "dispatched", "contract"]
    dispatched = next(s for s in data["stages"] if s["key"] == "dispatched")
    assert dispatched["done"] is True


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="wf_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "wf_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c
