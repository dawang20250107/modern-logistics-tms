"""MQTT 终端接入网关：订阅 broker，把终端上报归一化后入削峰队列。

用法：python manage.py mqtt_gateway --topic tms/telemetry/#
payload 支持 JSON 文本或 JT/T 808 二进制帧（按 topic 约定）。
"""

from django.conf import settings
from django.core.management.base import BaseCommand

from apps.telematics.gateway import ingest_terminal_report, normalize_terminal_message


class Command(BaseCommand):
    help = "订阅 MQTT broker，接入终端上报到 telemetry 队列。"

    def add_arguments(self, parser):
        parser.add_argument("--topic", default=settings.MQTT_TOPIC)
        parser.add_argument("--host", default=settings.MQTT_HOST)
        parser.add_argument("--port", type=int, default=settings.MQTT_PORT)

    def handle(self, *args, **options):
        import paho.mqtt.client as mqtt

        topic = options["topic"]

        def on_connect(client, userdata, flags, reason_code, properties=None):
            client.subscribe(topic)
            self.stdout.write(self.style.SUCCESS(f"已连接 MQTT，订阅 {topic}"))

        def on_message(client, userdata, msg):
            try:
                # JT808 帧以 0x7e 开头；否则按 JSON 文本处理
                raw = msg.payload if msg.payload[:1] == b"\x7e" else msg.payload.decode("utf-8")
                report = normalize_terminal_message(raw)
                ingest_terminal_report(report)
            except Exception as exc:  # noqa: BLE001 - 单条解析失败不应中断网关
                self.stderr.write(f"上报解析失败：{exc}")

        client = mqtt.Client()
        if settings.MQTT_USERNAME:
            client.username_pw_set(settings.MQTT_USERNAME, settings.MQTT_PASSWORD)
        client.on_connect = on_connect
        client.on_message = on_message
        client.connect(options["host"], options["port"], keepalive=60)
        self.stdout.write(f"MQTT 网关启动：{options['host']}:{options['port']}")
        client.loop_forever()
