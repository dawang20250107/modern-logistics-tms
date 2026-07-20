"""单据血缘：给定订单，返回 订单(DD) → 运单(YD) → 对账单(ST) 的完整关系图。

用于「确立订单/运单/对账单关系」的可追溯性——一次查询即可看清一张订单
拆出了哪些运单、各挂在哪个批次、各产生的应收/应付费用，以及这些费用最终
归集进了哪些应收/应付对账单及其结算进度。参见 docs/order-waybill-statement-relationship.md。
"""

from decimal import Decimal


def _disp(obj, field: str) -> str:
    """安全取 choices 展示名：字段未绑定 choices 时回退原始值（Order.status 未绑定 choices）。"""
    getter = getattr(obj, f"get_{field}_display", None)
    return getter() if callable(getter) else str(getattr(obj, field, "") or "")


def order_lineage(order) -> dict:
    from apps.finance.models import ExpenseRecord, StatementLine

    waybills = list(order.waybills.select_related("carrier", "batch", "batch__carrier").all())
    wb_ids = [w.id for w in waybills]

    # 各运单的费用明细（应收/应付/外部）
    exp_by_wb: dict = {}
    for e in ExpenseRecord.objects.filter(waybill_id__in=wb_ids).order_by("direction", "-amount"):
        exp_by_wb.setdefault(e.waybill_id, []).append(e)

    # 各运单的费用落进了哪些对账单（经 StatementLine → ExpenseRecord.waybill 反查）
    stmts_by_wb: dict = {}
    all_stmts: dict = {}
    for ln in (
        StatementLine.objects.filter(expense_record__waybill_id__in=wb_ids)
        .select_related("statement", "expense_record")
    ):
        st = ln.statement
        if st is None or not ln.expense_record_id:
            continue
        wbid = ln.expense_record.waybill_id
        all_stmts[st.id] = st
        if wbid:
            stmts_by_wb.setdefault(wbid, {})[st.id] = st

    def _stmt(st) -> dict:
        return {
            "id": str(st.id),
            "statement_no": st.statement_no,
            "direction": st.direction,
            "counterparty_type": st.counterparty_type,
            "counterparty_name": st.counterparty_name,
            "status": st.status,
            "status_label": _disp(st, "status"),
            "total_amount": float(st.total_amount),
            "settled_amount": float(st.settled_amount),
            "outstanding": float(st.total_amount - st.settled_amount),
            "period_start": st.period_start,
            "period_end": st.period_end,
        }

    wb_out = []
    recv_total = Decimal("0")
    pay_total = Decimal("0")
    for w in waybills:
        wexp = exp_by_wb.get(w.id, [])
        r = sum((e.amount for e in wexp if e.direction == "receivable"), Decimal("0"))
        p = sum((e.amount for e in wexp if e.direction == "payable"), Decimal("0"))
        recv_total += r
        pay_total += p
        wb_out.append({
            "id": str(w.id),
            "waybill_no": w.waybill_no,
            "status": w.status,
            "status_label": _disp(w, "status"),
            "carrier_name": w.carrier.name if w.carrier_id else "",
            "dispatch_type": w.dispatch_type,
            "batch_no": w.batch.batch_no if w.batch_id else "",
            "receivable": float(r),
            "payable": float(p),
            "expenses": [{
                "direction": e.direction,
                "expense_item_code": e.expense_item_code,
                "amount": float(e.amount),
                "payee_type": e.payee_type,
                "payee_ref": e.payee_ref,
                "risk_status": e.risk_status,
            } for e in wexp],
            "statements": [_stmt(s) for s in stmts_by_wb.get(w.id, {}).values()],
        })

    batches: dict = {}
    for w in waybills:
        if w.batch_id and w.batch_id not in batches:
            b = w.batch
            batches[w.batch_id] = {
                "batch_no": b.batch_no,
                "carrier_name": b.carrier.name if b.carrier_id else "",
                "status": b.status,
                "statement_no": b.statement_no,
                "order_count": b.order_count,
                "total_payable": float(b.total_payable),
            }

    return {
        "order": {
            "id": str(order.id),
            "order_no": order.order_no,
            "status": order.status,
            "status_label": _disp(order, "status"),
            "customer_name": order.customer.name if order.customer_id else "散客",
            "business_type": order.business_type,
            "quoted_amount": float(order.quoted_amount or 0),
            "created_at": order.created_at,
        },
        "waybills": wb_out,
        "batches": list(batches.values()),
        "ar_statements": [_stmt(s) for s in all_stmts.values() if s.direction == "receivable"],
        "ap_statements": [_stmt(s) for s in all_stmts.values() if s.direction == "payable"],
        "summary": {
            "waybill_count": len(waybills),
            "receivable_total": float(recv_total),
            "payable_total": float(pay_total),
            "gross": float(recv_total - pay_total),
            "statement_count": len(all_stmts),
        },
    }
