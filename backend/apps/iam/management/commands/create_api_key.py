from django.core.management.base import BaseCommand

from apps.iam.models import ApiKey


class Command(BaseCommand):
    help = "创建对外 API 密钥（HMAC）。secret 仅在此处明文输出一次。"

    def add_arguments(self, parser):
        parser.add_argument("name")
        parser.add_argument("--scopes", default="", help="逗号分隔权限点；* 表示全部")

    def handle(self, *args, **options):
        key_id, secret = ApiKey.generate_pair()
        ApiKey.objects.create(name=options["name"], key_id=key_id, secret=secret, scopes=options["scopes"])
        self.stdout.write(self.style.SUCCESS("API Key 已创建（请妥善保存 secret，仅显示一次）："))
        self.stdout.write(f"  X-Api-Key: {key_id}")
        self.stdout.write(f"  secret   : {secret}")
