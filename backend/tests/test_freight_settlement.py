"""运费付款方式（现付/到付/回单付/月结）+ 代收货款(COD) 生命周期。"""

from decimal import Decimal

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.finance.models import ExpenseRecord
from apps.masterdata.models import Customer
from apps.ops.intake import convert_order_to_waybill, create_order_from_intake
from apps.ops.models import Order, Waybill
from apps.ops.services import collect_cod, driver_collection, remit_cod

User = get_user_model()


@pytest.fixture
def admin_client(db):
    User.objects.create_superuser(username="frt_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "frt_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


@pytest.mark.django_db
def test_freight_term_and_cod_flow_order_to_waybill():
    order = create_order_from_intake(fields={
        "origin": "上海", "destination": "北京",
        "freight_term": "collect", "freight_payer": "consignee", "cod_amount": "5000",
    })
    order.refresh_from_db()
    assert order.freight_term == Order.FREIGHT_COLLECT
    assert order.freight_payer == Order.PAYER_CONSIGNEE
    assert order.cod_amount == Decimal("5000")

    wb = convert_order_to_waybill(order)
    assert wb.freight_term == "collect"
    assert wb.freight_payer == "consignee"
    assert wb.cod_amount == Decimal("5000")
    # 有代收金额 → 运单落地即置「待代收」
    assert wb.cod_status == Order.COD_PENDING


@pytest.mark.django_db
def test_no_cod_stays_none():
    order = create_order_from_intake(fields={"origin": "A", "destination": "B", "freight_term": "prepaid"})
    wb = convert_order_to_waybill(order)
    assert wb.cod_status == Order.COD_NONE


@pytest.mark.django_db
def test_driver_collection_sums_collect_freight_and_cod():
    cust = Customer.objects.create(code="FRC1", name="到付客户")
    wb = Waybill.objects.create(
        waybill_no="FRC1W", route_name="r", customer=cust,
        freight_term="collect", cod_amount=Decimal("2000"), cod_status=Order.COD_PENDING,
    )
    # 应收运费 3000（到付时司机现场收）
    ExpenseRecord.objects.create(
        waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE,
        expense_item_code="TRANSPORT_INCOME", amount=Decimal("3000"),
    )
    result = driver_collection(wb)
    assert result["collect_freight"] == 3000.0
    assert result["cod_amount"] == 2000.0
    assert result["total_to_collect"] == 5000.0


@pytest.mark.django_db
def test_prepaid_driver_collects_only_cod():
    wb = Waybill.objects.create(
        waybill_no="FRC2W", route_name="r", freight_term="prepaid",
        cod_amount=Decimal("800"), cod_status=Order.COD_PENDING,
    )
    ExpenseRecord.objects.create(
        waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE,
        expense_item_code="TRANSPORT_INCOME", amount=Decimal("3000"),
    )
    result = driver_collection(wb)
    # 现付：运费不由司机收，只收代收货款
    assert result["collect_freight"] == 0.0
    assert result["total_to_collect"] == 800.0


@pytest.mark.django_db
def test_cod_collect_then_remit_lifecycle():
    wb = Waybill.objects.create(
        waybill_no="FRC3W", route_name="r", cod_amount=Decimal("1000"), cod_status=Order.COD_PENDING,
    )
    collect_cod(wb)
    wb.refresh_from_db()
    assert wb.cod_status == Order.COD_COLLECTED
    assert wb.cod_collected_at is not None
    assert wb.events.filter(event_type="cod_collected").exists()

    remit_cod(wb)
    wb.refresh_from_db()
    assert wb.cod_status == Order.COD_REMITTED
    assert wb.cod_remitted_at is not None
    assert wb.events.filter(event_type="cod_remitted").exists()


@pytest.mark.django_db
def test_remit_requires_collected_first():
    from apps.core.exceptions import AppError

    wb = Waybill.objects.create(
        waybill_no="FRC4W", route_name="r", cod_amount=Decimal("1000"), cod_status=Order.COD_PENDING,
    )
    with pytest.raises(AppError):
        remit_cod(wb)


@pytest.mark.django_db
def test_cod_endpoints(admin_client):
    wb = Waybill.objects.create(
        waybill_no="FRC5W", route_name="r", cod_amount=Decimal("1500"), cod_status=Order.COD_PENDING,
    )
    r = admin_client.get(f"/api/v1/waybills/{wb.waybill_no}/collection")
    assert r.status_code == 200
    assert r.json()["data"]["cod_amount"] == 1500.0

    r = admin_client.post(f"/api/v1/waybills/{wb.waybill_no}/collect-cod")
    assert r.status_code == 200
    assert r.json()["data"]["cod_status"] == "collected"

    r = admin_client.post(f"/api/v1/waybills/{wb.waybill_no}/remit-cod")
    assert r.status_code == 200
    assert r.json()["data"]["cod_status"] == "remitted"


@pytest.mark.django_db
def test_customer_credit_fields_exposed(admin_client):
    Customer.objects.create(code="FRC6", name="授信客户", credit_limit=Decimal("100000"), credit_days=45, billing_day=25)
    r = admin_client.get("/api/v1/customers?search=FRC6")
    item = next(i for i in r.json()["data"]["items"] if i["code"] == "FRC6")
    assert item["credit_limit"] == "100000.00"
    assert item["credit_days"] == 45
    assert item["billing_day"] == 25
