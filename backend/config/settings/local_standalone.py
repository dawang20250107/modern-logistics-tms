"""裸机本地开发配置（无 docker 依赖）。

用于 Go 网关绞杀迁移期把 Django 作为上游跑在本机：
- 数据库直连本机 PostgreSQL（不依赖 compose 的 `db` 主机名）
- 缓存用进程内 LocMem（不依赖 `redis` 主机名；节流/缓存功能不受影响）
- CORS 放行 vite dev(:5173) 与 preview(:4173)

用法：python3 manage.py runserver 127.0.0.1:8001 --settings=config.settings.local_standalone
（配套 Go 网关启动方式见 backend-go/PORTING.md）
"""

from .local import *  # noqa: F401,F403

CORS_ALLOWED_ORIGINS = [
    "http://localhost:5173",
    "http://127.0.0.1:5173",
    "http://localhost:4173",
    "http://127.0.0.1:4173",
]

DATABASES["default"]["HOST"] = "127.0.0.1"  # noqa: F405
DATABASES["default"]["PORT"] = "5432"

CACHES = {
    "default": {
        "BACKEND": "django.core.cache.backends.locmem.LocMemCache",
        "LOCATION": "local-standalone",
    }
}
