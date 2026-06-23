"""内部简易报销：提交 → 审批（生成应付费用 + 付款申请，计入经营结果）→ 付款。"""

import uuid

from django.utils import timezone

from apps.core.exceptions import AppError

from .models import ExpenseRecord, PaymentRequest, Reimbursement

_CATEGORY_ITEM = {
    "freight_advance": "TRANSPORT_COST",
    "toll": "TOLL",
    "fuel": "FUEL_CARD",
    "loading": "LOADING",
    "lodging": "OTHER_COST",
    "other": "OTHER_COST",
}


def gen_reimb_no() -> str:
    return f"BX{timezone.now():%Y%m%d}{uuid.uuid4().hex[:6].upper()}"


def submit_reimbursement(*, waybill=None, order_no="", category="other", amount=0, reason="", operator=None) -> Reimbursement:
    if not amount or float(amount) <= 0:
        raise AppError("REIMB_AMOUNT", "报销金额必须大于 0。", status=400)
    return Reimbursement.objects.create(
        reimb_no=gen_reimb_no(), waybill=waybill,
        order_no=order_no or (waybill.order.order_no if waybill and waybill.order_id else ""),
        category=category, amount=amount, reason=reason,
        submitted_by=operator if operator and getattr(operator, "is_authenticated", False) else None,
    )


def approve_reimbursement(reimb, *, operator=None) -> Reimbursement:
    """审批通过：生成应付费用（计入毛利/经营结果）+ 下游付款申请。"""
    if reimb.status not in (Reimbursement.STATUS_SUBMITTED,):
        raise AppError("REIMB_NOT_SUBMITTED", "仅已提交的报销可审批。", status=409)
    reimb.status = Reimbursement.STATUS_APPROVED
    reimb.approved_by = operator if operator and getattr(operator, "is_authenticated", False) else None
    reimb.approved_at = timezone.now()
    # 应付费用 → 进入运单成本，反映到经营结果
    if reimb.waybill_id:
        ExpenseRecord.objects.create(
            waybill=reimb.waybill, direction=ExpenseRecord.DIRECTION_PAYABLE,
            expense_item_code=_CATEGORY_ITEM.get(reimb.category, "OTHER_COST"),
            amount=reimb.amount, payee_type="driver", source_system="reimbursement",
            external_id=reimb.reimb_no, remark=f"报销 {reimb.get_category_display()}",
        )
    # 下游付款申请
    pr = PaymentRequest.objects.create(
        request_no=f"PR-{reimb.reimb_no}", waybill=reimb.waybill,
        counterparty_type="reimbursement", counterparty_ref=reimb.order_no,
        amount=reimb.amount, reason=f"报销 {reimb.get_category_display()}：{reimb.reason}"[:255],
        status="created",
    )
    reimb.payment_request = pr
    reimb.save(update_fields=["status", "approved_by", "approved_at", "payment_request", "updated_at"])
    return reimb


def reject_reimbursement(reimb, *, reason="", operator=None) -> Reimbursement:
    if reimb.status not in (Reimbursement.STATUS_SUBMITTED,):
        raise AppError("REIMB_NOT_SUBMITTED", "仅已提交的报销可驳回。", status=409)
    reimb.status = Reimbursement.STATUS_REJECTED
    reimb.remark = reason
    reimb.save(update_fields=["status", "remark", "updated_at"])
    return reimb


def pay_reimbursement(reimb, *, operator=None) -> Reimbursement:
    if reimb.status != Reimbursement.STATUS_APPROVED:
        raise AppError("REIMB_NOT_APPROVED", "仅已审批的报销可付款。", status=409)
    reimb.status = Reimbursement.STATUS_PAID
    reimb.paid_at = timezone.now()
    reimb.save(update_fields=["status", "paid_at", "updated_at"])
    if reimb.payment_request_id:
        reimb.payment_request.status = "paid"
        reimb.payment_request.save(update_fields=["status", "updated_at"])
    return reimb
