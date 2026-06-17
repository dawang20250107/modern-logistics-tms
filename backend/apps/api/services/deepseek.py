import json
import urllib.error
import urllib.request

from django.conf import settings


class DeepSeekError(Exception):
    def __init__(self, code, message, status=502):
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
    def is_configured(self):
        return bool(self.api_key)

    def status(self):
        return {
            "provider": "deepseek",
            "configured": self.is_configured,
            "base_url": self.base_url,
            "model": self.default_model,
            "chat_path": "/chat/completions",
        }

    def chat_completion(self, messages, model=None, thinking=None, reasoning_effort=None, stream=False):
        if not self.is_configured:
            raise DeepSeekError("DEEPSEEK_NOT_CONFIGURED", "DEEPSEEK_API_KEY is not configured.", status=503)

        payload = {
            "model": model or self.default_model,
            "messages": messages,
            "stream": stream,
        }
        if thinking:
            payload["thinking"] = thinking
        if reasoning_effort:
            payload["reasoning_effort"] = reasoning_effort

        request = urllib.request.Request(
            f"{self.base_url}/chat/completions",
            data=json.dumps(payload).encode("utf-8"),
            headers={
                "Authorization": f"Bearer {self.api_key}",
                "Content-Type": "application/json",
            },
            method="POST",
        )

        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                body = response.read().decode("utf-8")
                return json.loads(body)
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            raise DeepSeekError("DEEPSEEK_HTTP_ERROR", body or exc.reason, status=exc.code) from exc
        except urllib.error.URLError as exc:
            raise DeepSeekError("DEEPSEEK_NETWORK_ERROR", str(exc.reason), status=502) from exc
        except json.JSONDecodeError as exc:
            raise DeepSeekError("DEEPSEEK_BAD_RESPONSE", "DeepSeek returned non-JSON response.", status=502) from exc
