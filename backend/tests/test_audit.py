"""审计日志查询端点。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.audit.models import AuditLog

User = get_user_model()


def _client(user):
    c = APIClient()
    c.force_authenticate(user)
    return c


@pytest.mark.django_db
def test_audit_list_requires_admin():
    AuditLog.objects.create(action="agent_tool:x", resource_type="waybill", resource_id="W1", status_code=200)
    # 普通用户禁止
    normal = User.objects.create_user(username="u", password="x")
    resp = _client(normal).get("/api/v1/audit-logs")
    assert resp.status_code == 403
    # 管理员可查
    admin = User.objects.create_superuser(username="a", password="x")
    resp = _client(admin).get("/api/v1/audit-logs")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["total"] >= 1


@pytest.mark.django_db
def test_audit_filter_by_resource():
    AuditLog.objects.create(action="agent_tool:eta", resource_type="waybill", resource_id="W1")
    AuditLog.objects.create(action="login", resource_type="user", resource_id="U1")
    admin = User.objects.create_superuser(username="a", password="x")
    resp = _client(admin).get("/api/v1/audit-logs?resource_type=waybill")
    items = resp.json()["data"]["items"]
    assert all(i["resource_type"] == "waybill" for i in items)
