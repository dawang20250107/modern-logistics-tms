"""基础配置：所有环境共享。环境差异放在 local.py / production.py。

配置遵循 12-Factor：一切可变项走环境变量，便于本地与腾讯云一套代码两处跑。
"""

import os
from datetime import timedelta
from pathlib import Path

import environ

# backend/ 目录
BASE_DIR = Path(__file__).resolve().parent.parent.parent

env = environ.Env()
# 本地开发若存在 backend/.env 则读取；容器内由编排注入环境变量
environ.Env.read_env(os.path.join(BASE_DIR, ".env"))

# ── 基础 ────────────────────────────────────────────────
SECRET_KEY = env("DJANGO_SECRET_KEY", default="dev-insecure-secret-change-me-min-32-bytes")
DEBUG = env.bool("DJANGO_DEBUG", default=False)
ALLOWED_HOSTS = env.list("DJANGO_ALLOWED_HOSTS", default=["127.0.0.1", "localhost"])

# ── 应用 ────────────────────────────────────────────────
DJANGO_APPS = [
    "django.contrib.admin",
    "django.contrib.auth",
    "django.contrib.contenttypes",
    "django.contrib.sessions",
    "django.contrib.messages",
    "django.contrib.staticfiles",
]
THIRD_PARTY_APPS = [
    "corsheaders",
    "rest_framework",
    "rest_framework_simplejwt.token_blacklist",
    "django_filters",
    "drf_spectacular",
    "django_celery_beat",
    "django_prometheus",
]
LOCAL_APPS = [
    "apps.core",
    "apps.accounts",
    "apps.iam",
    "apps.audit",
    "apps.masterdata",
    "apps.ops",
    "apps.finance",
    "apps.ai",
    "apps.telematics",
    "apps.analytics",
    "apps.notifications",
]
INSTALLED_APPS = DJANGO_APPS + THIRD_PARTY_APPS + LOCAL_APPS

# ── 中间件 ──────────────────────────────────────────────
MIDDLEWARE = [
    "django_prometheus.middleware.PrometheusBeforeMiddleware",
    "corsheaders.middleware.CorsMiddleware",
    "django.middleware.security.SecurityMiddleware",
    "apps.core.middleware.RequestIDMiddleware",
    "django.contrib.sessions.middleware.SessionMiddleware",
    "django.middleware.common.CommonMiddleware",
    "django.middleware.csrf.CsrfViewMiddleware",
    "django.contrib.auth.middleware.AuthenticationMiddleware",
    "django.contrib.messages.middleware.MessageMiddleware",
    "django.middleware.clickjacking.XFrameOptionsMiddleware",
    "apps.core.middleware.IdempotencyMiddleware",
    "apps.core.middleware.AccessLogMiddleware",
    "django_prometheus.middleware.PrometheusAfterMiddleware",
]

ROOT_URLCONF = "config.urls"
WSGI_APPLICATION = "config.wsgi.application"
ASGI_APPLICATION = "config.asgi.application"

TEMPLATES = [
    {
        "BACKEND": "django.template.backends.django.DjangoTemplates",
        "DIRS": [],
        "APP_DIRS": True,
        "OPTIONS": {
            "context_processors": [
                "django.template.context_processors.request",
                "django.contrib.auth.context_processors.auth",
                "django.contrib.messages.context_processors.messages",
            ],
        },
    },
]

# ── 数据库 ──────────────────────────────────────────────
DATABASES = {
    "default": env.db(
        "DATABASE_URL",
        default="postgres://tms:tms_dev_pwd@db:5432/tms",
    ),
}
DATABASES["default"]["CONN_MAX_AGE"] = env.int("DB_CONN_MAX_AGE", default=60)
DATABASES["default"]["CONN_HEALTH_CHECKS"] = True

# ── 缓存 / 会话（Redis）────────────────────────────────
REDIS_URL = env("REDIS_URL", default="redis://redis:6379/0")
CACHES = {
    "default": {
        "BACKEND": "django.core.cache.backends.redis.RedisCache",
        "LOCATION": REDIS_URL,
    },
}
SESSION_ENGINE = "django.contrib.sessions.backends.cached_db"
SESSION_CACHE_ALIAS = "default"

