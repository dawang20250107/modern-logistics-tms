"""安全加固测试：RBAC 数据域 + 权限点、HMAC 验签/防重放、幂等。"""

import hashlib
import hmac
import time

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.iam.models import ApiKey, Organization, Permission, Role, RoleAssignment
from apps.ops.models import ExceptionRecord, Waybill


@pytest.fixture
def api():
    return APIClient()


def _login(api, username, password):
    resp = api.post("/api/v1/auth/token", {"username": username, "password": password}, format="json")
    assert resp.status_code == 200, resp.content
    return resp.json()["data"]["access"]


@pytest.mark.django_db
def test_rbac_scope_and_permission(api):
    user_model = get_user_model()
    org_sh = Organization.objects.create(code="SH001", name="上海网点", type="station")
    org_cd = Organization.objects.create(code="CD001", name="成都网点", type="station")
    p_view = Permission.objects.create(code="waybill.view", name="查看")
    p_manage = Permission.objects.create(code="waybill.manage", name="管理")

    viewer_role = Role.objects.create(code="viewer", name="只读", data_scope="org")
    viewer_role.permissions.add(p_view)
    disp_role = Role.objects.create(code="dispatcher", name="调度", data_scope="org")
    disp_role.permissions.add(p_view, p_manage)

    Waybill.objects.create(waybill_no="SH1", route_name="r", organization=org_sh)
    Waybill.objects.create(waybill_no="CD1", route_name="r", organization=org_cd)

    viewer = user_model.objects.create_user(username="v", password="pw-strong-123", organization=org_sh)
    RoleAssignment.objects.create(user=viewer, role=viewer_role, organization=org_sh)
    dispatcher = user_model.objects.create_user(username="d", password="pw-strong-123", organization=org_sh)
    RoleAssignment.objects.create(user=dispatcher, role=disp_role, organization=org_sh)

    # 只读用户：只看到本组织(SH)运单
    api.credentials(HTTP_AUTHORIZATION=f"Bearer {_login(api, 'v', 'pw-strong-123')}")
    resp = api.get("/api/v1/waybills")
    assert resp.status_code == 200
    assert [w["waybill_no"] for w in resp.json()["data"]["items"]] == ["SH1"]

    # 只读用户：无 manage 权限，创建被拒
    resp = api.post("/api/v1/waybills", {"waybill_no": "X", "route_name": "r"}, format="json")
    assert resp.status_code == 403

    # 调度员：有 manage 权限，可创建
    api.credentials(HTTP_AUTHORIZATION=f"Bearer {_login(api, 'd', 'pw-strong-123')}")
    resp = api.post(
        "/api/v1/waybills",
        {"waybill_no": "SH2", "route_name": "r", "organization": str(org_sh.id)},
        format="json",
    )
    assert resp.status_code == 201, resp.content


@pytest.mark.django_db
def test_hmac_authentication(api):
    ApiKey.objects.create(name="ext", key_id="k1", secret="s3cr3t", scopes="*")
    path = "/api/v1/customers"
    ts = str(int(time.time()))
    canonical = f"GET\n{path}\n{ts}\n{hashlib.sha256(b'').hexdigest()}"
    good = hmac.new(b"s3cr3t", canonical.encode(), hashlib.sha256).hexdigest()

    resp = api.get(path, HTTP_X_API_KEY="k1", HTTP_X_TIMESTAMP=ts, HTTP_X_SIGNATURE=good)
    assert resp.status_code == 200, resp.content

    resp = api.get(path, HTTP_X_API_KEY="k1", HTTP_X_TIMESTAMP=ts, HTTP_X_SIGNATURE="bad")
    assert resp.status_code == 401

    old = str(int(time.time()) - 1000)
    stale_canonical = f"GET\n{path}\n{old}\n{hashlib.sha256(b'').hexdigest()}"
    stale = hmac.new(b"s3cr3t", stale_canonical.encode(), hashlib.sha256).hexdigest()
    resp = api.get(path, HTTP_X_API_KEY="k1", HTTP_X_TIMESTAMP=old, HTTP_X_SIGNATURE=stale)
    assert resp.status_code == 401


@pytest.mark.django_db
def test_idempotency_replay(api):
    user_model = get_user_model()
    user_model.objects.create_superuser(username="a", password="pw-strong-123")
    api.credentials(HTTP_AUTHORIZATION=f"Bearer {_login(api, 'a', 'pw-strong-123')}")

    payload = {"exception_type": "transit_delay", "description": "d"}
    first = api.post("/api/v1/exceptions", payload, format="json", HTTP_IDEMPOTENCY_KEY="key-1")
    assert first.status_code == 201
    second = api.post("/api/v1/exceptions", payload, format="json", HTTP_IDEMPOTENCY_KEY="key-1")
    assert second.status_code == 201
    assert second.headers.get("Idempotent-Replay") == "true"
    assert ExceptionRecord.objects.count() == 1
