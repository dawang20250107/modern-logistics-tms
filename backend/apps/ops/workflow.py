"""工作流编排：把各环节串成一条自动流转的流水线。

- 司机打卡节点 → 自动推进运单状态机（装货→已装/发车→已发车/在途→运输中/到达卸货→已到达）。
- 订单全流程总览：建单→确认→派单→合同→司机注册→在途→签收→报销→付款→对账→完成。
"""

from .models import Waybill
from .services import allowed_next, transition_waybill

# 主推进链路（前向单向）
_STAGE_ORDER = [
    Waybill.STATUS_PENDING_DISPATCH, Waybill.STATUS_DISPATCHED, Waybill.STATUS_LOADED,
    Waybill.STATUS_DEPARTED, Waybill.STATUS_IN_TRANSIT, Waybill.STATUS_ARRIVED,
    Waybill.STATUS_SIGNED, Waybill.STATUS_DELIVERED, Waybill.STATUS_SETTLED,
]

# 打卡节点 → 目标运单状态
NODE_TO_STATUS = {
    "loading": Waybill.STATUS_LOADED,
    "depart_loaded": Waybill.STATUS_DEPARTED,
    "in_transit": Waybill.STATUS_IN_TRANSIT,
    "arrive_delivery": Waybill.STATUS_ARRIVED,
}


def advance_waybill_to(waybill, target, *, operator=None, remark="") -> bool:
    """沿主链路前向推进到目标状态（逐级 transition，仅前进，不可达则止步）。返回是否发生推进。"""
    if waybill.status not in _STAGE_ORDER or target not in _STAGE_ORDER:
        return False
    moved = False
    guard = 0
    while _STAGE_ORDER.index(waybill.status) < _STAGE_ORDER.index(target) and guard < 12:
        guard += 1
        nxt = _STAGE_ORDER[_STAGE_ORDER.index(waybill.status) + 1]
        if nxt not in allowed_next(waybill.status):
            break
        transition_waybill(waybill, nxt, operator=operator, remark=remark or "司机打卡自动推进")
        moved = True
    return moved


def advance_from_checkin(waybill, node, *, operator=None) -> str:
    """司机打卡 → 推进运单状态。返回推进后的状态。"""
    target = NODE_TO_STATUS.get(node)
    if target:
        advance_waybill_to(waybill, target, operator=operator, remark=f"司机打卡：{node}")
    return waybill.status


def _stage(key, name, done, detail="", at=None):
    return {"key": key, "name": name, "done": bool(done), "detail": detail,
            "at": at.isoformat() if at else None}


def order_workflow(order) -> dict:
    """订单全流程总览：各环节 done/pending + 当前进度。"""
    from apps.finance.models import PaymentRequest, Reimbursement, Statement

    wb = order.waybills.order_by("-created_at").first()
    contract = wb.contracts.first() if wb else None
    driver = wb.driver if wb and wb.driver_id else None
    receipt_ok = bool(wb and wb.receipt_status in ("received", "confirmed")) or bool(
        wb and wb.status in (Waybill.STATUS_SIGNED, Waybill.STATUS_DELIVERED, Waybill.STATUS_SETTLED)
    )
    reimbs = list(Reimbursement.objects.filter(waybill=wb)) if wb else []
    pay_ok = bool(wb) and PaymentRequest.objects.filter(waybill=wb).exists() and \
        not PaymentRequest.objects.filter(waybill=wb).exclude(status="paid").exists()
    settled = bool(wb) and Statement.objects.filter(lines__waybill_no=wb.waybill_no).exists()

    transit_label = wb.get_status_display() if wb else "—"
    stages = [
        _stage("created", "建单", True, order.order_no, order.created_at),
        _stage("confirmed", "确认", order.status not in (order.STATUS_DRAFT, order.STATUS_PENDING_CONFIRM)),
        _stage("dispatched", "派单", bool(wb), wb.waybill_no if wb else "待派单"),
        _stage("contract", "承运合同", contract and contract.confirm_status == "confirmed",
               contract.get_confirm_status_display() if contract else "未生成"),
        _stage("onboard", "司机注册", driver and driver.app_registered,
               driver.name if driver else "未指派"),
        _stage("transit", "在途", wb and wb.status in (Waybill.STATUS_SIGNED, Waybill.STATUS_DELIVERED, Waybill.STATUS_SETTLED),
               transit_label),
        _stage("pod", "签收回单", receipt_ok),
        _stage("reimburse", "报销", all(r.status in ("paid", "rejected") for r in reimbs) if reimbs else True,
               f"{len(reimbs)} 笔" if reimbs else "无"),
        _stage("payment", "下游付款", pay_ok, "已付清" if pay_ok else "待付款"),
        _stage("reconcile", "上游对账", settled, "已对账" if settled else "待对账"),
        _stage("completed", "完成", order.status == order.STATUS_COMPLETED),
    ]
    current = next((s["key"] for s in stages if not s["done"]), "completed")
    return {"order_no": order.order_no, "current": current, "stages": stages}
