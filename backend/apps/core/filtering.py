"""服务端通用筛选：把前端 FilterBuilder 的条件模型翻译为 Django ORM 查询。

前端模型（URL 参数 filter=<JSON>）：
    {"combinator": "and"|"or", "conditions": [{"field", "op", "value"}, ...]}

每个 ViewSet 声明 `server_filter_fields: dict[str, FilterField]`，把前端字段 key
映射到 ORM 查询路径（可多路径，用于「线路=起+讫」这类跨列文本）。派生字段先在
get_queryset 里 annotate 成列，再在此按普通列处理。用 ServerFilterMixin 接入 list。
"""

import json

from django.db.models import Q

# 运算符集合与前端 FilterBuilder.OPS 对齐
TEXT_OPS = {"contains", "ncontains", "eq", "neq", "empty", "nempty"}
ENUM_OPS = {"in", "nin"}
NUMBER_OPS = {"gt", "lt", "gte", "lte", "eq", "between"}
DATE_OPS = {"on", "after", "before", "between"}


class FilterField:
    """筛选字段定义：类型 + 一个或多个 ORM 路径。"""

    def __init__(self, ftype: str, path: str | None = None, paths: list[str] | None = None):
        self.ftype = ftype  # text | enum | number | date
        self.paths = paths if paths else ([path] if path else [])


def _num(x):
    try:
        return float(x)
    except (TypeError, ValueError):
        return None


def _pair(value):
    if isinstance(value, (list, tuple)) and len(value) >= 2:
        return value[0], value[1]
    return "", ""


def _cond_q(field: FilterField, op: str, value):
    """把单个条件翻译成 Q；无法构成有效条件时返回 None（忽略该条件）。"""
    paths = field.paths
    if not paths:
        return None
    t = field.ftype

    def any_path(lookup, val):
        q = Q()
        for p in paths:
            q |= Q(**{f"{p}{lookup}": val})
        return q

    if t == "text":
        if op in ("empty", "nempty"):
            blank = Q()
            for p in paths:
                blank &= (Q(**{p: ""}) | Q(**{f"{p}__isnull": True}))
            return blank if op == "empty" else ~blank
        v = "" if value is None else str(value)
        if v == "":
            return None
        if op == "contains":
            return any_path("__icontains", v)
        if op == "ncontains":
            return ~any_path("__icontains", v)
        if op == "eq":
            return any_path("__iexact", v)
        if op == "neq":
            return ~any_path("__iexact", v)
        return None

    if t == "enum":
        vals = value if isinstance(value, list) else ([value] if value not in (None, "") else [])
        vals = [x for x in vals if x not in (None, "")]
        if not vals:
            return None
        if op == "in":
            return any_path("__in", vals)
        if op == "nin":
            return ~any_path("__in", vals)
        return None

    if t == "bool":
        # 布尔列以「是/否」枚举暴露（前端值 "1"/"0"），把字符串安全转成 True/False
        raw = value if isinstance(value, list) else ([value] if value not in (None, "") else [])
        truthy = {"1", "true", "yes", "y", "t"}
        wanted = {str(v).strip().lower() in truthy for v in raw if v not in (None, "")}
        if not wanted:
            return None
        q = any_path("__in", list(wanted))
        return ~q if op == "nin" else q

    p = paths[0]

    if t == "number":
        if op == "between":
            lo, hi = _pair(value)
            lo, hi = _num(lo), _num(hi)
            q = Q()
            if lo is not None:
                q &= Q(**{f"{p}__gte": lo})
            if hi is not None:
                q &= Q(**{f"{p}__lte": hi})
            return q if q.children else None
        n = _num(value)
        if n is None:
            return None
        suffix = {"gt": "__gt", "lt": "__lt", "gte": "__gte", "lte": "__lte", "eq": ""}.get(op)
        return None if suffix is None else Q(**{f"{p}{suffix}": n})

    if t == "date":
        if op == "between":
            lo, hi = _pair(value)
            q = Q()
            if lo:
                q &= Q(**{f"{p}__date__gte": lo})
            if hi:
                q &= Q(**{f"{p}__date__lte": hi})
            return q if q.children else None
        if not value:
            return None
        suffix = {"on": "__date", "after": "__date__gte", "before": "__date__lte"}.get(op)
        return None if suffix is None else Q(**{f"{p}{suffix}": value})

    return None


def apply_filter_model(queryset, raw, fields: dict):
    """按前端筛选模型过滤 queryset。raw 可为 JSON 字符串或 dict；fields: {key: FilterField}。"""
    if not raw:
        return queryset
    if isinstance(raw, str):
        try:
            model = json.loads(raw)
        except (ValueError, TypeError):
            return queryset
    else:
        model = raw
    if not isinstance(model, dict):
        return queryset
    conditions = model.get("conditions") or []
    combinator = (model.get("combinator") or "and").lower()
    combined = None
    for c in conditions:
        if not isinstance(c, dict):
            continue
        f = fields.get(c.get("field"))
        if f is None:
            continue
        q = _cond_q(f, c.get("op"), c.get("value"))
        if q is None:
            continue
        if combined is None:
            combined = q
        elif combinator == "or":
            combined |= q
        else:
            combined &= q
    if combined is not None:
        queryset = queryset.filter(combined).distinct()
    return queryset


class ServerFilterMixin:
    """在 DRF ViewSet 的 filter_queryset 后再应用 FilterBuilder 的 filter=<JSON> 参数。

    ViewSet 声明 server_filter_fields = {key: FilterField(...)} 即可。
    与 DRF 的 SearchFilter(search=)/OrderingFilter(ordering=)/分页天然叠加。
    """

    server_filter_fields: dict = {}

    def filter_queryset(self, queryset):
        queryset = super().filter_queryset(queryset)
        raw = self.request.query_params.get("filter")
        if raw and self.server_filter_fields:
            queryset = apply_filter_model(queryset, raw, self.server_filter_fields)
        return queryset
