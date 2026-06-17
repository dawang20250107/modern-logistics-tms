"""智能调度：可用运力匹配、装载适配评分、多承运商比价、批量排线（建议态）。

原则：调度是高风险动作，本模块只产出**建议/计划**，不自动派车（由人工经
现有 /waybills/<no>/dispatch 落地）。装载适配与比价为纯规则，便于测试。
"""

from django.db.models import Q

from .models import Waybill

# 占用运力的运单状态（已派车至在途，视为不可再分配）
_BUSY_STATUSES = [
    Waybill.STATUS_DISPATCHED,
    Waybill.STATUS_LOADED,
    Waybill.STATUS_DEPARTED,
    Waybill.STATUS_IN_TRANSIT,
]


def _busy_ids(field: str) -> set:
    return set(
        Waybill.objects.filter(status__in=_BUSY_STATUSES)
        .exclude(**{f"{field}__isnull": True})
        .values_list(field, flat=True)
    )


def available_vehicles():
    from apps.masterdata.models import Vehicle

    return Vehicle.objects.filter(is_active=True).exclude(id__in=_busy_ids("vehicle_id"))


def available_drivers():
    from apps.masterdata.models import Driver

    return Driver.objects.filter(is_active=True).exclude(id__in=_busy_ids("driver_id"))


def vehicle_fit(vehicle, waybill) -> dict | None:
    """车辆对运单的装载适配；不满足核载/容积返回 None，否则返回评分（slack 越小越优）。"""
    cap_t = float(vehicle.load_capacity_ton or 0)
    cap_v = float(vehicle.volume_capacity_cbm or 0)
    need_t = float(waybill.cargo_weight_ton or 0)
    need_v = float(waybill.cargo_volume_cbm or 0)
    if cap_t and need_t > cap_t:
        return None
    if cap_v and need_v > cap_v:
        return None
    # 余量（运力未知按 0 处理，排在后面）
    slack = (cap_t - need_t if cap_t else 1e9) + (cap_v - need_v if cap_v else 1e9)
    util = (need_t / cap_t) if cap_t else 0.0
    return {"plate_no": vehicle.plate_no, "slack": round(slack, 2), "utilization": round(util, 3)}


def rank_vehicles(waybill, vehicles=None) -> list[dict]:
    vehicles = list(available_vehicles()) if vehicles is None else vehicles
    scored = []
    for v in vehicles:
        fit = vehicle_fit(v, waybill)
        if fit is not None:
            fit["vehicle_id"] = str(v.id)
            scored.append((v, fit))
    scored.sort(key=lambda x: x[1]["slack"])  # 紧凑装载优先
    return [fit for _v, fit in scored]


def carrier_quotes(waybill) -> list[dict]:
    """多承运商比价：每个承运商取最低适用支出报价，按价升序（价低者得）。"""
    from apps.finance.models import PricingRule
    from apps.masterdata.models import Carrier

    weight = waybill.cargo_weight_ton
    quotes = []
    for carrier in Carrier.objects.filter(is_active=True):
        rules = PricingRule.objects.filter(
            is_active=True, price_type=PricingRule.PRICE_TYPE_COST
        ).filter(Q(carrier=carrier) | Q(carrier__isnull=True))
        prices = [rule.quote(weight) for rule in rules]
        if not prices:
            continue
        quotes.append({"carrier": carrier.name, "carrier_id": str(carrier.id), "quote": float(min(prices))})
    quotes.sort(key=lambda q: q["quote"])
    return quotes


def recommend_dispatch(waybill, *, top: int = 3) -> dict:
    """为单张运单产出调度建议：车辆候选 + 司机候选 + 承运商比价。"""
    vehicles = rank_vehicles(waybill)[:top]
    drivers = [{"driver_id": str(d.id), "name": d.name} for d in available_drivers()[:top]]
    quotes = carrier_quotes(waybill)
    return {
        "waybill_no": waybill.waybill_no,
        "cargo": {"weight_ton": float(waybill.cargo_weight_ton), "volume_cbm": float(waybill.cargo_volume_cbm)},
        "vehicle_candidates": vehicles,
        "driver_candidates": drivers,
        "carrier_quotes": quotes,
        "best_vehicle": vehicles[0] if vehicles else None,
        "best_carrier": quotes[0] if quotes else None,
    }


def plan_dispatch(waybills: list) -> dict:
    """批量排线（贪心）：按货量从大到小，把每张运单分配给最紧凑适配且未占用的车辆。"""
    vehicles = list(available_vehicles())
    used: set = set()
    assignments, unassigned = [], []
    ordered = sorted(waybills, key=lambda w: float(w.cargo_weight_ton or 0), reverse=True)
    for waybill in ordered:
        candidates = [v for v in vehicles if v.id not in used]
        ranked = rank_vehicles(waybill, candidates)
        if ranked:
            pick = ranked[0]
            used.add(_uuid(pick["vehicle_id"]))
            assignments.append({"waybill_no": waybill.waybill_no, "vehicle": pick})
        else:
            unassigned.append(waybill.waybill_no)
    return {"assignments": assignments, "unassigned": unassigned, "assigned_count": len(assignments)}


def _uuid(value):
    import uuid

    return uuid.UUID(value) if isinstance(value, str) else value
