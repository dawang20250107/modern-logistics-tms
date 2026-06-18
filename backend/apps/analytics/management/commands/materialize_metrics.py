"""手动物化指标快照：python manage.py materialize_metrics [--date YYYY-MM-DD]"""

from django.core.management.base import BaseCommand
from django.utils.dateparse import parse_date


class Command(BaseCommand):
    help = "把当日（或指定日）核心指标落物化快照。"

    def add_arguments(self, parser):
        parser.add_argument("--date", default=None)

    def handle(self, *args, **options):
        from apps.analytics.services import materialize_daily

        day = parse_date(options["date"]) if options["date"] else None
        count = materialize_daily(day)
        self.stdout.write(self.style.SUCCESS(f"已物化 {count} 个指标。"))
