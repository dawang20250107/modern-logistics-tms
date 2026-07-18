"""智能调度：可用运力匹配、装载适配评分、多承运商比价、批量排线（建议态）。

原则：调度是高风险动作，本模块只产出**建议/计划**，不自动派车（由人工经
现有 /waybills/<no>/dispatch 落地）。装载适配与比价为纯规则，便于测试。
"""

from django.db.models import Q
from django.utils import timezone

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


def vehicle_compliance_issues(vehicle) -> list[str]:
    """返回车辆已过期的证件（年检/保险/维保）标签；空表示合规。"""
    today = timezone.localdate()
    issues = []
    for field, label in [("inspection_expiry", "年检"), ("insurance_expiry", "保险"), ("maintenance_due_date", "维保")]:
        expiry = getattr(vehicle, field, None)
        if expiry and expiry < today:
            issues.append(label)
    return issues


def waybill_requirements(obj) -> dict:
    """从运单或订单推导对车辆的硬性要求：冷链需冷藏车、危险品需危运/罐车。

    兼容传入 Order（自身带 is_hazardous 等）或 Waybill（要素在 waybill.order 上）。
    """
    src = obj if hasattr(obj, "is_hazardous") else getattr(obj, "order", None)
    needs_reefer = is_hazmat = False
    if src is not None:
        is_hazmat = bool(getattr(src, "is_hazardous", False))
        biz_cold = getattr(src, "BIZ_COLDCHAIN", "coldchain")
        needs_reefer = bool(getattr(src, "temperature_range", "")) or getattr(src, "business_type", "") == biz_cold
    return {"needs_reefer": needs_reefer, "is_hazmat": is_hazmat}


def body_type_mismatch(vehicle, reqs) -> str:
    """车厢结构是否满足货物要求；返回不匹配原因，空串表示匹配。"""
    from apps.masterdata.models import Vehicle

    if reqs.get("needs_reefer") and vehicle.body_type != Vehicle.BODY_REEFER:
        return "冷链货需冷藏车"
    if reqs.get("is_hazmat") and vehicle.body_type not in (Vehicle.BODY_HAZMAT, Vehicle.BODY_TANK):
        return "危险品需危运/罐式车"
    return ""


def driver_qualification_issues(driver, vehicle=None, is_hazmat=False) -> list[str]:
    """司机资质硬校验：准驾车型匹配车辆、驾照/从业资格有效、危运资格。空表示合规。"""
    from apps.masterdata.models import Vehicle

    today = timezone.localdate()
    issues = []
    lt = (driver.license_type or "").upper()
    # 牵引车/挂车须 A2 准驾（空或不符均拦截）；普通货车不因未登记准驾而阻断
    needs_a2 = vehicle is not None and vehicle.vehicle_class in (Vehicle.CLASS_TRACTOR, Vehicle.CLASS_TRAILER)
    if needs_a2 and "A2" not in lt:
        issues.append("准驾不足(牵引挂车需A2)")
    if driver.license_expiry and driver.license_expiry < today:
        issues.append("驾照过期")
    if is_hazmat:
        if not driver.qualification_cert_no:
            issues.append("缺危运从业资格")
        elif driver.qualification_expiry and driver.qualification_expiry < today:
            issues.append("危运从业资格过期")
    return issues


def vehicle_fit(vehicle, waybill, reqs=None) -> dict | None:
    """车辆对运单的适配；不满足核载/容积/车厢结构返回 None（硬性排除），
    否则返回评分（slack 越小越优）+ 合规屏蔽标记（证件过期默认硬阻断）。"""
    from django.conf import settings

    cap_t = float(vehicle.load_capacity_ton or 0)
    cap_v = float(vehicle.volume_capacity_cbm or 0)
    need_t = float(waybill.cargo_weight_ton or 0)
    need_v = float(waybill.cargo_volume_cbm or 0)
    if cap_t and need_t > cap_t:
        return None
    if cap_v and need_v > cap_v:
        return None
    reqs = reqs if reqs is not None else waybill_requirements(waybill)
    if body_type_mismatch(vehicle, reqs):
        return None  # 车厢结构不满足（冷链/危险品）——硬性排除，不派敞车拉冷链/危货
    slack = (cap_t - need_t if cap_t else 1e9) + (cap_v - need_v if cap_v else 1e9)
    util = (need_t / cap_t) if cap_t else 0.0
    compliance = vehicle_compliance_issues(vehicle)
    block = bool(compliance) and getattr(settings, "DISPATCH_BLOCK_ON_EXPIRED", True)
    return {
        "plate_no": vehicle.plate_no,
        "body_type": vehicle.body_type,
        "vehicle_length_m": float(vehicle.vehicle_length_m or 0),
        "slack": round(slack, 2),
        "utilization": round(util, 3),
        "compliance": compliance,
        "compliance_ok": not compliance,
        "blocked": block,
        "block_reason": ("证件过期屏蔽：" + "/".join(compliance)) if block else "",
    }


