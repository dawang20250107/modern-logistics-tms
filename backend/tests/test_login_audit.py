"""登录审计 + 失败锁定：每次登录留痕，连续失败达阈值锁定，审计台账 + 解锁。"""

import pytest
from django.core.cache import cache
from django.test import override_settings
from rest_framework.test import APIClient

from apps.iam.models import LoginAttempt

PW = "pw-strong-123456"


@pytest.fixture(autouse=True)
def _clear_cache():
    cache.clear()
    yield
    cache.clear()


@pytest.fixture
def user(db):
    from django.contrib.auth import get_user_model

    return get_user_model().objects.create_user(username="alice", password=PW)


def _login(client, username, password):
    return client.post(
        "/api/v1/auth/token", {"username": username, "password": password}, format="json"
    )


@pytest.mark.django_db
def test_successful_login_is_audited(user):
    c = APIClient()
    r = _login(c, "alice", PW)
    assert r.status_code == 200
    assert "access" in r.json()["data"]
    row = LoginAttempt.objects.get(username="alice")
    assert row.success is True
    assert row.result == LoginAttempt.RESULT_SUCCESS
    assert row.user_id == user.id


@pytest.mark.django_db
def test_bad_password_is_audited_as_failure(user):
    c = APIClient()
    r = _login(c, "alice", "wrong-pw")
    assert r.status_code == 401
    row = LoginAttempt.objects.get(username="alice")
    assert row.success is False
    assert row.result == LoginAttempt.RESULT_BAD_CREDENTIALS


@override_settings(LOGIN_MAX_FAILURES=3, LOGIN_LOCKOUT_MINUTES=15)
@pytest.mark.django_db
def test_lockout_after_repeated_failures(user):
    c = APIClient()
    # 前两次失败 → 仍是 401（未达阈值）
    assert _login(c, "alice", "x").status_code == 401
    assert _login(c, "alice", "x").status_code == 401
    # 第三次失败达阈值 → 锁定，返回 423
    r3 = _login(c, "alice", "x")
    assert r3.status_code == 423
    assert "锁定" in r3.json()["error"]["message"] or "锁定" in str(r3.json())
    # 锁定期内即便密码正确也拒绝
    r4 = _login(c, "alice", PW)
    assert r4.status_code == 423
    # 审计留下一条 locked 记录
    assert LoginAttempt.objects.filter(username="alice", result=LoginAttempt.RESULT_LOCKED).exists()


@override_settings(LOGIN_MAX_FAILURES=3)
@pytest.mark.django_db
def test_success_resets_failure_counter(user):
    c = APIClient()
    _login(c, "alice", "x")
    _login(c, "alice", "x")  # 两次失败，未锁
    assert _login(c, "alice", PW).status_code == 200  # 成功清零
    # 清零后又能连错两次而不被锁
    assert _login(c, "alice", "x").status_code == 401
    assert _login(c, "alice", "x").status_code == 401


@override_settings(LOGIN_MAX_FAILURES=3)
@pytest.mark.django_db
def test_inactive_account_classified(db):
    from django.contrib.auth import get_user_model

    get_user_model().objects.create_user(username="bob", password=PW, is_active=False)
    c = APIClient()
    r = _login(c, "bob", PW)
    assert r.status_code == 401
    row = LoginAttempt.objects.get(username="bob")
    assert row.result == LoginAttempt.RESULT_INACTIVE


@pytest.mark.django_db
def test_audit_endpoint_requires_permission(user):
    from django.contrib.auth import get_user_model

    # 普通用户无 org.view → 403
    c = APIClient()
    c.force_authenticate(user=user)
    assert c.get("/api/v1/org/login-audit").status_code == 403

    # 超管可看
    su = get_user_model().objects.create_superuser(username="root", password=PW)
    c2 = APIClient()
    c2.force_authenticate(user=su)
    _login(APIClient(), "alice", "x")  # 造一条失败流水
    r = c2.get("/api/v1/org/login-audit")
    assert r.status_code == 200
    assert r.json()["data"]["total"] >= 1


@override_settings(LOGIN_MAX_FAILURES=2)
@pytest.mark.django_db
def test_admin_unlock_restores_login(user):
    from django.contrib.auth import get_user_model

    c = APIClient()
    _login(c, "alice", "x")
    assert _login(c, "alice", "x").status_code == 423  # 锁定
    su = get_user_model().objects.create_superuser(username="root", password=PW)
    admin = APIClient()
    admin.force_authenticate(user=su)
    ur = admin.post("/api/v1/org/login-audit/unlock", {"username": "alice"}, format="json")
    assert ur.status_code == 200
    assert ur.json()["data"]["unlocked"] is True
    # 解锁后正确密码可登录
    assert _login(APIClient(), "alice", PW).status_code == 200


@pytest.mark.django_db
def test_unlock_requires_rbac_permission(user):
    # 普通用户无 org.rbac → 解锁 403
    c = APIClient()
    c.force_authenticate(user=user)
    assert c.post("/api/v1/org/login-audit/unlock", {"username": "alice"}, format="json").status_code == 403
