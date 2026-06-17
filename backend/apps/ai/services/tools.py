"""Agent 工具注册表。

原则：模型只能"请求"工具，业务逻辑由服务端执行并返回证据；高风险动作不在工具内自动完成。
每个工具产出 evidence（可追溯输入与依据），命中风险时落 AgentSuggestion 供人工确认。
"""

from decimal import Decimal

from django.db.models import Sum

from apps.core.exceptions import AppError

_REGISTRY: dict = {}


def tool(name: str, description: str, input_schema: dict):
    def decorator(fn):
        _REGISTRY[name] = {
            "name": name,
            "description": description,
            "input_schema": input_schema,
            "fn": fn,
        }
        return fn

    return decorator


def list_tools() -> list[dict]:
    return [{k: v for k, v in spec.items() if k != "fn"} for spec in _REGISTRY.values()]


def execute_tool(name: str, arguments: dict) -> dict:
    spec = _REGISTRY.get(name)
    if spec is None:
        raise AppError("UNKNOWN_AGENT_TOOL", f"未知工具：{name}", status=404)
    arguments = arguments or {}
    for field in spec["input_schema"].get("required", []):
        if field not in arguments:
            raise AppError("INVALID_ARGUMENTS", f"缺少必填参数：{field}", status=400)
    return spec["fn"](arguments)


# ── 工具实现 ────────────────────────────────────────────

_WAYBILL_SCHEMA = {
    "type": "object",
    "required": ["waybill_no"],
    "properties": {"waybill_no": {"type": "string"}},
}


def _get_waybill(arguments):
    from apps.ops.models import Waybill

    waybill_no = arguments.get("waybill_no")
    waybill = (
        Waybill.objects.select_related("customer", "carrier", "vehicle", "driver")
        .filter(waybill_no=waybill_no)
        .first()
    )
    if waybill is None:
        raise AppError("WAYBILL_NOT_FOUND", "运单不存在。", status=404)
    return waybill


def _create_suggestion(waybill, suggestion_type, title, body, evidence, tool_name):
    from apps.ai.models import AgentSuggestion

    suggestion = AgentSuggestion.objects.create(
        waybill=waybill,
        suggestion_type=suggestion_type,
        title=title,
        body=body,
        evidence=evidence,
        tool_name=tool_name,
    )
    return {
        "suggestion_id": str(suggestion.id),
        "suggestion_type": suggestion.suggestion_type,
        "title": suggestion.title,
        "body": suggestion.body,
        "status": suggestion.status,
        "evidence": suggestion.evidence,
    }


@tool(
    "logistics.eta_risk_analysis",
    "分析运单 ETA 偏移与路线风险。",
    _WAYBILL_SCHEMA,
)
def eta_risk_analysis(arguments):
    from apps.ops.models import Waybill

    waybill = _get_waybill(arguments)
    high_risk = waybill.risk_level in {Waybill.RISK_HIGH, Waybill.RISK_MEDIUM}
    drift_hours = round(waybill.eta_drift_minutes / 60, 1)
    evidence = {
        "waybill_no": waybill.waybill_no,
        "route_name": waybill.route_name,
        "risk_level": waybill.risk_level,
        "eta_drift_minutes": waybill.eta_drift_minutes,
        "planned_arrival": waybill.planned_arrival.isoformat() if waybill.planned_arrival else None,
        "estimated_arrival": waybill.estimated_arrival.isoformat() if waybill.estimated_arrival else None,
    }
    if high_risk:
        body = f"{waybill.waybill_no} ETA 偏移 {drift_hours} 小时，建议确认司机路线、拥堵情况并向客户同步 ETA。"
        suggestion = _create_suggestion(
            waybill, "eta_risk", "ETA 或路线风险待确认", body, evidence, "logistics.eta_risk_analysis"
        )
        next_actions = ["contact_driver", "notify_customer", "monitor_next_location_event"]
    else:
        body = f"{waybill.waybill_no} 当前无活跃 ETA 风险。"
        suggestion = None
        next_actions = ["continue_monitoring"]
    return {
        "tool_name": "logistics.eta_risk_analysis",
        "waybill_no": waybill.waybill_no,
        "risk_detected": high_risk,
        "summary": body,
        "next_actions": next_actions,
        "evidence": evidence,
        "suggestion": suggestion,
    }


