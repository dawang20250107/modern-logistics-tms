"""阶段三：结构化费用构成 + 上下游收款方归集。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.masterdata.models import Carrier
from apps.ops.models import Waybill


@pytest.fixture
def client(db):
    get_user_model().objects.create_superuser(username="fin_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "fin_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


@pytest.mark.django_db
def test_cost_catalog_lists_items(client):
    resp = client.get("/api/v1/waybills/cost-catalog")
    assert resp.status_code == 200
    data = resp.json()["data"]
    assert data["cost_items"]["FUEL_CARD"] == "油卡"
    assert data["payees"]["carrier"] == "承运商"


@pytest.mark.django_db
def test_add_structured_expenses_and_group_by_payee(client):
    carrier = Carrier.objects.create(code="C1", name="顺达物流")
    Waybill.objects.create(waybill_no="COST1", route_name="r", carrier=carrier)

    # 应付：运费给承运商 + 油卡给油卡商
    r1 = client.post("/api/v1/waybills/COST1/add-expense", {
        "direction": "payable", "expense_item_code": "TRANSPORT_COST", "amount": "5000",
        "payee_type": "carrier", "payee_ref": "顺达物流",
    }, format="json")
    assert r1.status_code == 201, r1.content
    client.post("/api/v1/waybills/COST1/add-expense", {
        "direction": "payable", "expense_item_code": "FUEL_CARD", "amount": "800",
        "payee_type": "fuel_card", "payee_ref": "中石化",
    }, format="json")
    # 应收：运费收入
    client.post("/api/v1/waybills/COST1/add-expense", {
        "direction": "receivable", "expense_item_code": "TRANSPORT_INCOME", "amount": "7000",
        "payee_type": "customer",
    }, format="json")

    costs = client.get("/api/v1/waybills/COST1/costs").json()["data"]
    assert costs["payable_total"] == 5800.0
    assert costs["receivable_total"] == 7000.0
    assert costs["gross_profit"] == 1200.0
    by = {p["payee_type"]: p["amount"] for p in costs["payables_by_payee"]}
    assert by == {"carrier": 5000.0, "fuel_card": 800.0}
    labels = {line["expense_item_code"]: line["item_label"] for line in costs["payables"]}
    assert labels["FUEL_CARD"] == "油卡"


@pytest.mark.django_db
def test_add_expense_rejects_bad_item(client):
    Waybill.objects.create(waybill_no="COST2", route_name="r")
    resp = client.post("/api/v1/waybills/COST2/add-expense", {
        "direction": "payable", "expense_item_code": "NOPE", "amount": "1",
    }, format="json")
    assert resp.status_code == 400
