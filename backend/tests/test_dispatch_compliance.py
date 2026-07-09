"""调度合规：车厢结构匹配（冷链/危险品）、证件硬阻断、司机准驾校验。"""

from datetime import date, timedelta

import pytest

from apps.masterdata.models import Driver, Vehicle
from apps.ops.dispatch import (
    driver_qualification_issues,
    rank_vehicles,
    vehicle_fit,
    waybill_requirements,
)
from apps.ops.models import Order, Waybill

YESTERDAY = date.today() - timedelta(days=1)
NEXT_YEAR = date.today() + timedelta(days=365)


def _wb(**order_kw):
    order = Order.objects.create(order_no=f"OD{Order.objects.count()+1}", **order_kw)
    return Waybill.objects.create(waybill_no=f"WD{Waybill.objects.count()+1}", route_name="r", order=order,
                                  cargo_weight_ton=10, cargo_volume_cbm=30)


@pytest.mark.django_db
def test_coldchain_requires_reefer_vehicle():
    wb = _wb(business_type=Order.BIZ_COLDCHAIN, temperature_range="-18~0")
    reqs = waybill_requirements(wb)
    assert reqs["needs_reefer"] is True

    van = Vehicle.objects.create(plate_no="沪A0001", body_type=Vehicle.BODY_VAN, load_capacity_ton=30, volume_capacity_cbm=60)
    reefer = Vehicle.objects.create(plate_no="沪A0002", body_type=Vehicle.BODY_REEFER, load_capacity_ton=30, volume_capacity_cbm=60)
    # 厢车不能拉冷链 → 硬排除
    assert vehicle_fit(van, wb) is None
    assert vehicle_fit(reefer, wb) is not None


@pytest.mark.django_db
def test_hazmat_requires_hazmat_vehicle():
    wb = _wb(is_hazardous=True)
    assert waybill_requirements(wb)["is_hazmat"] is True
    stake = Vehicle.objects.create(plate_no="沪A0003", body_type=Vehicle.BODY_STAKE, load_capacity_ton=30, volume_capacity_cbm=60)
    hazmat = Vehicle.objects.create(plate_no="沪A0004", body_type=Vehicle.BODY_HAZMAT, load_capacity_ton=30, volume_capacity_cbm=60)
    assert vehicle_fit(stake, wb) is None
    assert vehicle_fit(hazmat, wb) is not None


@pytest.mark.django_db
def test_expired_vehicle_hard_blocked_from_ranking():
    wb = _wb()
    good = Vehicle.objects.create(plate_no="沪A0005", load_capacity_ton=30, volume_capacity_cbm=60, insurance_expiry=NEXT_YEAR)
    expired = Vehicle.objects.create(plate_no="沪A0006", load_capacity_ton=30, volume_capacity_cbm=60, insurance_expiry=YESTERDAY)
    plates = [r["plate_no"] for r in rank_vehicles(wb, vehicles=[good, expired])]
    assert "沪A0005" in plates
    assert "沪A0006" not in plates  # 证件过期硬阻断，不进推荐
    # include_blocked 时保留并带屏蔽原因
    with_blocked = rank_vehicles(wb, vehicles=[good, expired], include_blocked=True)
    blocked = next(r for r in with_blocked if r["plate_no"] == "沪A0006")
    assert blocked["blocked"] is True
    assert "证件过期" in blocked["block_reason"]


@pytest.mark.django_db
def test_driver_license_must_match_tractor():
    tractor = Vehicle.objects.create(plate_no="沪A0007", vehicle_class=Vehicle.CLASS_TRACTOR, load_capacity_ton=40)
    b2 = Driver.objects.create(name="B2司机", license_type="B2", license_expiry=NEXT_YEAR)
    a2 = Driver.objects.create(name="A2司机", license_type="A2", license_expiry=NEXT_YEAR)
    assert "准驾不足(牵引挂车需A2)" in driver_qualification_issues(b2, tractor)
    assert driver_qualification_issues(a2, tractor) == []


@pytest.mark.django_db
def test_driver_expired_license_and_hazmat_qualification():
    v = Vehicle.objects.create(plate_no="沪A0008", vehicle_class=Vehicle.CLASS_RIGID, load_capacity_ton=10)
    expired = Driver.objects.create(name="过期", license_type="B2", license_expiry=YESTERDAY)
    assert "驾照过期" in driver_qualification_issues(expired, v)
    # 危险品需危运从业资格
    no_qual = Driver.objects.create(name="无危运证", license_type="B2", license_expiry=NEXT_YEAR)
    assert "缺危运从业资格" in driver_qualification_issues(no_qual, v, is_hazmat=True)


@pytest.mark.django_db
def test_dispatch_order_blocks_expired_vehicle_and_bad_driver():
    from apps.core.exceptions import AppError
    from apps.ops.intake import create_order_from_intake, pool_order
    from apps.ops.order_dispatch import dispatch_order

    order = create_order_from_intake(fields={"origin": "A", "destination": "B", "cargo_weight_ton": 5})
    order.status = Order.STATUS_CONFIRMED
    order.save()
    pool_order(order)

    expired = Vehicle.objects.create(plate_no="沪A0009", load_capacity_ton=30, inspection_expiry=YESTERDAY)
    with pytest.raises(AppError) as exc:
        dispatch_order(order, dispatch_type="own_vehicle", vehicle=expired)
    assert exc.value.code == "VEHICLE_NON_COMPLIANT"