def rank_vehicles(waybill, vehicles=None, *, include_blocked=False) -> list[dict]:
    """按适配度排名可派车辆。默认剔除证件过期被屏蔽的车（硬阻断），
    include_blocked=True 时保留并标记，供前端展示屏蔽原因。"""
    vehicles = list(available_vehicles()) if vehicles is None else vehicles
    reqs = waybill_requirements(waybill)
    scored = []
    for v in vehicles:
        fit = vehicle_fit(v, waybill, reqs=reqs)
        if fit is None:
            continue
        if fit["blocked"] and not include_blocked:
            continue
        fit["vehicle_id"] = str(v.id)
        scored.append((v, fit))
    # 合规车辆优先，其次紧凑装载优先
    scored.sort(key=lambda x: (not x[1]["compliance_ok"], x[1]["slack"]))
    return [fit for _v, fit in scored]


def carrier_quotes(waybill) -> list[dict]:
    """多承运商比价：每个承运商取最低适用支出报价，按价升序（价低者得）。"""
    from apps.finance.models import PricingRule
    from apps.masterdata.models import Carrier

    weight = waybill.cargo_weight_ton
    quotes = []
    # 风控：黑名单承运商不进入比价建议（避免推荐后又在派单时被硬阻断）
    for carrier in Carrier.objects.filter(is_active=True, blacklisted=False):
        rules = PricingRule.objects.filter(
            is_active=True, price_type=PricingRule.PRICE_TYPE_COST
        ).filter(Q(carrier=carrier) | Q(carrier__isnull=True))
        prices = [rule.quote(weight).get("amount", 0) for rule in rules]
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