# ── 鉴权 ────────────────────────────────────────────────
AUTH_USER_MODEL = "accounts.User"
AUTH_PASSWORD_VALIDATORS = [
    {"NAME": "django.contrib.auth.password_validation.UserAttributeSimilarityValidator"},
    {"NAME": "django.contrib.auth.password_validation.MinimumLengthValidator"},
    {"NAME": "django.contrib.auth.password_validation.CommonPasswordValidator"},
    {"NAME": "django.contrib.auth.password_validation.NumericPasswordValidator"},
]

# ── DRF ─────────────────────────────────────────────────
REST_FRAMEWORK = {
    "DEFAULT_AUTHENTICATION_CLASSES": [
        "rest_framework_simplejwt.authentication.JWTAuthentication",
        "apps.iam.authentication.HMACAuthentication",
        "rest_framework.authentication.SessionAuthentication",
    ],
    "DEFAULT_PERMISSION_CLASSES": [
        "rest_framework.permissions.IsAuthenticated",
    ],
    "DEFAULT_RENDERER_CLASSES": [
        "apps.core.response.EnvelopeJSONRenderer",
    ],
    "DEFAULT_PARSER_CLASSES": [
        "rest_framework.parsers.JSONParser",
        "rest_framework.parsers.MultiPartParser",
        "rest_framework.parsers.FormParser",
    ],
    "DEFAULT_PAGINATION_CLASS": "apps.core.pagination.StandardResultsSetPagination",
    "PAGE_SIZE": 20,
    "DEFAULT_FILTER_BACKENDS": [
        "django_filters.rest_framework.DjangoFilterBackend",
        "rest_framework.filters.OrderingFilter",
        "rest_framework.filters.SearchFilter",
    ],
    "DEFAULT_THROTTLE_CLASSES": [
        "apps.core.throttling.BurstRateThrottle",
        "apps.core.throttling.SustainedRateThrottle",
        "apps.core.throttling.ApiKeyRateThrottle",
    ],
    "DEFAULT_THROTTLE_RATES": {
        "burst": env("THROTTLE_BURST", default="120/min"),
        "sustained": env("THROTTLE_SUSTAINED", default="3000/hour"),
        "anon": env("THROTTLE_ANON", default="60/min"),
        "apikey": env("THROTTLE_APIKEY", default="600/min"),
        "driver_login": env("THROTTLE_DRIVER_LOGIN", default="10/min"),
    },
    "EXCEPTION_HANDLER": "apps.core.exceptions.custom_exception_handler",
    "DEFAULT_SCHEMA_CLASS": "drf_spectacular.openapi.AutoSchema",
}

SIMPLE_JWT = {
    "ACCESS_TOKEN_LIFETIME": timedelta(minutes=env.int("JWT_ACCESS_MIN", default=30)),
    "REFRESH_TOKEN_LIFETIME": timedelta(days=env.int("JWT_REFRESH_DAYS", default=7)),
    "ROTATE_REFRESH_TOKENS": True,
    "BLACKLIST_AFTER_ROTATION": True,
    "UPDATE_LAST_LOGIN": True,
    "AUTH_HEADER_TYPES": ("Bearer",),
}

SPECTACULAR_SETTINGS = {
    "TITLE": "现代化物流 TMS API",
    "DESCRIPTION": "控制塔 + 运输执行引擎 + 开放 API + AI Agent 工作台",
    "VERSION": "0.1.0",
    "SERVE_INCLUDE_SCHEMA": False,
    "COMPONENT_SPLIT_REQUEST": True,
}

