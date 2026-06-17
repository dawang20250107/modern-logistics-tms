"""本地开发配置。"""

from .base import *  # noqa: F401,F403
from .base import REST_FRAMEWORK

DEBUG = True
ALLOWED_HOSTS = ["*"]

# 开发期开启可浏览 API
REST_FRAMEWORK["DEFAULT_RENDERER_CLASSES"] = [
    *REST_FRAMEWORK["DEFAULT_RENDERER_CLASSES"],
    "rest_framework.renderers.BrowsableAPIRenderer",
]
