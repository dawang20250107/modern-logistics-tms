"""应收/应付账龄。"""

from datetime import datetime, timedelta
from decimal import Decimal

import pytest
from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework.test import APIClient

from apps.finance.models import ExpenseRecord
from apps.finance.services import aging_report
from apps.masterdata.models import Customer
from apps.ops.models import Waybill


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def _dt(days_ago):
    return timezone.make_aware(datetime.now() - timedelta(days=days_ago))


@pytest.mark.django_db
def test_aging_buckets():
    cust = Customer.objects.create(code="C1", name="客户甲")
    wb = Waybill.objects.create(waybill_no="WA1", route_name="r", customer=cust)
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="F", amount=Decimal("1000"), occurred_at=_dt(10))
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="F", amount=Decimal("500"), occurred_at=_dt(45))
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="F", amount=Decimal("300"), occurred_at=_dt(120))

    report = aging_report(ExpenseRecord.DIRECTION_RECEIVABLE)
    assert len(report["rows"]) == 1
    row = report["rows"][0]
    assert row["counterparty_name"] == "客户甲"
    assert row["b0_30"] == 1000.0
    assert row["b31_60"] == 500.0
    assert row["b90"] == 300.0
    assert row["total"] == 1800.0
    assert report["totals"]["total"] == 1800.0


@pytest.mark.django_db
def test_aging_endpoint(admin_client):
    cust = Customer.objects.create(code="C2", name="客户乙")
    wb = Waybill.objects.create(waybill_no="WA2", route_name="r", customer=cust)
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="F", amount=Decimal("800"), occurred_at=_dt(5))
    resp = admin_client.get("/api/v1/finance/aging?direction=receivable")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["totals"]["b0_30"] == 800.0