# ── Celery ──────────────────────────────────────────────
CELERY_BROKER_URL = env("CELERY_BROKER_URL", default="redis://redis:6379/1")
CELERY_RESULT_BACKEND = env("CELERY_RESULT_BACKEND", default="redis://redis:6379/2")
CELERY_TASK_ACKS_LATE = True
CELERY_WORKER_PREFETCH_MULTIPLIER = env.int("CELERY_PREFETCH", default=1)
CELERY_TASK_TIME_LIMIT = env.int("CELERY_TASK_TIME_LIMIT", default=300)
CELERY_TASK_SOFT_TIME_LIMIT = env.int("CELERY_TASK_SOFT_TIME_LIMIT", default=270)
CELERY_BEAT_SCHEDULE = {
    "flush-tracking-points": {"task": "ops.flush_tracking_points", "schedule": 5.0},
    "scan-eta-risks": {"task": "ops.scan_eta_risks", "schedule": 60.0},
    "scan-receipt-reminders": {"task": "ops.scan_receipt_reminders", "schedule": 120.0},
    "scan-sla-breaches": {"task": "ops.scan_sla_breaches", "schedule": 300.0},
    "flush-telemetry": {"task": "telematics.flush_telemetry", "schedule": 5.0},
    "scan-offline-devices": {"task": "telematics.scan_offline_devices", "schedule": 60.0},
    "materialize-metrics": {"task": "analytics.materialize_metrics", "schedule": 3600.0},
}

# ── CORS ────────────────────────────────────────────────
CORS_ALLOWED_ORIGINS = env.list(
    "DJANGO_CORS_ORIGINS",
    default=["http://localhost:5173", "http://127.0.0.1:5173"],
)

# ── 国际化 ──────────────────────────────────────────────
LANGUAGE_CODE = "zh-hans"
TIME_ZONE = "Asia/Shanghai"
USE_I18N = True
USE_TZ = True

# ── 静态文件 ────────────────────────────────────────────
STATIC_URL = "static/"
STATIC_ROOT = BASE_DIR / "staticfiles"

# 媒体文件（回单/凭证）。默认本地文件系统占位；
# 生产可通过 STORAGES["default"] 切换为腾讯云 COS / S3 / MinIO。
MEDIA_URL = "media/"
MEDIA_ROOT = BASE_DIR / "media"

DEFAULT_AUTO_FIELD = "django.db.models.BigAutoField"

# ── 日志（结构化）──────────────────────────────────────
LOG_FORMAT = env("LOG_FORMAT", default="json")  # json | plain
LOG_LEVEL = env("LOG_LEVEL", default="INFO")
LOGGING = {
    "version": 1,
    "disable_existing_loggers": False,
    "filters": {
        "request_id": {"()": "apps.core.logging.RequestIDFilter"},
    },
    "formatters": {
        "json": {"()": "apps.core.logging.JSONFormatter"},
        "plain": {
            "format": "%(asctime)s %(levelname)s %(name)s [rid=%(request_id)s] %(message)s",
        },
    },
    "handlers": {
        "console": {
            "class": "logging.StreamHandler",
            "filters": ["request_id"],
            "formatter": LOG_FORMAT,
        },
    },
    "root": {"handlers": ["console"], "level": LOG_LEVEL},
    "loggers": {
        "django.request": {"handlers": ["console"], "level": "WARNING", "propagate": False},
        "django.server": {"handlers": ["console"], "level": "WARNING", "propagate": False},
    },
}

# ── DeepSeek (AI) ───────────────────────────────────────
DEEPSEEK_API_KEY = env("DEEPSEEK_API_KEY", default="")
DEEPSEEK_BASE_URL = env("DEEPSEEK_BASE_URL", default="https://api.deepseek.com")
DEEPSEEK_MODEL = env("DEEPSEEK_MODEL", default="deepseek-v4-pro")
DEEPSEEK_TIMEOUT_SECONDS = env.int("DEEPSEEK_TIMEOUT_SECONDS", default=60)

# ── 可观测：慢请求阈值（毫秒，超过则日志升级为 WARNING）────────
SLOW_REQUEST_MS = env.int("SLOW_REQUEST_MS", default=800)

