"""司机证件库：上传(自传/代上传) + OCR 建档 + 姓名/身份证后6位检索。"""

import pytest

from apps.masterdata.models import Driver, DriverCredential


@pytest.fixture
def client(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="cred_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "cred_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


def _png():
    from django.core.files.uploadedfile import SimpleUploadedFile

    return SimpleUploadedFile("idcard.png", b"\x89PNG\r\n\x1a\n fake", content_type="image/png")


@pytest.mark.django_db
def test_upload_credential_triggers_ocr(client):
    drv = Driver.objects.create(name="张三", phone="13800001111", id_no="510104199001011234")
    resp = client.post("/api/v1/driver-credentials", {
        "driver": str(drv.id), "cred_type": "id_card", "side": "main",
        "file": _png(), "self_uploaded": "true",
    }, format="multipart")
    assert resp.status_code == 201, resp.content
    data = resp.json()["data"]
    assert data["cred_type_label"] == "身份证"
    assert data["ocr_status"] == "manual"  # 上传触发 OCR；无引擎→待人工录入，不造数


@pytest.mark.django_db
def test_lookup_by_name_and_id_tail(client):
    drv = Driver.objects.create(name="李四", phone="13800002222", id_no="510104198805054321")
    DriverCredential.objects.create(driver=drv, cred_type="driving_license", side="main")
    DriverCredential.objects.create(driver=drv, cred_type="driving_license", side="back")
    # 姓名 + 身份证后6位
    resp = client.get("/api/v1/drivers/lookup?name=李四&id_tail=054321")
    assert resp.status_code == 200, resp.content
    body = resp.json()["data"]
    assert body["matched"] is True
    assert body["driver"]["name"] == "李四"
    assert len(body["credentials"]) == 2


@pytest.mark.django_db
def test_lookup_no_match(client):
    resp = client.get("/api/v1/drivers/lookup?name=王五&id_tail=999999")
    assert resp.json()["data"]["matched"] is False


def test_match_driver_helper(db):
    drv = Driver.objects.create(name="赵六", phone="13800003333", id_no="510104197703031111")
    from apps.masterdata.credential_ocr import match_driver

    assert match_driver(name="赵六", id_tail="031111").id == drv.id
    assert match_driver(name="赵六", id_tail="000000") is None
