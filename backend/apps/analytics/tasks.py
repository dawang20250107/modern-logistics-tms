"""指标物化定时任务。"""

from celery import shared_task


@shared_task(name="analytics.materialize_metrics")
def materialize_metrics() -> int:
    from .services import materialize_daily

    return materialize_daily()
