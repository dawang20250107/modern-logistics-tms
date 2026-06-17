from decimal import Decimal

from django.db.models import Q, Sum
from django.utils import timezone
from django.utils.dateparse import parse_datetime
from rest_framework.decorators import api_view
from rest_framework.response import Response

from .models import AgentSuggestion, ExceptionRecord, ExpenseRecord, TrackingPoint, Waybill, WaybillEvent
from .services.agent_tools import AgentToolError, execute_tool, list_tools
from .services.deepseek import DeepSeekClient, DeepSeekError


def ok(data, status=200):
    return Response({"success": True, "data": data, "error": None}, status=status)


def error(code, message, status=404):
    return Response({"success": False, "data": None, "error": {"code": code, "message": message}}, status=status)


def deepseek_client():
    return DeepSeekClient()


def find_waybill(waybill_no):
    return (
        Waybill.objects.select_related("customer", "carrier", "vehicle", "driver")
        .filter(waybill_no=waybill_no)
        .first()
    )


def as_time(value):
    if not value:
        return None
    return value.isoformat()


def as_money(value):
    return float(value or Decimal("0"))


def waybill_payload(waybill):
    return {
        "waybill_no": waybill.waybill_no,
        "customer_name": waybill.customer.name if waybill.customer else "",
        "route_name": waybill.route_name,
        "origin": waybill.origin,
        "destination": waybill.destination,
        "vehicle_plate": waybill.vehicle.plate_no if waybill.vehicle else "",
        "driver_name": waybill.driver.name if waybill.driver else "",
        "carrier_name": waybill.carrier.name if waybill.carrier else "",
        "status": waybill.status,
        "dispatch_status": waybill.dispatch_status,
        "risk_level": waybill.risk_level,
        "eta_drift_minutes": waybill.eta_drift_minutes,
        "receipt_status": waybill.receipt_status,
        "planned_arrival": as_time(waybill.planned_arrival),
        "estimated_arrival": as_time(waybill.estimated_arrival),
        "cargo": {
            "quantity": waybill.cargo_quantity,
            "weight_ton": as_money(waybill.cargo_weight_ton),
            "volume_cbm": as_money(waybill.cargo_volume_cbm),
        },
    }


def event_payload(event):
    return {
        "event_type": event.event_type,
        "time": event.event_time.isoformat(),
        "resource": event.resource,
        "payload": event.payload,
    }


def expense_payload(expense):
    return {
        "expense_item_code": expense.expense_item_code,
        "amount": as_money(expense.amount),
        "risk_status": expense.risk_status,
    }


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


@api_view(["GET"])
def health(_request):
    return ok({"status": "ok", "service": "modern-logistics-api"})


@api_view(["GET", "POST"])
def waybills(request):
    if request.method == "GET":
        query = request.GET.get("q", "").strip()
        queryset = Waybill.objects.select_related("customer", "carrier", "vehicle", "driver")
        if query:
            queryset = queryset.filter(
                Q(waybill_no__icontains=query)
                | Q(route_name__icontains=query)
                | Q(customer__name__icontains=query)
                | Q(vehicle__plate_no__icontains=query)
            )

        queryset = queryset.order_by("-eta_drift_minutes", "risk_level", "waybill_no")
        items = [waybill_payload(item) for item in queryset]
        return ok({"items": items, "total": len(items)})

    payload = request.data
    waybill_no = payload.get("waybill_no") or "WB202606040001"
    if Waybill.objects.filter(waybill_no=waybill_no).exists():
        return error("WAYBILL_ALREADY_EXISTS", "Waybill already exists", status=409)

    created = Waybill.objects.create(
        waybill_no=waybill_no,
        route_name=payload.get("route_name", ""),
        origin=payload.get("origin", ""),
        destination=payload.get("destination", ""),
        status=payload.get("status", Waybill.STATUS_PENDING_DISPATCH),
        dispatch_status=payload.get("dispatch_status", "pending_accept"),
        risk_level=payload.get("risk_level", Waybill.RISK_NONE),
        receipt_status=payload.get("receipt_status", "not_due"),
        eta_drift_minutes=int(payload.get("eta_drift_minutes") or 0),
    )
    return ok(waybill_payload(created), status=201)


