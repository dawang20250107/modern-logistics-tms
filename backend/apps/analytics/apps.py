from django.apps import AppConfig


class AnalyticsConfig(AppConfig):
    default_auto_field = "django.db.models.BigAutoField"
    name = "apps.analytics"
    verbose_name = "数据中台 / 指标"

    def ready(self):
        from . import definitions  # noqa: F401 - 注册指标