# ── 运满满/满帮 开放平台（调车运费比价）────────────────────
YMM_BASE_URL = env("YMM_BASE_URL", default="https://qa-open.ymm56.com")
YMM_APP_KEY = env("YMM_APP_KEY", default="")
YMM_APP_SECRET = env("YMM_APP_SECRET", default="")
YMM_ACCESS_TOKEN = env("YMM_ACCESS_TOKEN", default="")
YMM_TIMEOUT_SECONDS = env.int("YMM_TIMEOUT_SECONDS", default=8)

# ── 飞书开放平台（Bot 卡片 + 多维表格双向同步）· 预留 ──────────
FEISHU_APP_ID = env("FEISHU_APP_ID", default="")
FEISHU_APP_SECRET = env("FEISHU_APP_SECRET", default="")
FEISHU_BASE_URL = env("FEISHU_BASE_URL", default="https://open.feishu.cn")
FEISHU_BITABLE_APP_TOKEN = env("FEISHU_BITABLE_APP_TOKEN", default="")

# ── 微信接入（企业微信API / 个人微信自动化）· 预留 ────────────
WECHAT_PROVIDER = env("WECHAT_PROVIDER", default="")  # work / personal
WECHAT_CORP_ID = env("WECHAT_CORP_ID", default="")
WECHAT_CORP_SECRET = env("WECHAT_CORP_SECRET", default="")

# ── LangGraph Agent ─────────────────────────────────────
# ReAct 编排：DeepSeek（OpenAI 兼容）作为 LLM，状态持久化到 Postgres（checkpointer）。
AGENT_LLM_TEMPERATURE = env.float("AGENT_LLM_TEMPERATURE", default=0.2)
AGENT_MAX_TOOL_LOOPS = env.int("AGENT_MAX_TOOL_LOOPS", default=8)
# checkpointer 连接池（复用主库 DATABASE_URL；空则回退内存 saver，仅供无 PG 的本地/测试）
AGENT_CHECKPOINT_ENABLED = env.bool("AGENT_CHECKPOINT_ENABLED", default=True)
AGENT_CHECKPOINT_POOL_MAX = env.int("AGENT_CHECKPOINT_POOL_MAX", default=10)
# 外部 MCP server 连接表（JSON），用于后期接入大量 API/MCP 工具；空则不加载。
# 例：{"weather": {"url": "http://mcp:8000/mcp", "transport": "streamable_http"}}
AGENT_MCP_SERVERS = env.json("AGENT_MCP_SERVERS", default={})

# ── 车联网 / 报警阈值 ───────────────────────────────────
ALERT_SPEED_LIMIT_KMH = env.float("ALERT_SPEED_LIMIT_KMH", default=90.0)
ALERT_SPEED_HIGH_MARGIN = env.float("ALERT_SPEED_HIGH_MARGIN", default=20.0)  # 超限速 +N 判高危
ALERT_TEMP_MIN_C = env.float("ALERT_TEMP_MIN_C", default=-18.0)  # 冷链允许温区下限
ALERT_TEMP_MAX_C = env.float("ALERT_TEMP_MAX_C", default=8.0)  # 上限
ALERT_FUEL_LOW_PCT = env.float("ALERT_FUEL_LOW_PCT", default=15.0)  # 低油量阈值
ALERT_DEDUP_MINUTES = env.int("ALERT_DEDUP_MINUTES", default=15)  # 同车同类型报警去重窗口
DEVICE_OFFLINE_MINUTES = env.int("DEVICE_OFFLINE_MINUTES", default=10)  # 超时未上报判离线
AMAP_KEY = env("AMAP_KEY", default="")  # 高德地图 Web/JS API Key（前端实时地图用）

# ── IoT 终端网关（MQTT）─────────────────────────────────
MQTT_HOST = env("MQTT_HOST", default="mqtt")
MQTT_PORT = env.int("MQTT_PORT", default=1883)
MQTT_TOPIC = env("MQTT_TOPIC", default="tms/telemetry/#")
MQTT_USERNAME = env("MQTT_USERNAME", default="")
MQTT_PASSWORD = env("MQTT_PASSWORD", default="")
