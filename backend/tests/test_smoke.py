"""平台底座冒烟测试：健康探针、统一信封、JWT 登录闭环。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient


@pytest.fixture
def api():
    return APIClient()


def test_healthz(api):
    resp = api.get("/healthz")
    assert resp.status_code == 200
    assert resp.json()["status"] == "ok"


@pytest.mark.django_db
def test_unauthenticated_me_is_enveloped(api):
    resp = api.get("/api/v1/auth/me")
    assert resp.status_code == 401
    body = resp.json()
    assert body["success"] is False
    assert body["data"] is None
    assert body["error"]["code"]


@pytest.mark.django_db
def test_jwt_login_and_me(api):
    user_model = get_user_model()
    user_model.objects.create_user(username="alice", password="pw-strong-123", nickname="Alice")

    resp = api.post(
        "/api/v1/auth/token",
        {"username": "alice", "password": "pw-strong-123"},
        format="json",
    )
    assert resp.status_code == 200, resp.content
    payload = resp.json()
    assert payload["success"] is True
    access = payload["data"]["access"]

    api.credentials(HTTP_AUTHORIZATION=f"Bearer {access}")
    me = api.get("/api/v1/auth/me")
    assert me.status_code == 200
    assert me.json()["data"]["username"] == "alice"
