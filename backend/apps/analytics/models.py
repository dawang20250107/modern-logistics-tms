"""指标物化快照：按日落指标值，支撑趋势查询与看板加速（物化层精简版）。"""

from django.db import models

from apps.core.models import BaseModel


class MetricSnapshot(BaseModel):
    metric_code = models.CharField(max_length=64, db_index=True)
    stat_date = models.DateField(db_index=True)
    dimension_key = models.CharField(max_length=64, blank=True, default="", help_text="维度取值，空为总量")
    value = models.DecimalField(max_digits=18, decimal_places=4, default=0)

    class Meta:
        db_table = "ana_metric_snapshot"
        unique_together = [("metric_code", "stat_date", "dimension_key")]
        ordering = ["-stat_date"]
        verbose_name = "指标快照"
        verbose_name_plural = "指标快照"

    def __str__(self) -> str:
        return f"{self.metric_code}@{self.stat_date}={self.value}"
