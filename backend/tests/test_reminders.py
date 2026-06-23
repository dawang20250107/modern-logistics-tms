"""作业提醒：模板库 + 下发 + 司机强制确认。"""

import pytest

from apps.masterdata.models import Driver
from apps.ops.models import DriverReminder, ReminderTemplate, Waybill


@pytest.fixture
def client(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="rm_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "rm_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


@pytest.mark.django_db
def test_send_reminder_from_template_and_acknowledge(client):
    drv = Driver.objects.create(name="王师傅", phone="13900008888")
    wb = Waybill.objects.create(waybill_no="RMWB1", route_name="上海→成都", driver=drv)
    tpl = ReminderTemplate.objects.create(name="标准作业提醒", category="装货", content="装货要三角木、反光背心…")

    sent = client.post(f"/api/v1/waybills/{wb.waybill_no}/reminders",
                       {"template": str(tpl.id), "ack_required": True}, format="json")
    assert sent.status_code == 201, sent.content
    rid = sent.json()["data"]["id"]
    assert sent.json()["data"]["status"] == "pending"
    assert "三角木" in sent.json()["data"]["content"]

    # 司机确认收到
    ack = client.post(f"/api/v1/reminders/{rid}/acknowledge", {}, format="json")
    assert ack.status_code == 200
    assert ack.json()["data"]["status"] == "acknowledged"
    assert ack.json()["data"]["acknowledged_at"]


@pytest.mark.django_db
def test_send_custom_reminder_and_list(client):
    drv = Driver.objects.create(name="李师傅", phone="13900009999")
    wb = Waybill.objects.create(waybill_no="RMWB2", route_name="r", driver=drv)
    client.post(f"/api/v1/waybills/{wb.waybill_no}/reminders",
                {"title": "回单寄回", "content": "白联红联寄回成都", "ack_required": False}, format="json")
    lst = client.get(f"/api/v1/waybills/{wb.waybill_no}/reminders")
    assert lst.status_code == 200
    assert len(lst.json()["data"]) == 1
    assert lst.json()["data"][0]["title"] == "回单寄回"


@pytest.mark.django_db
def test_empty_reminder_rejected(client):
    wb = Waybill.objects.create(waybill_no="RMWB3", route_name="r")
    resp = client.post(f"/api/v1/waybills/{wb.waybill_no}/reminders", {"content": "   "}, format="json")
    assert resp.status_code == 400


@pytest.mark.django_db
def test_pending_reminders_filter(client):
    drv = Driver.objects.create(name="赵师傅", phone="13900000000")
    wb = Waybill.objects.create(waybill_no="RMWB4", route_name="r", driver=drv)
    from apps.ops.reminders import send_reminder

    send_reminder(wb, title="t1", content="c1")
    r2 = send_reminder(wb, title="t2", content="c2")
    r2.status = DriverReminder.STATUS_ACKNOWLEDGED
    r2.save()
    pending = client.get(f"/api/v1/reminders?driver={drv.id}&status=pending")
    assert pending.json()["data"]["total"] == 1
