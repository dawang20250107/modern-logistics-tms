"""全局查单：车牌/电话/单号解析为答案卡。"""

import pytest

from apps.masterdata.models import Customer, Driver, Vehicle
from apps.ops.models import Order, Waybill


@pytest.mark.django_db
def test_lookup_by_plate_returns_active_waybill_card():
    drv = Driver.objects.create(name="张师傅", phone="13800138000")
    veh = Vehicle.objects.create(plate_no="苏B12345", load_capacity_ton=20)
    Waybill.objects.create(waybill_no="YD-L1", route_name="r", origin="上海", destination="杭州",
                           status=Waybill.STATUS_IN_TRANSIT, vehicle=veh, driver=drv)

    from apps.ops.lookup import global_lookup

    card = global_lookup("苏B12345")
    assert card["kind"] == "waybill"
    assert card["waybill_no"] == "YD-L1"
    assert "call_driver" in card["actions"]
    assert any(f["label"] == "司机" and "138****8000" in f["value"] for f in card["fields"])


@pytest.mark.django_db
def test_lookup_by_waybill_no():
    Waybill.objects.create(waybill_no="YD20260719001", route_name="r", origin="上海", destination="南京",
                           status=Waybill.STATUS_DISPATCHED)
    from apps.ops.lookup import global_lookup

    assert global_lookup("YD20260719001")["kind"] == "waybill"


@pytest.mark.django_db
def test_lookup_by_phone_no_active_waybill():
    Driver.objects.create(name="李师傅", phone="13900139000")
    from apps.ops.lookup import global_lookup

    card = global_lookup("13900139000")
    assert card["kind"] == "driver"
    assert "无在途运单" in card["fields"][-1]["value"]


@pytest.mark.django_db
def test_lookup_customer_and_none():
    Customer.objects.create(code="C1", name="阿斯利康", credit_days=30)
    Order.objects.create(order_no="DD1", status="pooled")
    from apps.ops.lookup import global_lookup

    assert global_lookup("阿斯利康")["kind"] == "customer"
    assert global_lookup("DD1")["kind"] == "order"
    assert global_lookup("zzzznotexist")["kind"] == "none"
