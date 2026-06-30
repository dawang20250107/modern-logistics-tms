"""健壮性加固：司机端登录双因子 + 限流、轨迹上报输入校验、坐标容错。"""

import pytest
from rest_framework.test import APIClient

from apps.masterdata.models import Driver
from apps.ops.models import DriverCheckin, Waybill


@pytest.fixture
def driver(db):
    return Driver.objects.create(name="陈强", phone="13800009999", id_no="510104198501011234")


@pytest.mark.django_db
def test_login_requires_both_phone_and_id_tail(driver):
    c = APIClient()
    # 仅手机号 → 拒绝（关闭仅凭手机号登录的漏洞）
    r = c.post("/api/v1/driver/login", {"phone": driver.phone}, format="json")
    assert r.status_code == 400
    # 手机号对、id_tail 错 → 拒绝
    r = c.post("/api/v1/driver/login", {"phone": driver.phone, "id_tail": "000000"}, format="json")
    assert r.status_code == 401
    # 两者匹配 → 通过
    r = c.post("/api/v1/driver/login", {"phone": driver.phone, "id_tail": "011234"}, format="json")
    assert r.status_code == 200
    assert r.json()["data"]["token"]


@pytest.mark.django_db
def test_login_rejected_when_driver_has_no_id_no(db):
    drv = Driver.objects.create(name="无证司机", phone="13700001234", id_no="")
    c = APIClient()
    r = c.post("/api/v1/driver/login", {"phone": drv.phone, "id_tail": "123456"}, format="json")
    assert r.status_code == 401  # 无身份证号无法验证身份


@pytest.mark.django_db
def test_login_id_tail_format_validated(driver):
    c = APIClient()
    r = c.post("/api/v1/driver/login", {"phone": driver.phone, "id_tail": "12ab"}, format="json")
    assert r.status_code == 400


@pytest.mark.django_db
def test_tracking_ingest_caps_and_validates(admin_client):
    # 超量 → 413
    big = {"points": [{"waybill_no": "X", "lat": 31, "lng": 121}] * 1001}
    assert admin_client.post("/api/v1/tracking/points", big, format="json").status_code == 413
    # 非数组 → 400
    assert admin_client.post("/api/v1/tracking/points", {"points": "nope"}, format="json").status_code == 400
    # 非法坐标被丢弃，合法的入队
    mixed = {"points": [
        {"waybill_no": "A", "lat": 31.2, "lng": 121.4},
        {"waybill_no": "A", "lat": "bad", "lng": 121.4},
        {"waybill_no": "A", "lat": 999, "lng": 121.4},
        {"lat": 31, "lng": 121},  # 无 waybill_no
    ]}
    resp = admin_client.post("/api/v1/tracking/points", mixed, format="json")
    assert resp.status_code == 202
    assert resp.json()["data"]["queued"] == 1
    assert resp.json()["data"]["received"] == 4


@pytest.mark.django_db
def test_checkin_tolerates_bad_coords(driver):
    wb = Waybill.objects.create(waybill_no="HARDWB1", route_name="r", driver=driver,
                                status=Waybill.STATUS_DEPARTED)
    c = APIClient()
    tok = c.post("/api/v1/driver/login", {"phone": driver.phone, "id_tail": "011234"}, format="json").json()["data"]["token"]
    c.credentials(HTTP_X_DRIVER_TOKEN=tok)
    # 非法坐标不应 500，落库为 None
    r = c.post("/api/v1/driver/checkin", {"waybill_no": "HARDWB1", "node": "in_transit", "lat": "abc", "lng": ""}, format="multipart")
    assert r.status_code == 201, r.content
    chk = DriverCheckin.objects.get(waybill=wb, node="in_transit")
    assert chk.lat is None and chk.lng is None


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model

    get_user_model().objects.create_superuser(username="hard_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "hard_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c
