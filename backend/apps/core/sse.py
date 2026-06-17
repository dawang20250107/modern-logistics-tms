"""Server-Sent Events 实时事件流。

EventSource 无法自定义请求头，故经 ?token=<access JWT> 鉴权。
订阅 Redis 频道并转发，业务侧通过 core.redis.publish_event 推送。
"""

import redis.asyncio as aioredis
from django.conf import settings
from django.http import HttpResponse, StreamingHttpResponse
from rest_framework_simplejwt.exceptions import TokenError
from rest_framework_simplejwt.tokens import AccessToken

from .redis import EVENT_CHANNEL


async def event_stream(request):
    token = request.GET.get("token", "")
    try:
        AccessToken(token)
    except TokenError:
        return HttpResponse("unauthorized", status=401)

    async def gen():
        client = aioredis.from_url(settings.REDIS_URL, decode_responses=True)
        pubsub = client.pubsub()
        await pubsub.subscribe(EVENT_CHANNEL)
        yield "retry: 5000\n\n"
        yield "event: ready\ndata: {}\n\n"
        try:
            async for message in pubsub.listen():
                if message.get("type") == "message":
                    yield f"data: {message['data']}\n\n"
        finally:
            await pubsub.unsubscribe(EVENT_CHANNEL)
            await client.aclose()

    response = StreamingHttpResponse(gen(), content_type="text/event-stream")
    response["Cache-Control"] = "no-cache"
    response["X-Accel-Buffering"] = "no"
    return response
