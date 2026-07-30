package agent

// DeepSeek（OpenAI 兼容）客户端 —— 替代 Python 侧的 langchain_openai.ChatOpenAI。
// 只依赖标准库 net/http：请求体就是 OpenAI chat.completions 契约，
// 工具声明直接透传各工具自带的 JSON Schema（无需 pydantic 适配层）。
//
// 刻意放在 Client 接口之后：将来若要外挂独立推理服务（本地模型/RAG），
// 只需换一个实现，Agent 循环与工具层无需改动。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type functionDecl struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type Client struct {
	APIKey      string
	BaseURL     string
	Model       string
	Temperature float64
	Timeout     time.Duration
	MaxRetries  int
	HTTP        *http.Client
}

var defaultClient *Client

// DefaultClient 从环境变量装配（与 Django settings 同名，便于双栈共用配置）
func DefaultClient() *Client {
	if defaultClient != nil {
		return defaultClient
	}
	timeout := 60
	if v, err := strconv.Atoi(os.Getenv("DEEPSEEK_TIMEOUT_SECONDS")); err == nil && v > 0 {
		timeout = v
	}
	temp := 0.2
	if v, err := strconv.ParseFloat(os.Getenv("AGENT_LLM_TEMPERATURE"), 64); err == nil {
		temp = v
	}
	base := os.Getenv("DEEPSEEK_BASE_URL")
	if base == "" {
		base = "https://api.deepseek.com"
	}
	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		model = "deepseek-v4-pro"
	}
	defaultClient = &Client{
		APIKey: os.Getenv("DEEPSEEK_API_KEY"), BaseURL: trimSlash(base), Model: model,
		Temperature: temp, Timeout: time.Duration(timeout) * time.Second, MaxRetries: 2,
		HTTP: &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}
	return defaultClient
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func (c *Client) IsConfigured() bool { return c.APIKey != "" }

// Status 对齐 /ai/deepseek/status
func (c *Client) Status() map[string]any {
	return map[string]any{
		"provider": "deepseek", "configured": c.IsConfigured(),
		"base_url": c.BaseURL, "model": c.Model, "chat_path": "/chat/completions",
	}
}

var ErrNotConfigured = errors.New("DEEPSEEK_NOT_CONFIGURED")

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage map[string]any `json:"usage"`
}

// Chat 一次补全；tools 非空时声明可调用工具（OpenAI function calling 契约）
func (c *Client) Chat(ctx context.Context, messages []Message, tools []functionDecl) (*Message, error) {
	if !c.IsConfigured() {
		return nil, ErrNotConfigured
	}
	payload := map[string]any{
		"model": c.Model, "messages": messages, "temperature": c.Temperature, "stream": false,
	}
	if len(tools) > 0 {
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	body, _ := json.Marshal(payload)

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.BaseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue // 网络类错误重试
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("上游 %d：%s", resp.StatusCode, truncate(string(raw), 200))
			continue // 5xx 重试
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("上游 %d：%s", resp.StatusCode, truncate(string(raw), 200))
		}
		var out chatResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("上游响应解析失败：%w", err)
		}
		if len(out.Choices) == 0 {
			return nil, errors.New("上游未返回 choices")
		}
		msg := out.Choices[0].Message
		return &msg, nil
	}
	return nil, lastErr
}

// SimpleChat 无工具的一轮问答（客服话术等场景）
func (c *Client) SimpleChat(ctx context.Context, system, user string) (string, error) {
	msg, err := c.Chat(ctx, []Message{
		{Role: "system", Content: system}, {Role: "user", Content: user},
	}, nil)
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