@tool(
    "logistics.receipt_reminder",
    "生成回单催收建议。",
    _WAYBILL_SCHEMA,
)
def receipt_reminder(arguments):
    waybill = _get_waybill(arguments)
    pending = waybill.receipt_status == "pending"
    evidence = {
        "waybill_no": waybill.waybill_no,
        "receipt_status": waybill.receipt_status,
        "carrier_name": waybill.carrier.name if waybill.carrier else "",
        "driver_name": waybill.driver.name if waybill.driver else "",
    }
    if pending:
        body = f"{waybill.waybill_no} 电子回单待确认，建议提醒承运商上传并触发 OCR 复核。"
        suggestion = _create_suggestion(
            waybill, "receipt_reminder", "回单催收待处理", body, evidence, "logistics.receipt_reminder"
        )
        next_actions = ["send_carrier_reminder", "schedule_ocr_review"]
    else:
        body = f"{waybill.waybill_no} 回单状态非待处理。"
        suggestion = None
        next_actions = ["continue_monitoring"]
    return {
        "tool_name": "logistics.receipt_reminder",
        "waybill_no": waybill.waybill_no,
        "reminder_required": pending,
        "summary": body,
        "next_actions": next_actions,
        "evidence": evidence,
        "suggestion": suggestion,
    }


@tool(
    "finance.expense_risk_check",
    "检查运单成本毛利与可疑费用记录。",
    _WAYBILL_SCHEMA,
)
def expense_risk_check(arguments):
    from apps.finance.models import ExpenseRecord

    waybill = _get_waybill(arguments)

    def total(direction):
        return waybill.expenses.filter(direction=direction).aggregate(t=Sum("amount"))["t"] or Decimal("0")

    receivable = total(ExpenseRecord.DIRECTION_RECEIVABLE)
    payable = total(ExpenseRecord.DIRECTION_PAYABLE)
    external = total(ExpenseRecord.DIRECTION_EXTERNAL)
    gross = receivable - payable - external
    margin = (gross / receivable) if receivable else Decimal("0")
    risky = list(waybill.expenses.exclude(risk_status="normal"))
    risk_detected = margin < Decimal("0.12") or bool(risky)
    evidence = {
        "waybill_no": waybill.waybill_no,
        "receivable_total": float(receivable),
        "payable_total": float(payable),
        "external_total": float(external),
        "gross_profit": float(gross),
        "gross_margin": float(margin),
        "risky_expense_count": len(risky),
    }
    if risk_detected:
        body = f"{waybill.waybill_no} 毛利率 {float(margin):.2%}，建议复核成本凭证与外部费用记录。"
        suggestion = _create_suggestion(
            waybill, "expense_risk", "费用风险待复核", body, evidence, "finance.expense_risk_check"
        )
        next_actions = ["review_cost_proof", "hold_payment_request"]
    else:
        body = f"{waybill.waybill_no} 成本结构在当前阈值内。"
        suggestion = None
        next_actions = ["continue_settlement"]
    return {
        "tool_name": "finance.expense_risk_check",
        "waybill_no": waybill.waybill_no,
        "risk_detected": risk_detected,
        "summary": body,
        "next_actions": next_actions,
        "evidence": evidence,
        "suggestion": suggestion,
    }


