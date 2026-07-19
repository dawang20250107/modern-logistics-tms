"""承运商优先调度评分（Carrier-first dispatch scoring）。

产品原则：调度不是"找车"，而是"找合适承运商"。
本模块从**运单历史**实时计算承运商在目标线路上的经营表现（成交票数 / 常跑线路 /
准班率 / 异常率 / 回单及时率 / 最近成交价），按权重打分，产出**可执行派单建议 +
风险说明 + 是否需主管确认**，供调度员人工确认（建议态，不自动派）。

派单通道优先级（默认）：外包承运商 > 网货平台 > 自营车（自营仅特殊场景兜底）。
"""

from __future__ import annotations

from datetime import timedelta

from django.db.models import Avg, F, Q, Sum
from django.utils import timezone

# 评分权重（合计 100）——对应产品需求中的推荐评分维度表
WEIGHTS = {
    "route_familiarity": 20,  # 常跑线路
    "price_reasonable": 20,   # 报价合理
    "on_time": 15,            # 准班率
    "low_exception": 15,      # 异常率低
    "receipt_timely": 10,     # 回单及时
    "responsiveness": 10,     # 响应速度（暂无采集，给中性基线）
    "compliance": 10,         # 合规资质
}

# 无历史数据时的中性基线（避免新承运商被一刀切压到 0 分）
_BASELINE = {
    "on_time": 0.85,
    "low_exception": 0.90,
    "receipt_timely": 0.88,
    "responsiveness": 0.80,
}

_DONE_STATUSES = ("arrived", "signed", "delivered", "settled")
_RECEIPT_DONE = ("returned", "audited")


def _rate(numerator: int, denominator: int, default: float) -> float:
    return round(numerator / denominator, 4) if denominator else default


def carrier_performance(carrier, origin: str, destination: str, *, days: int = 90) -> dict:
    """从近 N 天运单历史统计承运商经营表现（本线路 + 整体）。"""
    from .models import Waybill

    since = timezone.now() - timedelta(days=days)
    qs = Waybill.objects.filter(carrier=carrier, created_at__gte=since).exclude(status="voided")

    total = qs.count()
    route_qs = qs.filter(origin=origin, destination=destination) if origin and destination else qs.none()
    route_hits = route_qs.count()

    # 准班率：有计划到达且已实际到达的运单中，实际不晚于计划的占比
    timed = qs.exclude(planned_arrival__isnull=True).exclude(arrived_at__isnull=True)
    timed_total = timed.count()
    on_time_hits = timed.filter(arrived_at__lte=F("planned_arrival")).count() if timed_total else 0

    # 异常率：关联异常记录的运单占比
    exc_total = qs.filter(exceptions__isnull=False).distinct().count()

    # 回单及时率：已完成运单中回单已回收/已核销的占比
    done = qs.filter(status__in=_DONE_STATUSES)
    done_total = done.count()
    receipt_hits = done.filter(receipt_status__in=_RECEIPT_DONE).count()

    # 最近成交价（本线路应付均值，来自费用台账）
    lane_payable = (
        route_qs.annotate(pay=Sum("expenses__amount", filter=Q(expenses__direction="payable")))
        .aggregate(avg=Avg("pay"))
        .get("avg")
    )

    return {
        "deals": total,
        "route_hits": route_hits,
        "on_time_rate": _rate(on_time_hits, timed_total, _BASELINE["on_time"]),
        "exception_rate": _rate(exc_total, total, 1 - _BASELINE["low_exception"]),
        "receipt_timely_rate": _rate(receipt_hits, done_total, _BASELINE["receipt_timely"]),
        "recent_deal_price": round(float(lane_payable), 2) if lane_payable else None,
        "has_history": total > 0,
    }


def _score(perf: dict, price_pos: float) -> tuple[int, dict]:
    """把经营表现 + 报价位置(0..1，越高越便宜)折算为 0-100 综合分。"""
    parts = {
        "route_familiarity": min(perf["route_hits"] / 5.0, 1.0),
        "price_reasonable": price_pos,
        "on_time": perf["on_time_rate"],
        "low_exception": 1.0 - perf["exception_rate"],
        "receipt_timely": perf["receipt_timely_rate"],
        "responsiveness": _BASELINE["responsiveness"],
        "compliance": 1.0,  # 进入评分的均已过硬阻断，合规基线满分（分级在下方微调）
    }
    score = sum(parts[k] * WEIGHTS[k] for k in WEIGHTS)
    return round(score), parts


def _risk_and_label(carrier, perf: dict, is_cheapest: bool) -> tuple[str, str, list[str]]:
    """产出风险等级 / 推荐标签 / 风险说明。"""
    notes: list[str] = []
    exc = perf["exception_rate"]
    on_time = perf["on_time_rate"]
    grade = getattr(carrier, "grade", "B")

    if exc >= 0.10:
        notes.append(f"异常率偏高（{exc:.0%}）")
    if on_time < 0.85 and perf["has_history"]:
        notes.append(f"准班率偏低（{on_time:.0%}）")
    if grade in ("C", "D"):
        notes.append(f"综合评级 {grade}，需关注")
    if not perf["has_history"]:
        notes.append("近 90 天无成交历史，建议先试单")

    if exc >= 0.10 or on_time < 0.80 or grade == "D":
        risk = "high"
    elif exc >= 0.05 or grade == "C" or not perf["has_history"]:
        risk = "medium"
    else:
        risk = "low"

    if is_cheapest and (exc >= 0.06 or on_time < 0.85):
        label = "低价有风险"
        notes.insert(0, "报价最低但履约有波动，议价需谨慎")
    elif on_time >= 0.97 and exc <= 0.03 and perf["has_history"]:
        label = "高服务"
    elif risk == "low" and perf["route_hits"] >= 3:
        label = "推荐"
    else:
        label = "备选"

    return risk, label, notes


