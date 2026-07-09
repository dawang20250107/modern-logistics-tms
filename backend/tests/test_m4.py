"""M4 异常闭环与回单 OCR 测试。"""

import pytest
from django.contrib.auth import get_user_model
from django.core.files.uploadedfile import SimpleUploadedFile
from rest_framework.test import APIClient

from apps.finance.models import ExpenseRecord
from apps.ops.models import ExceptionRecord, Receipt, Waybill


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_exception_close_creates_payable(admin_client):
    wb = Waybill.objects.create(waybill_no="EX1", route_name="r")
    exc = ExceptionRecord.objects.create(waybill=wb, exception_type="damage")
    resp = admin_client.post(
        f"/api/v1/exceptions/{exc.id}/close",
        {"responsibility_party": "carrier", "amount": "300.00", "resolution": "赔付"},
        format="json",
    )
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["status"] == "closed"
    assert ExpenseRecord.objects.filter(waybill=wb, expense_item_code="EXCEPTION_COST").count() == 1


@pytest.mark.django_db
def test_receipt_upload_runs_ocr(admin_client):
    wb = Waybill.objects.create(waybill_no="RC1", route_name="r")
    upload = SimpleUploadedFile("pod.txt", b"signed pod", content_type="text/plain")
    resp = admin_client.post(
        "/api/v1/receipts", {"waybill": str(wb.id), "file": upload}, format="multipart"
    )
    assert resp.status_code == 201, resp.content
    receipt = Receipt.objects.get(id=resp.json()["data"]["id"])
    assert receipt.ocr_status == "manual"  # 无 OCR 引擎→待人工，不伪造签收人