@api_view(["GET"])
def waybill_detail(_request, waybill_no):
    waybill = find_waybill(waybill_no)
    if not waybill:
        return error("WAYBILL_NOT_FOUND", "Waybill not found")

    return ok(
        {
            **waybill_payload(waybill),
            "timeline": [event_payload(event) for event in waybill.events.all()],
            "agent_suggestions": [suggestion_payload(item) for item in waybill.agent_suggestions.all()],
        }
    )


@api_view(["POST"])
def dispatch_waybill(request, waybill_no):
    waybill = find_waybill(waybill_no)
    if not waybill:
        return error("WAYBILL_NOT_FOUND", "Waybill not found")

    waybill.dispatch_status = request.data.get("dispatch_status", "accepted")
    waybill.status = request.data.get("status", Waybill.STATUS_IN_TRANSIT)
    waybill.save(update_fields=["dispatch_status", "status", "updated_at"])
    return ok(waybill_payload(waybill))


@api_view(["POST"])
def add_waybill_event(request, waybill_no):
    waybill = find_waybill(waybill_no)
    if not waybill:
        return error("WAYBILL_NOT_FOUND", "Waybill not found")

    event_time = parse_datetime(request.data.get("time", "")) or timezone.now()
    event = WaybillEvent.objects.create(
        waybill=waybill,
        event_type=request.data.get("event_type", "manual_event"),
        event_time=event_time,
        resource=request.data.get("resource", waybill.waybill_no),
        payload=dict(request.data),
    )
    return ok({"waybill_no": waybill_no, "event": event_payload(event), "accepted": True}, status=201)


@api_view(["GET"])
def waybill_costs(_request, waybill_no):
    waybill = find_waybill(waybill_no)
    if not waybill:
        return error("WAYBILL_NOT_FOUND", "Waybill not found")

    receivables = waybill.expenses.filter(direction=ExpenseRecord.DIRECTION_RECEIVABLE)
    payables = waybill.expenses.filter(direction=ExpenseRecord.DIRECTION_PAYABLE)
    external = waybill.expenses.filter(direction=ExpenseRecord.DIRECTION_EXTERNAL)
    receivable_total = receivables.aggregate(total=Sum("amount"))["total"] or Decimal("0")
    payable_total = payables.aggregate(total=Sum("amount"))["total"] or Decimal("0")
    external_total = external.aggregate(total=Sum("amount"))["total"] or Decimal("0")
    gross_profit = receivable_total - payable_total - external_total

    return ok(
        {
            "waybill_no": waybill_no,
            "receivables": [expense_payload(item) for item in receivables],
            "payables": [expense_payload(item) for item in payables],
            "external_expenses": [expense_payload(item) for item in external],
            "gross_profit": as_money(gross_profit),
            "gross_margin": float(gross_profit / receivable_total) if receivable_total else 0,
        }
    )


@api_view(["POST"])
def tracking_points(request):
    created = 0
    for point in request.data.get("points", []):
        waybill = find_waybill(point.get("waybill_no", ""))
        if not waybill:
            continue
        TrackingPoint.objects.create(
            waybill=waybill,
            lng=point.get("lng"),
            lat=point.get("lat"),
            speed_kmh=point.get("speed_kmh") or 0,
            reported_at=parse_datetime(point.get("reported_at", "")) or timezone.now(),
        )
        created += 1

    return ok({"received": created, "status": "queued_for_analysis"})


@api_view(["GET"])
def eta(_request, waybill_no):
    waybill = find_waybill(waybill_no)
    if not waybill:
        return error("WAYBILL_NOT_FOUND", "Waybill not found")

    return ok(
        {
            "waybill_no": waybill_no,
            "planned_arrival": as_time(waybill.planned_arrival),
            "estimated_arrival": as_time(waybill.estimated_arrival),
            "eta_drift_minutes": waybill.eta_drift_minutes,
            "risk_level": waybill.risk_level,
            "reason": "route_deviation_detected" if waybill.risk_level == Waybill.RISK_HIGH else "traffic_or_capacity_risk",
        }
    )