@tool(
    "logistics.dispatch_recommendation",
    "为运单推荐可用车辆/司机并预估成本毛利。",
    _WAYBILL_SCHEMA,
)
def dispatch_recommendation(arguments):
    from apps.finance.services import estimate_costs
    from apps.masterdata.models import Driver, Vehicle
    from apps.ops.models import Waybill

    waybill = _get_waybill(arguments)
    busy_v = set(
        Waybill.objects.filter(status=Waybill.STATUS_IN_TRANSIT)
        .exclude(vehicle__isnull=True)
        .values_list("vehicle_id", flat=True)
    )
    busy_d = set(
        Waybill.objects.filter(status=Waybill.STATUS_IN_TRANSIT)
        .exclude(driver__isnull=True)
        .values_list("driver_id", flat=True)
    )
    vehicle = Vehicle.objects.filter(is_active=True).exclude(id__in=busy_v).first()
    driver = Driver.objects.filter(is_active=True).exclude(id__in=busy_d).first()
    est = estimate_costs(waybill)
    body = (
        f"推荐车辆 {vehicle.plate_no if vehicle else '无可用'}、司机 {driver.name if driver else '无可用'}；"
        f"预估收入 {est['income']}、成本 {est['cost']}、毛利 {est['gross']}。"
    )
    evidence = {
        "recommended_vehicle": vehicle.plate_no if vehicle else None,
        "recommended_driver": driver.name if driver else None,
        **est,
    }
    suggestion = _create_suggestion(
        waybill, "dispatch", "调度建议", body, evidence, "logistics.dispatch_recommendation"
    )
    return {
        "tool_name": "logistics.dispatch_recommendation",
        "waybill_no": waybill.waybill_no,
        "summary": body,
        "evidence": evidence,
        "suggestion": suggestion,
    }


@tool(
    "logistics.exception_analysis",
    "分析运单异常的可能原因与责任建议。",
    _WAYBILL_SCHEMA,
)
def exception_analysis(arguments):
    from apps.ops.models import Waybill

    waybill = _get_waybill(arguments)
    tracking_count = waybill.tracking_points.count()
    open_exceptions = waybill.exceptions.exclude(status="closed").count()
    causes = []
    if waybill.eta_drift_minutes >= 240:
        causes.append("严重延误，疑似偏航或拥堵")
    elif waybill.eta_drift_minutes > 0:
        causes.append("存在延误")
    if tracking_count == 0 and waybill.status == Waybill.STATUS_IN_TRANSIT:
        causes.append("在途但无轨迹，疑似 GPS 离线")
    if open_exceptions:
        causes.append(f"{open_exceptions} 个未关闭异常待处理")
    party = "carrier" if causes else "none"
    body = "；".join(causes) if causes else "未发现明显异常。"
    evidence = {
        "eta_drift_minutes": waybill.eta_drift_minutes,
        "tracking_points": tracking_count,
        "open_exceptions": open_exceptions,
    }
    suggestion = (
        _create_suggestion(waybill, "exception_analysis", "异常分析", body, evidence, "logistics.exception_analysis")
        if causes
        else None
    )
    return {
        "tool_name": "logistics.exception_analysis",
        "waybill_no": waybill.waybill_no,
        "possible_causes": causes,
        "responsibility_suggestion": party,
        "summary": body,
        "evidence": evidence,
        "suggestion": suggestion,
    }


@tool(
    "service.customer_reply_draft",
    "生成面向客户的运单状态回复话术（DeepSeek 可用时调用，否则模板兜底）。",
    _WAYBILL_SCHEMA,
)
def customer_reply_draft(arguments):
    from apps.ai.services.deepseek import DeepSeekClient, DeepSeekError

    waybill = _get_waybill(arguments)
    eta = waybill.estimated_arrival.isoformat() if waybill.estimated_arrival else "待定"
    context = f"运单号 {waybill.waybill_no}，线路 {waybill.route_name}，当前状态 {waybill.status}，预计到达 {eta}。"
    draft = (
        f"您好，您的运单 {waybill.waybill_no}（{waybill.route_name}）当前状态为 {waybill.status}，"
        f"预计 {eta} 送达。如有疑问请随时联系我们。"
    )
    source = "template"
    client = DeepSeekClient()
    if client.is_configured:
        try:
            resp = client.chat_completion(
                messages=[
                    {"role": "system", "content": "你是物流客服，用简洁、礼貌的中文回复客户运单状态。"},
                    {"role": "user", "content": context},
                ]
            )
            draft = resp.get("choices", [{}])[0].get("message", {}).get("content", "") or draft
            source = "deepseek"
        except DeepSeekError:
            source = "fallback"
    evidence = {"context": context, "source": source}
    suggestion = _create_suggestion(
        waybill, "customer_reply", "客服话术草稿", draft, evidence, "service.customer_reply_draft"
    )
    return {
        "tool_name": "service.customer_reply_draft",
        "waybill_no": waybill.waybill_no,
        "draft": draft,
        "source": source,
        "suggestion": suggestion,
    }
