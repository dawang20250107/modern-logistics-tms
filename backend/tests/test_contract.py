"""合同库：生成（含PDF）/ 发送 / 司机确认。"""

import pytest

from apps.masterdata.models import Driver, Vehicle
from apps.ops.models import Contract, Waybill


@pytest.fixture
def client(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="ct_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "ct_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


def _waybill():
    drv = Driver.objects.create(name="合同司机", phone="13700000000")
    veh = Vehicle.objects.create(plate_no="沪C8888")
    return Waybill.objects.create(waybill_no="CTWB1", route_name="上海→成都", driver=drv, vehicle=veh,
                                  cargo_weight_ton=8, cargo_quantity=10)


@pytest.mark.django_db
def test_generate_contract_with_pdf():
    from apps.ops.contracts import generate_contract

    wb = _waybill()
    c = generate_contract(wb)
    assert c.contract_no.startswith("HT")
    assert "运输承运合同" in c.content
    assert "合同司机" in c.content
    assert c.confirm_status == Contract.STATUS_PENDING
    assert c.pdf  # PDF 已生成
    assert c.pdf.read(4) == b"%PDF"  # 是合法 PDF


@pytest.mark.django_db
def test_contract_full_flow_via_api(client):
    wb = _waybill()
    gen = client.post(f"/api/v1/waybills/{wb.waybill_no}/contract", {}, format="json")
    assert gen.status_code == 201, gen.content
    sent = client.post(f"/api/v1/waybills/{wb.waybill_no}/contract/send", {}, format="json")
    assert sent.status_code == 200
    assert sent.json()["data"]["confirm_status"] == "sent"
    assert sent.json()["data"]["sent_at"]
    conf = client.post(f"/api/v1/waybills/{wb.waybill_no}/contract/confirm",
                       {"accepted": True, "reply": "同意承运"}, format="json")
    assert conf.status_code == 200
    data = conf.json()["data"]
    assert data["confirm_status"] == "confirmed"
    assert data["driver_reply"] == "同意承运"
    assert data["status_label"] == "已确认"
    # 取最新合同
    got = client.get(f"/api/v1/waybills/{wb.waybill_no}/contract")
    assert got.json()["data"]["confirm_status"] == "confirmed"


@pytest.mark.django_db
def test_wechat_send_is_reserved():
    from apps.integrations.wechat import send_contract_to_driver

    res = send_contract_to_driver(type("C", (), {"contract_no": "HT1"})())
    assert res["status"] == "reserved"
