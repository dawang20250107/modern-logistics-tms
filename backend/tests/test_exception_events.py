"""异常处置事件溯源（ExceptionEvent）+ ai-resolve 修复验证。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.ops.models import ExceptionEvent, ExceptionRecord, Waybill

User = get_user_model()


@pytest.fixture
def admin_client(db):
    User.objects.create_superuser(username="exev_admin", password="pw-strong-123456")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "exev_admin", "password": "pw-strong-123456"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_create_assign_handle_close_recorded_as_events(admin_client):
    wb = Waybill.objects.create(waybill_no="EXEV1", route_name="r")
    resp = admin_client.post(
        "/api/v1/exceptions",
        {"waybill": str(wb.id), "exception_type": "transit_delay", "level": "high", "description": "高速拥堵"},
        format="json",
    )
    assert resp.status_code == 201
    exc_id = resp.json()["data"]["id"]
    exc = ExceptionRecord.objects.get(id=exc_id)
    assert ExceptionEvent.objects.filter(exception=exc, event_type="create").exists()

    admin_user = User.objects.get(username="exev_admin")
    resp = admin_client.post(f"/api/v1/exceptions/{exc_id}/assign", {"assignee": str(admin_user.id)}, format="json")
    assert resp.status_code == 200
    assign_evt = ExceptionEvent.objects.get(exception=exc, event_type="assign")
    assert assign_evt.from_status == "pending_handle"
    assert assign_evt.to_status == "handling"
    assert assign_evt.actor_id == admin_user.id

    resp = admin_client.post(f"/api/v1/exceptions/{exc_id}/handle", {"resolution": "已联系司机绕行"}, format="json")
    assert resp.status_code == 200
    assert ExceptionEvent.objects.filter(exception=exc, event_type="handle", note="已联系司机绕行").exists()

    resp = admin_client.post(
        f"/api/v1/exceptions/{exc_id}/close",
        {"responsibility_party": "carrier", "amount": "100", "resolution": "已赔付"},
        format="json",
    )
    assert resp.status_code == 200
    close_evt = ExceptionEvent.objects.get(exception=exc, event_type="close")
    assert close_evt.to_status == "closed"
    assert close_evt.payload["responsibility_party"] == "carrier"

    # 全过程时间线可查询，按发生时间顺序
    timeline = admin_client.get(f"/api/v1/exceptions/{exc_id}/timeline")
    assert timeline.status_code == 200
    types = [e["event_type"] for e in timeline.json()["data"]]
    assert types == ["create", "assign", "handle", "close"]


@pytest.mark.django_db
def test_ai_resolve_works_on_freshly_created_pending_exception(admin_client, monkeypatch):
    """回归测试：此前 ai-resolve 因引用不存在的 execute_agent_sync 与错误的状态常量
    （STATUS_PENDING_HANDLE 不存在，应为 STATUS_PENDING）而对刚创建的异常 100% 报错。"""

    def fake_run_agent(prompt, thread_id=None):
        assert "运输异常" in prompt
        return {"answer": "建议：立即联系司机核实路况，30 分钟内回传定位。", "tool_calls": [], "suggestions": []}

    monkeypatch.setattr(
        "apps.ai.services.agent_runner.run_agent", fake_run_agent
    )

    wb = Waybill.objects.create(waybill_no="EXEV2", route_name="r")
    exc = ExceptionRecord.objects.create(
        waybill=wb, exception_type="transit_delay", level="high", description="超时未到",
        status=ExceptionRecord.STATUS_PENDING,
    )
    assert exc.status == "pending_handle"

    resp = admin_client.post(f"/api/v1/exceptions/{exc.id}/ai-resolve")
    assert resp.status_code == 200, resp.content
    body = resp.json()["data"]
    assert body["status"] == "handling"
    assert "立即联系司机" in body["ai_resolution"]

    exc.refresh_from_db()
    assert exc.status == ExceptionRecord.STATUS_HANDLING
    assert "立即联系司机" in exc.resolution
    evt = ExceptionEvent.objects.get(exception=exc, event_type="ai_resolve")
    assert evt.from_status == "pending_handle"
    assert evt.to_status == "handling"