def consolidate_and_group_orders(orders) -> dict:
    """智能 B2B 拼单配载与最省算路算法 (Intelligent Consolidation Engine)

    1. 将所有待调度订单按相同 始发城市 -> 目的城市 归类（共线同向）
    2. 计算同向订单的货量总和（重量/体积）
    3. 配载匹配：尝试将同向的多个小单（LTL）合拼成一辆 FTL 大卡车的运力容积中
    4. 降本测算：自动对比“分单分派”与“合单整车”的运价，算出拼单节省金额（基于 AI 智能运价估算）
    """
    import uuid
    from decimal import Decimal

    from apps.integrations.ymm import freight_quote

    vehicles = list(available_vehicles())
    used_vehicles = set()
    
    # 按 始发城市->目的城市 归组
    groups = {}
    for o in orders:
        if o.status not in ("pooled", "dispatching"):
            continue
        key = f"{o.origin or '未知'}→{o.destination or '未知'}"
        if key not in groups:
            groups[key] = []
        groups[key].append(o)

    consolidated_trips = []
    unassigned = []

    for route, grp_orders in groups.items():
        parts = route.split("→")
        origin, destination = parts[0], parts[1]
        
        # 1. 尝试拼单合并（按货量从大到小，配载拼装）
        ordered = sorted(grp_orders, key=lambda x: float(x.cargo_weight_ton or 0), reverse=True)
        
        while ordered:
            current_trip_orders = []
            cur_weight = 0.0
            cur_volume = 0.0
            
            # 找到一辆最适合这条线路的空闲车来进行配载
            temp_order_mock = type('MockOrder', (object,), {
                'cargo_weight_ton': Decimal("15.00"),  # 模拟大重货寻找最大容积卡车
                'cargo_volume_cbm': Decimal("40.00")
            })()
            candidates = [v for v in vehicles if v.id not in used_vehicles]
            ranked = rank_vehicles(temp_order_mock, candidates)
            
            if not ranked:
                # 如果没有可用空闲车，所有剩余订单均列为未分派
                for o in ordered:
                    unassigned.append({"order_id": str(o.id), "order_no": o.order_no, "route": route})
                break
                
            # 选择排在第一的最佳运力，获取其极限载重
            best_v_fit = ranked[0]
            v_id = best_v_fit["vehicle_id"]
            plate_no = best_v_fit["plate_no"]
            
            # 获取数据库对象
            from apps.masterdata.models import Vehicle
            db_vehicle = Vehicle.objects.get(id=uuid.UUID(v_id) if isinstance(v_id, str) else v_id)
            cap_t = float(db_vehicle.load_capacity_ton or 0)
            cap_v = float(db_vehicle.volume_capacity_cbm or 0)
            
            # 贪心背包拼装 (Knapsack-like Greedy Packing)
            remaining_ordered = []
            for o in ordered:
                w = float(o.cargo_weight_ton or 0)
                v = float(o.cargo_volume_cbm or 0)
                
                # 判断加入这笔订单后，是否会超载或超容
                if cur_weight + w <= cap_t and cur_volume + v <= cap_v:
                    current_trip_orders.append(o)
                    cur_weight += w
                    cur_volume += v
                else:
                    remaining_ordered.append(o)
            
            # 如果拼成了
            if current_trip_orders:
                used_vehicles.add(db_vehicle.id)
                
                # 3. 测算合单后一辆大车运送的总成本
                consolidated_quote = freight_quote(origin, destination, weight_ton=cur_weight, volume_cbm=cur_volume)
                consolidated_cost = float(consolidated_quote.get("avg") or 0)
                
                # 4. 计算这些合并订单的单独发运总成本
                sep_cost = 0.0
                for o in current_trip_orders:
                    quote = freight_quote(o.origin, o.destination, weight_ton=float(o.cargo_weight_ton), volume_cbm=float(o.cargo_volume_cbm))
                    sep_cost += float(quote.get("avg") or 0)
                    
                saved = round(sep_cost - consolidated_cost, 2)
                
                consolidated_trips.append({
                    "route": route,
                    "origin": origin,
                    "destination": destination,
                    "orders": [
                        {
                            "order_id": str(o.id), "order_no": o.order_no,
                            "weight_ton": float(o.cargo_weight_ton), "volume_cbm": float(o.cargo_volume_cbm),
                            "customer_name": o.customer.name if o.customer else "散客"
                        } for o in current_trip_orders
                    ],
                    "total_weight_ton": round(cur_weight, 2),
                    "total_volume_cbm": round(cur_volume, 2),
                    "vehicle": {
                        "id": str(db_vehicle.id),
                        "plate_no": plate_no,
                        "load_capacity_ton": cap_t,
                        "volume_capacity_cbm": cap_v
                    },
                    "separate_cost": round(sep_cost, 2),
                    "consolidated_cost": round(consolidated_cost, 2),
                    "money_saved": max(0.0, saved)
                })
                ordered = remaining_ordered
            else:
                # 极端单件就超大，单车装不下的极端情况
                o = ordered[0]
                unassigned.append({"order_id": str(o.id), "order_no": o.order_no, "route": route})
                ordered = ordered[1:]

    # === 兼容性扁平映射 (Backward Compatibility Mapping) ===
    flat_assignments = []
    for trip in consolidated_trips:
        for order in trip["orders"]:
            flat_assignments.append({
                "order_id": order["order_id"],
                "order_no": order["order_no"],
                "route": trip["route"],
                "weight_ton": order["weight_ton"],
                "vehicle": {
                    "vehicle_id": trip["vehicle"]["id"],
                    "plate_no": trip["vehicle"]["plate_no"],
                    "slack": 0.0,
                    "utilization": 1.0,
                    "compliance": [],
                    "compliance_ok": True
                }
            })

    flat_unassigned = []
    for o in unassigned:
        flat_unassigned.append({
            "order_id": o["order_id"],
            "order_no": o["order_no"]
        })

    return {
        "consolidated_count": len(consolidated_trips),
        "unassigned_count": len(unassigned),
        "consolidated_trips": consolidated_trips,
        "unassigned_orders": unassigned,
        "estimated_total_saving": round(sum(t["money_saved"] for t in consolidated_trips), 2),
        
        # 兼容旧版本字段
        "assigned_count": sum(len(t["orders"]) for t in consolidated_trips),
        "assignments": flat_assignments,
        "unassigned": flat_unassigned
    }
