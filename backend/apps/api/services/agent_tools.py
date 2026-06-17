from decimal import Decimal

from django.db.models import Sum

from apps.api.models import AgentSuggestion, ExpenseRecord, Waybill


class AgentToolError(Exception):
    def __init__(self, code, message, status=400):
        super().__init__(message)
        self.code = code
        self.message = message
        self.status = status


TOOL_DEFINITIONS = [
    {
        "name": "logistics.eta_risk_analysis",
        "description": "Analyze ETA drift and route risk for a waybill.",
        "input_schema": {
            "type": "object",
            "required": ["waybill_no"],
            "properties": {"waybill_no": {"type": "string"}},
        },
    },
    {
        "name": "logistics.receipt_reminder",
        "description": "Generate a receipt collection reminder suggestion.",
        "input_schema": {
            "type": "object",
            "required": ["waybill_no"],
            "properties": {"waybill_no": {"type": "string"}},
        },
    },
    {
        "name": "finance.expense_risk_check",
        "description": "Check waybill cost margin and suspicious external expense records.",
        "input_schema": {
            "type": "object",
            "required": ["waybill_no"],
            "properties": {"waybill_no": {"type": "string"}},
        },
    },
]


def list_tools():
    return TOOL_DEFINITIONS


def execute_tool(name, arguments):
    if name == "logistics.eta_risk_analysis":
        return eta_risk_analysis(arguments)
    if name == "logistics.receipt_reminder":
        return receipt_reminder(arguments)
    if name == "finance.expense_risk_check":
        return expense_risk_check(arguments)
    raise AgentToolError("UNKNOWN_AGENT_TOOL", f"Unknown agent tool: {name}", status=404)


def get_waybill(arguments):
    waybill_no = (arguments or {}).get("waybill_no")
    if not waybill_no:
        raise AgentToolError("WAYBILL_NO_REQUIRED", "waybill_no is required.")

    waybill = (
        Waybill.objects.select_related("customer", "carrier", "vehicle", "driver")
        .filter(waybill_no=waybill_no)
        .first()
    )
    if not waybill:
        raise AgentToolError("WAYBILL_NOT_FOUND", "Waybill not found.", status=404)
    return waybill


def create_suggestion(waybill, suggestion_type, title, body, evidence):
    return AgentSuggestion.objects.create(
        waybill=waybill,
        suggestion_type=suggestion_type,
        title=title,
        body=body,
        evidence=evidence,
    )


def suggestion_payload(suggestion):
    return {
        "suggestion_id": suggestion.id,
        "suggestion_type": suggestion.suggestion_type,
        "title": suggestion.title,
        "body": suggestion.body,
        "status": suggestion.status,
        "evidence": suggestion.evidence,
        "created_at": suggestion.created_at.isoformat(),
    }


def eta_risk_analysis(arguments):
    waybill = get_waybill(arguments)
    high_risk = waybill.risk_level in {Waybill.RISK_HIGH, Waybill.RISK_MEDIUM}
    drift_hours = round(waybill.eta_drift_minutes / 60, 1)

    if high_risk:
        title = "ETA or route risk requires confirmation"
        body = (
            f"{waybill.waybill_no} has ETA drift of {drift_hours}h. "
            "Confirm driver route, congestion, and customer ETA notice."
        )
        next_actions = ["contact_driver", "notify_customer", "monitor_next_location_event"]
    else:
        title = "No active ETA risk"
        body = f"{waybill.waybill_no} has no active ETA risk."
        next_actions = ["continue_monitoring"]

    evidence = {
        "waybill_no": waybill.waybill_no,
        "route_name": waybill.route_name,
        "risk_level": waybill.risk_level,
        "eta_drift_minutes": waybill.eta_drift_minutes,
        "planned_arrival": waybill.planned_arrival.isoformat() if waybill.planned_arrival else None,
        "estimated_arrival": waybill.estimated_arrival.isoformat() if waybill.estimated_arrival else None,
    }
    suggestion = create_suggestion(waybill, "eta_risk", title, body, evidence) if high_risk else None

    return {
        "tool_name": "logistics.eta_risk_analysis",
        "waybill_no": waybill.waybill_no,
        "risk_detected": high_risk,
        "summary": body,
        "next_actions": next_actions,
        "evidence": evidence,
        "suggestion": suggestion_payload(suggestion) if suggestion else None,
    }


def receipt_reminder(arguments):
    waybill = get_waybill(arguments)
    pending = waybill.receipt_status == "pending"
    evidence = {
        "waybill_no": waybill.waybill_no,
        "receipt_status": waybill.receipt_status,
        "carrier_name": waybill.carrier.name if waybill.carrier else "",
        "driver_name": waybill.driver.name if waybill.driver else "",
    }

    if pending:
        title = "Receipt reminder required"
        body = f"{waybill.waybill_no} is pending electronic receipt. Remind carrier to upload and trigger OCR review."
        suggestion = create_suggestion(waybill, "receipt_reminder", title, body, evidence)
    else:
        body = f"{waybill.waybill_no} receipt is not pending."
        suggestion = None

    return {
        "tool_name": "logistics.receipt_reminder",
        "waybill_no": waybill.waybill_no,
        "reminder_required": pending,
        "summary": body,
        "next_actions": ["send_carrier_reminder", "schedule_ocr_review"] if pending else ["continue_monitoring"],
        "evidence": evidence,
        "suggestion": suggestion_payload(suggestion) if suggestion else None,
    }


def expense_risk_check(arguments):
    waybill = get_waybill(arguments)
    receivable_total = total_amount(waybill, ExpenseRecord.DIRECTION_RECEIVABLE)
    payable_total = total_amount(waybill, ExpenseRecord.DIRECTION_PAYABLE)
    external_total = total_amount(waybill, ExpenseRecord.DIRECTION_EXTERNAL)
    gross_profit = receivable_total - payable_total - external_total
    gross_margin = (gross_profit / receivable_total) if receivable_total else Decimal("0")
    risky_expenses = list(waybill.expenses.exclude(risk_status="normal"))
    risk_detected = gross_margin < Decimal("0.12") or bool(risky_expenses)
    evidence = {
        "waybill_no": waybill.waybill_no,
        "receivable_total": float(receivable_total),
        "payable_total": float(payable_total),
        "external_total": float(external_total),
        "gross_profit": float(gross_profit),
        "gross_margin": float(gross_margin),
        "risky_expense_count": len(risky_expenses),
    }

    if risk_detected:
        title = "Expense risk requires review"
        body = f"{waybill.waybill_no} margin is {float(gross_margin):.2%}; review cost proof and external expense records."
        suggestion = create_suggestion(waybill, "expense_risk", title, body, evidence)
    else:
        body = f"{waybill.waybill_no} cost structure is within current guardrails."
        suggestion = None

    return {
        "tool_name": "finance.expense_risk_check",
        "waybill_no": waybill.waybill_no,
        "risk_detected": risk_detected,
        "summary": body,
        "next_actions": ["review_cost_proof", "hold_payment_request"] if risk_detected else ["continue_settlement"],
        "evidence": evidence,
        "suggestion": suggestion_payload(suggestion) if suggestion else None,
    }


def total_amount(waybill, direction):
    return waybill.expenses.filter(direction=direction).aggregate(total=Sum("amount"))["total"] or Decimal("0")
