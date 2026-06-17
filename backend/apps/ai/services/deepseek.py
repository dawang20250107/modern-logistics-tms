"""DeepSeek V4（OpenAI 兼容）客户端。

httpx 实现，含超时与网络重试。未配置 API Key 时返回明确错误，不外呼。
"""

import time

import httpx
from django.conf import settings


class DeepSeekError(Exception):
    def __init__(self, code: str, message: str, status: int = 502):
        super().__init__(message)
        self.code = code
        self.message = message
        self.status = status


class DeepSeekClient:
    def __init__(self):
        self.api_key = settings.DEEPSEEK_API_KEY
        self.base_url = settings.DEEPSEEK_BASE_URL.rstrip("/")
        self.default_model = settings.DEEPSEEK_MODEL
        self.timeout = settings.DEEPSEEK_TIMEOUT_SECONDS

    @property
    def is_configured(self) -> bool:
        return bool(self.api_key)

    def status(self) -> dict:
        return {
            "provider": "deepseek",
            "configured": self.is_configured,
            "base_url": self.base_url,
            "model": self.default_model,
            "chat_path": "/chat/completions",
        }

    def chat_completion(self, messages, model=None, temperature=None, stream=False, max_retries=2):
        if not self.is_configured:
            raise DeepSeekError("DEEPSEEK_NOT_CONFIGURED", "DEEPSEEK_API_KEY 未配置。", status=503)

        payload = {"model": model or self.default_model, "messages": messages, "stream": stream}
        if temperature is not None:
            payload["temperature"] = temperature

        headers = {"Authorization": f"Bearer {self.api_key}", "Content-Type": "application/json"}
        url = f"{self.base_url}/chat/completions"

        last_exc = None
        for attempt in range(max_retries + 1):
            try:
                with httpx.Client(timeout=self.timeout) as client:
                    resp = client.post(url, json=payload, headers=headers)
                resp.raise_for_status()
                return resp.json()
            except httpx.HTTPStatusError as exc:
                # 4xx 不重试，5xx 重试
                if exc.response.status_code < 500 or attempt == max_retries:
                    raise DeepSeekError(
                        "DEEPSEEK_HTTP_ERROR", exc.response.text, status=exc.response.status_code
                    ) from exc
                last_exc = exc
            except httpx.HTTPError as exc:
                if attempt == max_retries:
                    raise DeepSeekError("DEEPSEEK_NETWORK_ERROR", str(exc), status=502) from exc
                last_exc = exc
            time.sleep(0.5 * (attempt + 1))

        raise DeepSeekError("DEEPSEEK_NETWORK_ERROR", str(last_exc), status=502)
