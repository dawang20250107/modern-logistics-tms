"""数据资产目录（数据治理 lite）：自省业务模型，输出表/字段/记录数元数据。

为数据中台提供"资产可见"能力：哪些数据域、有哪些表与字段、量级如何。
"""

# 纳入目录的业务域（排除 Django 内置与三方）
DOMAIN_APPS = {
    "accounts", "iam", "audit", "masterdata", "ops",
    "finance", "ai", "telematics", "analytics", "notifications",
}

DOMAIN_LABEL = {
    "accounts": "账号", "iam": "权限/组织", "audit": "审计", "masterdata": "主数据",
    "ops": "运单/订单", "finance": "财务", "ai": "AI", "telematics": "车联网",
    "analytics": "数据中台", "notifications": "通知",
}


def list_data_assets(*, with_counts: bool = False) -> list[dict]:
    from django.apps import apps as django_apps

    assets = []
    for model in django_apps.get_models():
        app = model._meta.app_label
        if app not in DOMAIN_APPS:
            continue
        fields = [
            {
                "name": f.name,
                "type": f.get_internal_type(),
                "help": str(getattr(f, "help_text", "") or ""),
            }
            for f in model._meta.get_fields()
            if hasattr(f, "get_internal_type")
        ]
        asset = {
            "app": app,
            "domain": DOMAIN_LABEL.get(app, app),
            "model": model.__name__,
            "table": model._meta.db_table,
            "verbose_name": str(model._meta.verbose_name),
            "field_count": len(fields),
            "fields": fields,
        }
        if with_counts:
            try:
                asset["row_count"] = model._base_manager.count()
            except Exception:  # noqa: BLE001
                asset["row_count"] = None
        assets.append(asset)
    assets.sort(key=lambda a: (a["app"], a["model"]))
    return assets