@api_view(["POST"])
def create_exception(request):
    waybill = find_waybill(request.data.get("waybill_no", ""))
    record = ExceptionRecord.objects.create(
        waybill=waybill,
        exception_type=request.data.get("exception_type", "manual_exception"),
        description=request.data.get("description", ""),
        responsibility_party=request.data.get("responsibility_party", ""),
        amount=request.data.get("amount") or 0,
    )
    return ok({"exception_id": record.id, "status": record.status, "payload": request.data}, status=201)


@api_view(["POST"])
def expense_records(request):
    waybill = find_waybill(request.data.get("waybill_no", ""))
    if not waybill:
        return error("WAYBILL_NOT_FOUND", "Waybill not found")

    record = ExpenseRecord.objects.create(
        waybill=waybill,
        direction=request.data.get("direction", ExpenseRecord.DIRECTION_EXTERNAL),
        expense_item_code=request.data.get("expense_item_code", "EXTERNAL_EXPENSE"),
        amount=request.data.get("amount") or 0,
        risk_status=request.data.get("risk_status", "pending_check"),
    )
    return ok({"expense_record_id": record.id, "risk_status": record.risk_status, "payload": request.data}, status=201)


@api_view(["POST"])
def payment_requests(request):
    return ok({"payment_request_id": "payreq_001", "status": "sent_to_external_workflow", "payload": request.data})


@api_view(["POST"])
def payment_results(request):
    return ok({"status": "recorded", "payload": request.data})


@api_view(["POST"])
def ai_query_waybill(request):
    query = request.data.get("query", "").strip()
    queryset = Waybill.objects.select_related("customer", "carrier", "vehicle", "driver")
    if query:
        queryset = queryset.filter(
            Q(waybill_no__icontains=query)
            | Q(route_name__icontains=query)
            | Q(customer__name__icontains=query)
            | Q(vehicle__plate_no__icontains=query)
        )
    else:
        queryset = queryset.filter(Q(risk_level__in=[Waybill.RISK_HIGH, Waybill.RISK_MEDIUM]) | Q(receipt_status="pending"))

    waybill_items = list(queryset[:10])
    suggestions = AgentSuggestion.objects.filter(waybill__in=waybill_items)[:10]
    risk_count = sum(1 for item in waybill_items if item.risk_level in {Waybill.RISK_HIGH, Waybill.RISK_MEDIUM})
    answer = f"Found {len(waybill_items)} related waybills, {risk_count} with active ETA or route risk."

    return ok(
        {
            "answer": answer,
            "query": query,
            "waybills": [waybill_payload(item) for item in waybill_items],
            "evidence": [suggestion_payload(item) for item in suggestions],
        }
    )


@api_view(["GET"])
def deepseek_status(_request):
    return ok(deepseek_client().status())


@api_view(["POST"])
def deepseek_chat(request):
    messages = request.data.get("messages")
    if not isinstance(messages, list) or not messages:
        return error("INVALID_MESSAGES", "messages must be a non-empty list.", status=400)

    client = deepseek_client()
    try:
        response = client.chat_completion(
            messages=messages,
            model=request.data.get("model"),
            thinking=request.data.get("thinking"),
            reasoning_effort=request.data.get("reasoning_effort"),
            stream=False,
        )
    except DeepSeekError as exc:
        return error(exc.code, exc.message, status=exc.status)

    return ok(
        {
            "provider": "deepseek",
            "model": response.get("model"),
            "content": response.get("choices", [{}])[0].get("message", {}).get("content", ""),
            "raw": response,
        }
    )


@api_view(["GET"])
def agent_tools(_request):
    return ok({"tools": list_tools()})


@api_view(["POST"])
def agent_tool_execute(request):
    tool_name = request.data.get("tool_name")
    arguments = request.data.get("arguments") or {}
    if not tool_name:
        return error("TOOL_NAME_REQUIRED", "tool_name is required.", status=400)
    if not isinstance(arguments, dict):
        return error("INVALID_ARGUMENTS", "arguments must be an object.", status=400)

    try:
        result = execute_tool(tool_name, arguments)
    except AgentToolError as exc:
        return error(exc.code, exc.message, status=exc.status)

    return ok({"tool_name": tool_name, "result": result})
