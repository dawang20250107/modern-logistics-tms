import pytest
from django.core.cache import cache


@pytest.fixture(autouse=True)
def _isolate_cache():
    """每个测试前后清空缓存，保证幂等/限流等用例相互隔离。"""
    cache.clear()
    yield
    cache.clear()