def score_carriers(obj, *, top: int = 6, days: int = 90) -> list[dict]:
    """为订单/运单在目标线路上给候选承运商打分排序（价低者未必最优，综合履约）。

    obj 可为 Order 或 Waybill：需要 origin/destination/cargo_weight_ton。
    """
    from apps.finance.models import PricingRule
    from apps.masterdata.models import Carrier

    origin = getattr(obj, "origin", "") or ""
    destination = getattr(obj, "destination", "") or ""
    weight = getattr(obj, "cargo_weight_ton", 0)

    # 硬阻断：黑名单 / 停用 / 资质过期承运商不进入推荐
    candidates = []
    for carrier in Carrier.objects.filter(is_active=True, blacklisted=False):
        if carrier.dispatch_block_reason():
            continue
        rules = PricingRule.objects.filter(
            is_active=True, price_type=PricingRule.PRICE_TYPE_COST
        ).filter(Q(carrier=carrier) | Q(carrier__isnull=True))
        prices = [rule.quote(weight).get("amount", 0) for rule in rules]
        quote = float(min(prices)) if prices else None
        perf = carrier_performance(carrier, origin, destination, days=days)
        candidates.append({"carrier": carrier, "quote": quote, "perf": perf})

    if not candidates:
        return []

    quoted = [c["quote"] for c in candidates if c["quote"] is not None]
    lo, hi = (min(quoted), max(quoted)) if quoted else (0.0, 0.0)
    cheapest = lo if quoted else None

    rows = []
    for c in candidates:
        carrier, quote, perf = c["carrier"], c["quote"], c["perf"]
        # 报价位置：越便宜分越高；无报价给中性 0.5
        if quote is None or hi == lo:
            price_pos = 0.5
        else:
            price_pos = 1.0 - (quote - lo) / (hi - lo)
        score, breakdown = _score(perf, price_pos)
        is_cheapest = quote is not None and cheapest is not None and abs(quote - cheapest) < 1e-6
        risk, label, notes = _risk_and_label(carrier, perf, is_cheapest)
        # 建议成交价区间：优先用本线路历史成交价，否则用成本报价上浮
        base = perf["recent_deal_price"] or quote
        price_band = [round(base * 0.97), round(base * 1.03)] if base else None
        rows.append({
            "carrier_id": str(carrier.id),
            "carrier": carrier.name,
            "carrier_grade": getattr(carrier, "grade", "B"),
            "quote": quote,
            "recent_deal_price": perf["recent_deal_price"],
            "suggested_price_band": price_band,
            "deals": perf["deals"],
            "route_hits": perf["route_hits"],
            "on_time_rate": perf["on_time_rate"],
            "exception_rate": perf["exception_rate"],
            "receipt_timely_rate": perf["receipt_timely_rate"],
            "score": score,
            "score_breakdown": {k: round(v, 3) for k, v in breakdown.items()},
            "risk_level": risk,
            "label": label,
            "risk_notes": notes,
        })

    # 综合分降序；同分价低者优先
    rows.sort(key=lambda r: (-r["score"], r["quote"] if r["quote"] is not None else 1e12))
    return rows[:top]


def carrier_recommendation(obj, *, days: int = 90) -> dict | None:
    """给出最终推荐结论：首选承运商 + 建议价区间 + 风险 + 理由 + 是否需主管确认。"""
    rows = score_carriers(obj, days=days)
    if not rows:
        return None
    top = rows[0]
    reasons = []
    if top["route_hits"]:
        reasons.append(f"近 90 天该线路成交 {top['route_hits']} 单，准班率 {top['on_time_rate']:.0%}")
    if top["recent_deal_price"]:
        reasons.append(f"最近成交价约 ¥{top['recent_deal_price']:.0f}")
    if top["receipt_timely_rate"]:
        reasons.append(f"回单及时率 {top['receipt_timely_rate']:.0%}")
    if not reasons:
        reasons.append("暂无历史成交，建议先试单或电话询价确认")
    # 需主管确认：首选风险偏高、或无历史、或最优也异常偏多
    needs_approval = top["risk_level"] == "high" or not top["route_hits"] or top["exception_rate"] >= 0.08
    return {
        "carrier_id": top["carrier_id"],
        "carrier": top["carrier"],
        "suggested_price_band": top["suggested_price_band"],
        "risk_level": top["risk_level"],
        "label": top["label"],
        "reasons": reasons,
        "risk_notes": top["risk_notes"],
        "needs_approval": needs_approval,
    }
