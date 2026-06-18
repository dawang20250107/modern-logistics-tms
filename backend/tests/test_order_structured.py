"""企业级录单：多货物明细 / 多站点 / 自动报价 / 模板。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.finance.models import PricingRule
from apps.finance.services import estimate_order_quote
from apps.masterdata.models import Customer
from apps.ops.intake import create_order_from_intake, recompute_cargo_totals
from apps.ops.models import OrderCargoItem, OrderStop


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_recompute_cargo_totals_sums_items():
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    OrderCargoItem.objects.create(order=order, seq=1, name="钢材", quantity=10, weight_ton=5, volume_cbm=3)
    OrderCargoItem.objects.create(order=order, seq=2, name="木材", quantity=4, weight_ton=2, volume_cbm=6)
    recompute_cargo_totals(order)
    order.refresh_from_db()
    assert order.cargo_quantity == 14
    assert float(order.cargo_weight_ton) == 7.0
    assert float(order.cargo_volume_cbm) == 9.0


@pytest.mark.django_db
def test_estimate_order_quote_matches_rule():
    cust = Customer.objects.create(code="CQ1", name="比亚迪")
    PricingRule.objects.create(
        name="沪蓉整车", price_type=PricingRule.PRICE_TYPE_INCOME, expense_item_code="FREIGHT",
        customer=cust, base_price=1000, price_per_ton=100, priority=10,
    )
    q = estimate_order_quote(customer_id=cust.id, route_name="上海→成都", weight_ton=8)
    assert q["matched"] is True
    assert q["amount"] == 1800.0  # 1000 + 100*8
    assert q["rule_name"] == "沪蓉整车"


@pytest.mark.django_db
def test_quote_uses_volumetric_weight_for_bulky_cargo():
    cust = Customer.objects.create(code="CV1", name="泡货客户")
    PricingRule.objects.create(
        name="泡货线", price_type=PricingRule.PRICE_TYPE_INCOME, expense_item_code="FREIGHT",
        customer=cust, base_price=0, price_per_ton=100, priority=10,
    )
    # 实际 1 吨，但 30 方泡货 → 体积重 30*0.333≈10 吨，应按抛重计费
    q = estimate_order_quote(customer_id=cust.id, route_name="x", weight_ton=1, volume_cbm=30)
    assert q["by_volume"] is True
    assert q["chargeable_weight"] == pytest.approx(9.99, abs=0.01)
    assert q["amount"] == pytest.approx(999.0, abs=1.0)  # 100 * 9.99


@pytest.mark.django_db
def test_estimate_order_quote_no_match():
    q = estimate_order_quote(customer_id=None, route_name="x", weight_ton=5)
    assert q["matched"] is False
    assert q["amount"] == 0.0


@pytest.mark.django_db
def test_intake_with_cargo_items_and_draft(admin_client):
    resp = admin_client.post("/api/v1/orders/intake", {
        "channel": "cs",
        "status": "draft",
        "fields": {"origin": "上海", "destination": "成都", "business_type": "ftl"},
        "cargo_items": [
            {"name": "钢材", "quantity": 10, "weight_ton": 5, "volume_cbm": 3},
            {"name": "木材", "quantity": 4, "weight_ton": 2},
        ],
        "stops": [
            {"stop_type": "pickup", "city": "上海", "address": "A仓", "contact_phone": "13800001234"},
            {"stop_type": "delivery", "city": "成都", "address": "B仓"},
        ],
    }, format="json")
    assert resp.status_code == 201, resp.content
    data = resp.json()["data"]
    assert data["status"] == "draft"
    assert len(data["cargo_items"]) == 2
    assert len(data["stops"]) == 2
    assert float(data["cargo_weight_ton"]) == 7.0  # 汇总
    assert data["cargo_quantity"] == 14


@pytest.mark.django_db
def test_quote_endpoint(admin_client):
    cust = Customer.objects.create(code="CQ2", name="宁德时代")
    PricingRule.objects.create(
        name="沪蓉", price_type=PricingRule.PRICE_TYPE_INCOME, expense_item_code="FREIGHT",
        customer=cust, base_price=500, price_per_ton=200, priority=5,
    )
    resp = admin_client.post("/api/v1/orders/quote", {
        "customer": str(cust.id), "origin": "上海", "destination": "成都", "cargo_weight_ton": 10,
    }, format="json")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["amount"] == 2500.0  # 500 + 200*10


@pytest.mark.django_db
def test_edit_and_clone_order(admin_client):
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    OrderCargoItem.objects.create(order=order, seq=1, name="旧货", quantity=1, weight_ton=1)
    # 编辑：替换货物明细
    resp = admin_client.post(f"/api/v1/orders/{order.id}/edit", {
        "fields": {"priority": "urgent"},
        "cargo_items": [{"name": "新货", "quantity": 3, "weight_ton": 6}],
    }, format="json")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["priority"] == "urgent"
    assert len(data["cargo_items"]) == 1
    assert data["cargo_items"][0]["name"] == "新货"
    assert float(data["cargo_weight_ton"]) == 6.0
    # 复制建单 → 新草稿
    resp2 = admin_client.post(f"/api/v1/orders/{order.id}/clone")
    assert resp2.status_code == 201, resp2.content
    clone = resp2.json()["data"]
    assert clone["status"] == "draft"
    assert clone["order_no"] != order.order_no
    assert len(clone["cargo_items"]) == 1


@pytest.mark.django_db
def test_order_template_crud(admin_client):
    resp = admin_client.post("/api/v1/order-templates", {
        "name": "沪蓉整车模板",
        "payload": {"fields": {"origin": "上海", "destination": "成都", "business_type": "ftl"}},
    }, format="json")
    assert resp.status_code == 201, resp.content
    resp2 = admin_client.get("/api/v1/order-templates")
    assert resp2.status_code == 200
    assert resp2.json()["data"]["total"] >= 1


@pytest.mark.django_db
def test_export_csv(admin_client):
    create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    resp = admin_client.get("/api/v1/orders/export")
    assert resp.status_code == 200
    assert "text/csv" in resp["Content-Type"]
    assert "订单号" in resp.content.decode("utf-8")


@pytest.mark.django_db
def test_high_value_order_requires_approval():
    from apps.core.exceptions import AppError
    from apps.ops.intake import approve_order, pool_order
    from apps.ops.models import Order

    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都", "quoted_amount": "80000"})
    assert order.approval_status == Order.APPROVAL_PENDING  # 高价值自动进入待审批
    with pytest.raises(AppError) as exc:
        pool_order(order)
    assert exc.value.code == "ORDER_NEEDS_APPROVAL"
    # 审批通过后可进池
    approve_order(order, remark="同意")
    order.refresh_from_db()
    pooled = pool_order(order)
    assert pooled.status == Order.STATUS_POOLED


@pytest.mark.django_db
def test_reject_blocks_pool(admin_client):
    from apps.ops.models import Order

    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都", "cargo_value": "800000"})
    assert order.approval_status == Order.APPROVAL_PENDING
    resp = admin_client.post(f"/api/v1/orders/{order.id}/reject", {"remark": "超预算"}, format="json")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["approval_status"] == "rejected"
    r2 = admin_client.post(f"/api/v1/orders/{order.id}/pool")
    assert r2.status_code == 409


@pytest.mark.django_db
def test_normal_order_no_approval():
    from apps.ops.models import Order

    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都", "quoted_amount": "3000"})
    assert order.approval_status == Order.APPROVAL_NONE


@pytest.mark.django_db
def test_order_attachment_upload_list_delete(admin_client):
    from django.core.files.uploadedfile import SimpleUploadedFile

    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    f = SimpleUploadedFile("contract.txt", b"hello contract", content_type="text/plain")
    resp = admin_client.post(f"/api/v1/orders/{order.id}/attachments", {"kind": "contract", "file": f}, format="multipart")
    assert resp.status_code == 201, resp.content
    att_id = resp.json()["data"]["id"]

    lst = admin_client.get(f"/api/v1/orders/{order.id}/attachments")
    assert len(lst.json()["data"]) == 1
    assert lst.json()["data"][0]["kind"] == "contract"

    detail = admin_client.get(f"/api/v1/orders/{order.id}")
    assert len(detail.json()["data"]["attachments"]) == 1

    d = admin_client.delete(f"/api/v1/orders/{order.id}/attachments/{att_id}")
    assert d.status_code == 204
    assert order.attachments.count() == 0


@pytest.mark.django_db
def test_split_order_by_cargo_items(admin_client):
    from apps.ops.models import Order

    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    a = OrderCargoItem.objects.create(order=order, seq=1, name="钢材", quantity=10, weight_ton=5)
    b = OrderCargoItem.objects.create(order=order, seq=2, name="木材", quantity=4, weight_ton=2)
    resp = admin_client.post(f"/api/v1/orders/{order.id}/split", {
        "groups": [{"cargo_item_ids": [str(a.id)]}, {"cargo_item_ids": [str(b.id)]}],
    }, format="json")
    assert resp.status_code == 201, resp.content
    children = resp.json()["data"]
    assert len(children) == 2
    order.refresh_from_db()
    assert order.status == Order.STATUS_CANCELLED  # 原单作废
    weights = sorted(float(c["cargo_weight_ton"]) for c in children)
    assert weights == [2.0, 5.0]


@pytest.mark.django_db
def test_split_requires_two_items():
    from apps.core.exceptions import AppError
    from apps.ops.intake import split_order

    order = create_order_from_intake(fields={"origin": "A", "destination": "B"})
    OrderCargoItem.objects.create(order=order, seq=1, name="单件", quantity=1, weight_ton=1)
    with pytest.raises(AppError) as exc:
        split_order(order, [{"cargo_item_ids": []}, {"cargo_item_ids": []}])
    assert exc.value.code == "SPLIT_NEEDS_ITEMS"


@pytest.mark.django_db
def test_merge_orders(admin_client):
    from apps.ops.models import Order

    o1 = create_order_from_intake(fields={"origin": "上海", "destination": "成都", "quoted_amount": "1000"})
    OrderCargoItem.objects.create(order=o1, seq=1, name="货1", quantity=2, weight_ton=3)
    o2 = create_order_from_intake(fields={"origin": "上海", "destination": "成都", "quoted_amount": "2000"})
    OrderCargoItem.objects.create(order=o2, seq=1, name="货2", quantity=1, weight_ton=4)
    resp = admin_client.post("/api/v1/orders/merge", {"ids": [str(o1.id), str(o2.id)]}, format="json")
    assert resp.status_code == 201, resp.content
    merged = resp.json()["data"]
    assert float(merged["cargo_weight_ton"]) == 7.0
    assert len(merged["cargo_items"]) == 2
    assert float(merged["quoted_amount"]) == 3000.0
    o1.refresh_from_db()
    o2.refresh_from_db()
    assert o1.status == Order.STATUS_CANCELLED
    assert o2.status == Order.STATUS_CANCELLED


@pytest.mark.django_db
def test_customer_addresses_book(admin_client):
    cust = Customer.objects.create(code="CA1", name="海尔")
    o1 = create_order_from_intake(fields={"origin": "青岛", "destination": "上海"}, customer=cust)
    OrderStop.objects.create(order=o1, seq=1, stop_type=OrderStop.STOP_PICKUP, city="青岛", address="海尔工业园", contact_phone="13800001234")
    OrderStop.objects.create(order=o1, seq=2, stop_type=OrderStop.STOP_DELIVERY, city="上海", address="浦东仓")
    o2 = create_order_from_intake(fields={"origin": "青岛", "destination": "上海"}, customer=cust)
    OrderStop.objects.create(order=o2, seq=1, stop_type=OrderStop.STOP_PICKUP, city="青岛", address="海尔工业园")  # 重复去重

    resp = admin_client.get(f"/api/v1/orders/customer-addresses?customer={cust.id}")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert len(data["pickup"]) == 1  # 去重
    assert data["pickup"][0]["address"] == "海尔工业园"
    assert len(data["delivery"]) == 1


@pytest.mark.django_db
def test_order_detail_exposes_cargo_items_and_stops(admin_client):
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    OrderCargoItem.objects.create(order=order, seq=1, name="钢材", quantity=10, weight_ton=5)
    OrderStop.objects.create(order=order, seq=1, stop_type=OrderStop.STOP_PICKUP, city="上海", address="A仓")
    OrderStop.objects.create(order=order, seq=2, stop_type=OrderStop.STOP_DELIVERY, city="成都", address="B仓")
    resp = admin_client.get(f"/api/v1/orders/{order.id}")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert len(data["cargo_items"]) == 1
    assert data["cargo_items"][0]["name"] == "钢材"
    assert len(data["stops"]) == 2
    assert data["stops"][1]["stop_type"] == "delivery"


@pytest.mark.django_db
def test_order_edit_records_field_level_diff():
    from apps.ops.intake import update_order

    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都", "quoted_amount": "1000"})
    update_order(order, fields={"destination": "重庆", "quoted_amount": "1500"})
    ev = order.events.filter(event_type="updated").latest("event_time")
    changes = {c["field"]: c for c in ev.payload.get("changes", [])}
    assert changes["destination"]["from"] == "成都"
    assert changes["destination"]["to"] == "重庆"
    assert changes["destination"]["label"] == "目的地"
    assert float(changes["quoted_amount"]["to"]) == 1500.0
    # 未改的字段不应出现
    assert "origin" not in changes


@pytest.mark.django_db
def test_order_edit_flags_changed_collections():
    from apps.ops.intake import update_order

    order = create_order_from_intake(fields={"origin": "A", "destination": "B"})
    update_order(order, fields={}, stops=[{"seq": 1, "stop_type": "pickup", "city": "A"}])
    ev = order.events.filter(event_type="updated").latest("event_time")
    assert "站点" in ev.payload.get("changed_collections", [])
