"""承运商风控：分级/黑名单/资质到期的派单硬阻断、比价过滤、端点鉴权。"""

from datetime import date, timedelta

import pytest
from django.core.cache import cache
from rest_framework.test import APIClient

from apps.iam.models import Permission, Role, RoleAssignment
from apps.masterdata.models import Carrier

YESTERDAY = date.today() - timedelta(days=1)
NEXT_YEAR = date.today() + timedelta(days=365)


@pytest.fixture(autouse=True)
def _clear_perm_cache():
    cache.clear()
    yield
    cache.clear()


def _carrier(**kw):
    n = Carrier.objects.count() + 1
    kw.setdefault("code", f"C{n}")
    kw.setdefault("name", f"承运商{n}")
    return Carrier.objects.create(**kw)


# ── 模型层：dispatch_block_reason 集中规则 ──────────────────────────


@pytest.mark.django_db
def test_dispatch_block_reason_rules():
    assert _carrier().dispatch_block_reason() == ""  # 正常承运商放行
    assert "黑名单" in _carrier(blacklisted=True, blacklist_reason="连续货损").dispatch_block_reason()
    assert "停用" in _carrier(is_active=False).dispatch_block_reason()
    assert "资质已" in _carrier(qualification_expiry=YESTERDAY).dispatch_block_reason()
    # 资质过期但关闭开关 → 放行
    assert _carrier(qualification_expiry=YESTERDAY).dispatch_block_reason(block_on_expired=False) == ""
    # 资质未过期 → 放行
    assert _carrier(qualification_expiry=NEXT_YEAR).dispatch_block_reason() == ""


# ── 派单硬阻断 ────────────────────────────────────────────────────


def _pooled_order():
    from apps.ops.intake import create_order_from_intake, pool_order
    from apps.ops.models import Order

    order = create_order_from_intake(fields={"origin": "A", "destination": "B", "cargo_weight_ton": 5})
    order.status = Order.STATUS_CONFIRMED
    order.save()
    pool_order(order)
    return order


@pytest.mark.django_db
def test_dispatch_blocks_blacklisted_carrier():
    from apps.core.exceptions import AppError
    from apps.ops.order_dispatch import dispatch_order

    order = _pooled_order()
    bad = _carrier(blacklisted=True, blacklist_reason="逃逸")
    with pytest.raises(AppError) as exc:
        dispatch_order(order, dispatch_type="third_party", carrier=bad)
    assert exc.value.code == "CARRIER_NOT_ALLOWED"


@pytest.mark.django_db
def test_dispatch_blocks_expired_qualification_carrier():
    from apps.core.exceptions import AppError
    from apps.ops.order_dispatch import dispatch_order

    order = _pooled_order()
    expired = _carrier(qualification_expiry=YESTERDAY)
    with pytest.raises(AppError) as exc:
        dispatch_order(order, dispatch_type="third_party", carrier=expired)
    assert exc.value.code == "CARRIER_NOT_ALLOWED"


@pytest.mark.django_db
def test_dispatch_allows_compliant_carrier():
    from apps.ops.order_dispatch import dispatch_order

    order = _pooled_order()
    good = _carrier(qualification_expiry=NEXT_YEAR)
    waybill = dispatch_order(order, dispatch_type="third_party", carrier=good)
    assert waybill.carrier_id == good.id


# ── 比价过滤：黑名单不进入建议 ────────────────────────────────────


@pytest.mark.django_db
def test_carrier_quotes_excludes_blacklisted():
    from apps.finance.models import PricingRule
    from apps.ops.dispatch import carrier_quotes
    from apps.ops.models import Order, Waybill

    good = _carrier(name="优质专线")
    bad = _carrier(name="黑名单专线", blacklisted=True, blacklist_reason="欺诈")
    # 全局适用的支出报价规则（carrier 为空 → 对所有承运商生效）
    PricingRule.objects.create(
        name="整车成本价", price_type=PricingRule.PRICE_TYPE_COST,
        charge_method=PricingRule.METHOD_FLAT, expense_item_code="freight",
        base_price=1000, is_active=True,
    )
    order = Order.objects.create(order_no="ODQ1")
    wb = Waybill.objects.create(waybill_no="WDQ1", route_name="r", order=order, cargo_weight_ton=8)

    names = [q["carrier"] for q in carrier_quotes(wb)]
    assert good.name in names
    assert bad.name not in names  # 黑名单承运商被过滤


# ── 端点鉴权：carrier.view / carrier.manage ───────────────────────


@pytest.fixture
def users(db):
    from django.contrib.auth import get_user_model

    User = get_user_model()
    return (
        User.objects.create_superuser(username="root", password="pw-strong-123456"),
        User.objects.create_user(username="nobody", password="pw-strong-123456"),
        User.objects.create_user(username="viewer", password="pw-strong-123456"),
    )


def _grant(user, *codes, scope="all"):
    role = Role.objects.create(code=f"r_{user.username}", name=user.username, data_scope=scope)
    perms = [Permission.objects.get_or_create(code=c, defaults={"name": c, "module": "承运商"})[0] for c in codes]
    role.permissions.set(perms)
    RoleAssignment.objects.create(user=user, role=role)


def _auth(user):
    c = APIClient()
    c.force_authenticate(user=user)
    return c


@pytest.mark.django_db
def test_carrier_endpoints_require_permission(users):
    root, plain, viewer = users
    carrier = _carrier()
    _grant(viewer, "carrier.view")

    # 无权限用户：读写皆 403
    cp = _auth(plain)
    assert cp.get("/api/v1/carriers").status_code == 403
    assert cp.post("/api/v1/carriers", {"code": "X", "name": "越权建"}, format="json").status_code == 403
    assert cp.post(f"/api/v1/carriers/{carrier.id}/blacklist",
                   {"blacklisted": True, "reason": "x"}, format="json").status_code == 403

    # 仅 carrier.view：可读，不可写/拉黑
    cv = _auth(viewer)
    assert cv.get("/api/v1/carriers").status_code == 200
    assert cv.post("/api/v1/carriers", {"code": "Y", "name": "只读越权"}, format="json").status_code == 403
    assert cv.post(f"/api/v1/carriers/{carrier.id}/blacklist",
                   {"blacklisted": True, "reason": "x"}, format="json").status_code == 403

    # 超管：畅通，拉黑生效
    cr = _auth(root)
    assert cr.get("/api/v1/carriers").status_code == 200
    r = cr.post(f"/api/v1/carriers/{carrier.id}/blacklist",
                {"blacklisted": True, "reason": "连续违约"}, format="json")
    assert r.status_code == 200
    carrier.refresh_from_db()
    assert carrier.blacklisted is True and carrier.blacklist_reason == "连续违约"


@pytest.mark.django_db
def test_blacklist_requires_reason(users):
    root, _, _ = users
    carrier = _carrier()
    r = _auth(root).post(f"/api/v1/carriers/{carrier.id}/blacklist", {"blacklisted": True}, format="json")
    assert r.status_code == 400  # 拉黑必须填原因


# ── 到期预警面板纳入承运资质 ──────────────────────────────────────


@pytest.mark.django_db
def test_expiring_credentials_includes_carrier():
    from django.contrib.auth import get_user_model

    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")

    _carrier(name="资质将到期", qualification_expiry=date.today() + timedelta(days=5))
    data = client.get("/api/v1/credentials/expiring?days=30").json()["data"]
    assert "carriers" in data
    assert any(c["credential"] == "承运资质" for c in data["carriers"])
    assert data["summary"]["total"] >= 1
