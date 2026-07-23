"""客服工作台：客户上下文 + 建单补全 + 客户回复卡。"""

from decimal import Decimal

import pytest

from apps.masterdata.models import Customer, Driver, Vehicle
from apps.ops.models import ExceptionRecord, Order, Waybill


def _customer():
    return Customer.objects.create(code="CU1", name="阿斯利康", settlement_type="monthly",
                                   credit_limit=Decimal("100000"), credit_days=30, billing_day=1)


def _order(cust, no, status, origin="上海", dest="杭州", cargo="医药冷藏箱", quoted=2000, delivery="杭州仓A"):
    return Order.objects.create(
        order_no=no, customer=cust, status=status, origin=origin, destination=dest,
        cargo_desc=cargo, quoted_amount=Decimal(str(quoted)),
        delivery_address=delivery, delivery_contact_name="李收", delivery_contact_phone="13900000000",
    )


@pytest.mark.django_db
def test_customer_context_aggregates_profile_routes_and_counts():
    cust = _customer()
    _order(cust, "O1", Order.STATUS_POOLED)          # 未完成
    _order(cust, "O2", Order.STATUS_CONFIRMED)       # 未完成
    _order(cust, "O3", Order.STATUS_CONVERTED)       # 已派单
    _order(cust, "O4", Order.STATUS_CANCELLED)       # 取消（应排除）

    # 回单未返运单
    Waybill.objects.create(waybill_no="W1", route_name="r", customer=cust, origin="上海", destination="杭州",
                           status=Waybill.STATUS_SIGNED, receipt_status="pending")

    from apps.ops.customer_ctx import customer_context

    ctx = customer_context(cust)
    assert ctx["counts"]["total"] == 3            # 取消单排除
    assert ctx["counts"]["open"] == 2
    assert ctx["counts"]["receipt_pending"] == 1
    assert "上海→杭州" in ctx["common_routes"]
    assert ctx["common_deliveries"][0]["address"] == "杭州仓A"
    assert ctx["profile"]["credit_days"] == 30


@pytest.mark.django_db
def test_customer_credit_outstanding_from_unsettled_receivables():
    from apps.finance.models import ExpenseRecord

    cust = _customer()
    wb = Waybill.objects.create(waybill_no="W2", route_name="r", customer=cust,
                                status=Waybill.STATUS_IN_TRANSIT)
    ExpenseRecord.objects.create(waybill=wb, direction="receivable", expense_item_code="freight", amount=Decimal("30000"))

    from apps.ops.customer_ctx import customer_context

    credit = customer_context(cust)["credit"]
    assert credit["outstanding"] == 30000.0
    assert credit["available"] == 70000.0
    assert credit["over_limit"] is False


@pytest.mark.django_db
def test_lane_suggest_returns_cargo_and_price_band():
    cust = _customer()
    _order(cust, "L1", Order.STATUS_CONVERTED, cargo="医药冷藏箱", quoted=1900)
    _order(cust, "L2", Order.STATUS_CONVERTED, cargo="医药冷藏箱", quoted=2100)

    from apps.ops.customer_ctx import lane_suggest

    s = lane_suggest(cust, "上海", "杭州")
    assert "医药冷藏箱" in s["common_cargo"]
    assert s["price_band"] == [1900, 2100]


@pytest.mark.django_db
def test_reply_card_builds_copyable_text():
    cust = _customer()
    drv = Driver.objects.create(name="王师傅", phone="13800138000")
    veh = Vehicle.objects.create(plate_no="沪C88992", load_capacity_ton=20)
    wb = Waybill.objects.create(
        waybill_no="W3", route_name="r", customer=cust, origin="上海", destination="杭州",
        status=Waybill.STATUS_IN_TRANSIT, receipt_status="pending", driver=drv, vehicle=veh,
    )
    ExceptionRecord.objects.create(waybill=wb, exception_type="transit_delay", level="medium")

    from apps.ops.customer_ctx import reply_card

    card = reply_card(wb)
    assert card["plate_no"] == "沪C88992"
    assert card["driver_phone"] == "13800138000"
    assert card["exception"] == "在途超时"
    assert "当前状态：" in card["copy_text"]
    assert "W3" in card["copy_text"]
