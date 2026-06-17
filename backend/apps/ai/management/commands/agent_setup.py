"""初始化 LangGraph Agent：创建 Postgres checkpointer 表，并在已配置 LLM 时自检图编译。

用法：python manage.py agent_setup
部署时随 migrate 一并执行即可（幂等）。未配置 DEEPSEEK_API_KEY 时仅建表，不报错。
"""

from django.conf import settings
from django.core.management.base import BaseCommand


class Command(BaseCommand):
    help = "初始化 LangGraph Agent 的 Postgres 状态表，并校验图可编译。"

    def handle(self, *args, **options):
        from apps.ai.services import agent_graph

        agent_graph.reset_agent_graph()

        # 先确保 checkpointer 表就绪（与 LLM 是否配置无关）
        checkpointer = agent_graph._build_checkpointer()
        self.stdout.write(self.style.SUCCESS(f"checkpointer 就绪：{type(checkpointer).__name__}"))

        if not settings.DEEPSEEK_API_KEY:
            self.stdout.write(
                self.style.WARNING("DEEPSEEK_API_KEY 未配置，跳过图编译自检（配置后 Agent 即可用）。")
            )
            return

        agent_graph.get_agent_graph()
        self.stdout.write(self.style.SUCCESS("LangGraph Agent 已就绪（状态表已创建，图已编译）。"))
