"""Agent 工具注册表。

原则：模型只能"请求"工具，业务逻辑由服务端执行并返回证据；高风险动作不在工具内自动完成。
每个工具产出 evidence（可追溯输入与依据），命中风险时落 AgentSuggestion 供人工确认。
"""

from decimal import Decimal

from django.db.models import Sum

from apps.core.exceptions import AppError

_REGISTRY: dict = {}

# 风险分级（分级闸门）：
# - "low"：只读/分析/落建议等不直接变更核心业务状态的动作，agent 可自动执行；
# - "high"：会真正写入/执行高风险动作（改运单状态、发起付款、对外承诺等），
#   必须经人工确认闭环（落 AgentSuggestion 等待 confirm），agent 不得自动落地。
RISK_LOW = "low"
RISK_HIGH = "high"


def tool(name: str, description: str, input_schema: dict, risk: str = RISK_LOW):
    def decorator(fn):
        _REGISTRY[name] = {
            "name": name,
            "description": description,
            "input_schema": input_schema,
            "risk": risk,
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


@tool(
    "telematics.vehicle_alert_summary",
    "汇总运单关联车辆的未处理车联网报警（超速/温度/油量/离线/疲劳等），用于风险归因。",
    _WAYBILL_SCHEMA,
)
def vehicle_alert_summary(arguments):
    from apps.telematics.models import Alert

    waybill = _get_waybill(arguments)
    alerts = list(
        Alert.objects.filter(waybill=waybill, status=Alert.STATUS_OPEN).order_by("-triggered_at")[:20]
    )
    by_type: dict = {}
    for a in alerts:
        by_type[a.alert_type] = by_type.get(a.alert_type, 0) + 1
    high = [a for a in alerts if a.level == Alert.LEVEL_HIGH]
    evidence = {
        "waybill_no": waybill.waybill_no,
        "open_alert_count": len(alerts),
        "high_count": len(high),
        "by_type": by_type,
        "recent": [
            {"type": a.alert_type, "level": a.level, "message": a.message, "at": a.triggered_at.isoformat()}
            for a in alerts[:5]
        ],
    }
    risk_detected = bool(high)
    if alerts:
        body = f"{waybill.waybill_no} 关联车辆有 {len(alerts)} 条未处理报警（高危 {len(high)}）：{by_type}。"
    else:
        body = f"{waybill.waybill_no} 关联车辆当前无未处理报警。"
    suggestion = (
        _create_suggestion(
            waybill, "vehicle_alert", "车辆报警待核实", body, evidence, "telematics.vehicle_alert_summary"
        )
        if risk_detected
        else None
    )
    return {
        "tool_name": "telematics.vehicle_alert_summary",
        "waybill_no": waybill.waybill_no,
        "risk_detected": risk_detected,
        "summary": body,
        "evidence": evidence,
        "suggestion": suggestion,
    }


@tool(
    "analytics.query_metric",
    "查询经营/运营指标（运单量/在途/准时率/风险率/运力在线率/利用率/报警数/订单量/转化率/应收/应付/对账差异等）。",
    {
        "type": "object",
        "required": ["metric_code"],
        "properties": {
            "metric_code": {"type": "string", "description": "指标 code，如 ops.on_time_rate"},
            "days": {"type": "integer", "description": "统计区间天数，默认 30"},
        },
    },
)
def query_metric(arguments):
    from datetime import timedelta

    from django.utils import timezone

    from apps.analytics.registry import compute_metric

    code = arguments["metric_code"]
    days = arguments.get("days") or 30
    end = timezone.now().date()
    start = end - timedelta(days=days)
    result = compute_metric(code, start=start, end=end)
    return {
        "tool_name": "analytics.query_metric",
        "metric_code": code,
        "value": result["value"],
        "unit": result.get("unit", ""),
        "summary": f"{result['name']}（近{days}天）= {result['value']}{result.get('unit', '')}",
        "evidence": result,
        "suggestion": None,
    }


_EXCEPTION_SCHEMA = {
    "type": "object",
    "properties": {
        "waybill_no": {"type": "string", "description": "关联运单号（若有）"},
        "exception_type": {"type": "string", "description": "异常类型，如 'deviation', 'temperature', 'fuel'"}
    }
}


@tool(
    "logistics.exception_handler",
    "调阅该运单真实的车联网报警、轨迹与未闭环异常，对突发异常（偏航/温控/油损等）做数据回溯，并给出处置建议。",
    _EXCEPTION_SCHEMA,
)
def exception_handler(arguments):
    """基于真实数据回溯异常。绝不编造传感器读数：无数据即如实说明，处置建议为 SOP 参考而非既成事实。"""
    from apps.ops.models import Waybill
    from apps.telematics.models import Alert

    waybill_no = arguments.get("waybill_no")
    exc_type = (arguments.get("exception_type") or "").strip()

    waybill = (
        Waybill.objects.select_related("vehicle", "driver").filter(waybill_no=waybill_no).first()
        if waybill_no
        else None
    )
    if waybill is None:
        return {
            "tool_name": "logistics.exception_handler",
            "diagnosis": f"未找到运单 {waybill_no or '（未提供单号）'} 的可回溯数据，无法基于真实轨迹/设备做诊断。",
            "mitigation_plan": "请补充有效运单号后重试，或转人工核实。",
            "evidence": {"waybill_no": waybill_no, "exception_type": exc_type, "data_available": False},
            "summary": "数据不足，未做诊断（不编造结论）。",
        }

    # 真实证据：按异常类型过滤该运单的车联网报警 + 轨迹 + ETA 偏移 + 未闭环异常
    alert_qs = Alert.objects.filter(waybill=waybill).order_by("-triggered_at")
    typed_qs = alert_qs.filter(alert_type=exc_type) if exc_type else alert_qs
    typed = list(typed_qs[:5])
    tracking_count = waybill.tracking_points.count()
    latest = waybill.tracking_points.order_by("-reported_at").first()
    open_exc = waybill.exceptions.exclude(status="closed").count()

    findings = []
    for a in typed:
        piece = a.message or a.get_alert_type_display()
        if a.value is not None and a.threshold is not None:
            piece += f"（实测 {a.value}/阈值 {a.threshold}）"
        findings.append(f"{a.triggered_at:%m-%d %H:%M} {piece}[{a.get_level_display()}]")

    parts = [f"运单 {waybill.waybill_no}（{waybill.route_name}）异常回溯："]
    if findings:
        parts.append("车联网记录到以下真实报警——" + "；".join(findings) + "。")
    else:
        label = dict(Alert._meta.get_field("alert_type").choices).get(exc_type, exc_type or "该类")
        parts.append(f"未在车联网报警库检索到「{label}」类型的报警记录。")
    if latest is not None:
        parts.append(f"最新轨迹点 {latest.reported_at:%m-%d %H:%M}（共 {tracking_count} 个点）。")
    elif waybill.status == Waybill.STATUS_IN_TRANSIT:
        parts.append("在途但暂无轨迹上报，疑似设备离线，请优先核实定位。")
    if waybill.eta_drift_minutes:
        parts.append(f"当前 ETA 偏移 {round(waybill.eta_drift_minutes / 60, 1)} 小时。")
    if open_exc:
        parts.append(f"另有 {open_exc} 个未闭环异常待处理。")

    mitigation = (
        "以下为处置 SOP 建议（尚未执行，需人工确认）：\n"
        "1. 电话联系司机核实现场情况（勿仅发短信）。\n"
        "2. 结合上述真实报警/定位判断责任与紧急度，必要时要求靠边停车检查。\n"
        "3. 如影响时效，向客户同步最新 ETA；如涉及货损/油损，登记定损单留证。"
    )
    evidence = {
        "waybill_no": waybill.waybill_no,
        "exception_type": exc_type,
        "data_available": True,
        "matched_alerts": len(typed),
        "tracking_points": tracking_count,
        "eta_drift_minutes": waybill.eta_drift_minutes,
        "open_exceptions": open_exc,
    }
    return {
        "tool_name": "logistics.exception_handler",
        "diagnosis": "".join(parts),
        "mitigation_plan": mitigation,
        "evidence": evidence,
        "summary": (
            f"已基于真实数据回溯：命中 {len(typed)} 条{('「' + exc_type + '」') if exc_type else ''}报警、"
            f"{tracking_count} 个轨迹点。处置建议见 mitigation_plan（未自动执行）。"
        ),
    }


_CONSOLIDATION_SCHEMA = {
    "type": "object",
    "properties": {
        "city_filter": {"type": "string", "description": "可选：指定始发或目的城市名称（如'无锡'），过滤匹配的拼单建议"}
    }
}


@tool(
    "logistics.intelligent_consolidation",
    "运行智能 B2B 拼单配载与最省算路算法，将同向 LTL 小单合并配载 FTL 卡车，输出降本方案与预计节省金额。",
    _CONSOLIDATION_SCHEMA,
)
def intelligent_consolidation(arguments):
    from apps.ops.dispatch import consolidate_and_group_orders
    from apps.ops.models import Order

    city = arguments.get("city_filter")
    qs = Order.objects.filter(status__in=[Order.STATUS_POOLED, Order.STATUS_DISPATCHING])
    if city:
        from django.db.models import Q
        qs = qs.filter(Q(origin__icontains=city) | Q(destination__icontains=city))

    orders = list(qs)
    res = consolidate_and_group_orders(orders)

    summary = (
        f"拼单配载引擎扫描了在池订单，生成 {res['consolidated_count']} 个同向合并推荐；"
        f"按运价估算模型测算，预计可节省运费约 {res['estimated_total_saving']} 元（估算值，以实际询价为准）。"
    )

    return {
        "tool_name": "logistics.intelligent_consolidation",
        "summary": summary,
        "consolidated_count": res["consolidated_count"],
        "unassigned_count": res["unassigned_count"],
        "consolidated_trips": res["consolidated_trips"],
        "unassigned_orders": res["unassigned_orders"],
        "estimated_total_saving": res["estimated_total_saving"],
    }
