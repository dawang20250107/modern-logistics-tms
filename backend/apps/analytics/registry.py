"""指标中台：统一指标注册表与计算入口。

统一口径：每个指标声明 code/名称/主题域/单位/支持维度，并提供计算函数；
查询层只认 code，业务计算集中在 definitions，避免口径散落各处。
"""

from apps.core.exceptions import AppError

# 主题域
DOMAIN_OPS = "ops"          # 运单/履约
DOMAIN_FLEET = "fleet"      # 运力/车辆
DOMAIN_ORDER = "order"      # 订单/渠道
DOMAIN_FINANCE = "finance"  # 财务/对账

# 指标类型
TYPE_SNAPSHOT = "snapshot"  # 即时态（当前状态，忽略时间范围）
TYPE_RANGE = "range"        # 区间累计（按时间范围）

_REGISTRY: dict = {}


def metric(code, name, domain, *, unit="", mtype=TYPE_RANGE, dimensions=None, description=""):
    def decorator(fn):
        _REGISTRY[code] = {
            "code": code,
            "name": name,
            "domain": domain,
            "unit": unit,
            "type": mtype,
            "dimensions": dimensions or [],
            "description": description,
            "fn": fn,
        }
        return fn

    return decorator


def list_metrics(domain: str | None = None) -> list[dict]:
    out = []
    for spec in _REGISTRY.values():
        if domain and spec["domain"] != domain:
            continue
        out.append({k: v for k, v in spec.items() if k != "fn"})
    return out


def compute_metric(code: str, *, start=None, end=None, dimension: str | None = None, filters: dict | None = None) -> dict:
    spec = _REGISTRY.get(code)
    if spec is None:
        raise AppError("UNKNOWN_METRIC", f"未知指标：{code}", status=404)
    if dimension and dimension not in spec["dimensions"]:
        raise AppError("INVALID_DIMENSION", f"指标 {code} 不支持维度 {dimension}", status=400)
    result = spec["fn"](start=start, end=end, dimension=dimension, filters=filters or {})
    return {
        "code": code,
        "name": spec["name"],
        "unit": spec["unit"],
        "domain": spec["domain"],
        **result,
    }
