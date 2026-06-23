"""司机端 H5：登录 + 任务/强制提醒 + 打卡(定位+水印照片) + 证件自传。"""

import io

import pytest
from rest_framework.test import APIClient

from apps.masterdata.models import Driver, DriverCredential
from apps.ops.models import DriverCheckin, DriverReminder, Waybill


@pytest.fixture
def driver(db):
    return Driver.objects.create(name="司机老陈", phone="13700007777", id_no="510104198001019876")


def _login(driver, *, id_tail="019876"):
    c = APIClient()
    resp = c.post("/api/v1/driver/login", {"phone": driver.phone, "id_tail": id_tail}, format="json")
    assert resp.status_code == 200, resp.content
    token = resp.json()["data"]["token"]
    c.credentials(HTTP_X_DRIVER_TOKEN=token)
    return c, token


def _jpeg_bytes():
    from PIL import Image

    buf = io.BytesIO()
    Image.new("RGB", (200, 150), (120, 140, 160)).save(buf, format="JPEG")
    return buf.getvalue()


@pytest.mark.django_db
def test_login_and_tasks_with_pending_reminders(driver):
    wb = Waybill.objects.create(waybill_no="DPWB1", route_name="上海→成都", driver=driver,
                                status=Waybill.STATUS_IN_TRANSIT)
    DriverReminder.objects.create(waybill=wb, driver=driver, title="装货要求", content="带三角木反光背心")
    c, _ = _login(driver)
    tasks = c.get("/api/v1/driver/tasks")
    assert tasks.status_code == 200, tasks.content
    data = tasks.json()["data"]
    assert data["driver"]["name"] == "司机老陈"
    assert len(data["waybills"]) == 1
    assert len(data["pending_reminders"]) == 1
    assert data["pending_reminders"][0]["ack_required"] is True


@pytest.mark.django_db
def test_login_rejects_wrong_id_tail(driver):
    c = APIClient()
    resp = c.post("/api/v1/driver/login", {"phone": driver.phone, "id_tail": "000000"}, format="json")
    assert resp.status_code == 401


@pytest.mark.django_db
def test_force_acknowledge_reminder(driver):
    wb = Waybill.objects.create(waybill_no="DPWB2", route_name="r", driver=driver)
    r = DriverReminder.objects.create(waybill=wb, driver=driver, title="t", content="c")
    c, _ = _login(driver)
    ack = c.post(f"/api/v1/driver/reminders/{r.id}/ack", {}, format="json")
    assert ack.status_code == 200
    r.refresh_from_db()
    assert r.status == DriverReminder.STATUS_ACKNOWLEDGED


@pytest.mark.django_db
def test_checkin_with_geo_and_watermark_photo(driver):
    from django.core.files.uploadedfile import SimpleUploadedFile

    wb = Waybill.objects.create(waybill_no="DPWB3", route_name="r", driver=driver,
                                status=Waybill.STATUS_DEPARTED)
    c, _ = _login(driver)
    photo = SimpleUploadedFile("p.jpg", _jpeg_bytes(), content_type="image/jpeg")
    resp = c.post("/api/v1/driver/checkin", {
        "waybill_no": "DPWB3", "node": "arrive_pickup", "lat": "31.23", "lng": "121.47", "photo": photo,
    }, format="multipart")
    assert resp.status_code == 201, resp.content
    chk = DriverCheckin.objects.get(waybill=wb, node="arrive_pickup")
    assert chk.photo  # 水印照片已保存
    assert chk.photo.read(2) == b"\xff\xd8"  # JPEG 魔数（水印后）
    assert float(chk.lat) == 31.23


@pytest.mark.django_db
def test_invalid_token_rejected():
    c = APIClient()
    c.credentials(HTTP_X_DRIVER_TOKEN="bogus")
    assert c.get("/api/v1/driver/tasks").status_code == 401


@pytest.mark.django_db
def test_driver_self_upload_credential(driver):
    from django.core.files.uploadedfile import SimpleUploadedFile

    c, _ = _login(driver)
    f = SimpleUploadedFile("dl.jpg", _jpeg_bytes(), content_type="image/jpeg")
    resp = c.post("/api/v1/driver/credentials", {"cred_type": "driving_license", "side": "main", "file": f},
                  format="multipart")
    assert resp.status_code == 201, resp.content
    cred = DriverCredential.objects.get(driver=driver, cred_type="driving_license")
    assert cred.self_uploaded is True
    assert cred.ocr_status == "done"
