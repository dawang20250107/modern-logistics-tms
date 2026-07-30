package agent

// MCP（Model Context Protocol）客户端：把外部 MCP server 暴露的工具接进本地工具表，
// 与内置业务工具一起进 /agent/tools 与 ReAct 循环。
//
// 对齐 apps/ai/services/mcp_tools.build_mcp_tools 的能力，但实现是从零写的：
// Django 版只是 langchain_mcp_adapters 的 30 行外壳，Go 侧没有等价库，
// 于是直接实现 Streamable HTTP 传输上的 JSON-RPC（initialize / tools/list / tools/call）。
// 这三个方法就是"接工具"所需的全部，stdio 传输与资源/提示词面暂不实现——
// 网关是个长驻服务，不该去 fork 子进程当 MCP host。
//
// 降级策略与原版一致：配置为空零开销；某个 server 不可达则记录并跳过，
// 内置工具与整个 agent 照常可用。外部依赖不该拖垮自家能力。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MCPServer 单个 server 的连接配置（AGENT_MCP_SERVERS 的值）
type MCPServer struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	// Risk 声明该 server 的工具风险等级。标 high 则这些工具不在 ReAct 自动循环里
	// 执行，只登记为待确认，人工在工具面板点了才跑。外部工具能干什么我们无从判断，
	// 接一个会改数据的 server 时务必标上。
	Risk string `json:"risk"`
}

type mcpClient struct {
	name   string
	cfg    MCPServer
	http   *http.Client
	mu     sync.Mutex
	nextID int
	sessID string
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call 发一条 JSON-RPC 请求并取回结果。
//
// Streamable HTTP 允许服务端用 application/json 直接回，也允许用 text/event-stream
// 分帧回——两种都得吃下，否则换个 server 实现就连不上。
func (c *mcpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	sess := c.sessID
	c.mu.Unlock()

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sess != "" {
		req.Header.Set("Mcp-Session-Id", sess)
	}
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.mu.Lock()
		c.sessID = s
		c.mu.Unlock()
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	payload := raw
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		payload = lastSSEData(raw)
		if payload == nil {
			return nil, fmt.Errorf("SSE 响应里没有 data 帧")
		}
	}
	var r rpcResponse
	if err := json.Unmarshal(payload, &r); err != nil {
		return nil, fmt.Errorf("响应不是合法 JSON-RPC：%w", err)
	}
	if r.Error != nil {
		return nil, fmt.Errorf("MCP 错误 %d：%s", r.Error.Code, r.Error.Message)
	}
	return r.Result, nil
}

// notify 发通知（无 id、不等结果）
func (c *mcpClient) notify(ctx context.Context, method string) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	c.mu.Lock()
	sess := c.sessID
	c.mu.Unlock()
	if sess != "" {
		req.Header.Set("Mcp-Session-Id", sess)
	}
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

// lastSSEData 取 SSE 流里最后一个 data: 帧（JSON-RPC 响应就在那里）
func lastSSEData(raw []byte) []byte {
	var last []byte
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			last = []byte(strings.TrimSpace(v))
		}
	}
	return last
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// LoadMCPTools 读配置、连接各 server、把它们的工具注册进本地工具表。
// 返回注册成功的工具数。配置为空直接返回 0（零开销 no-op）。
func LoadMCPTools(ctx context.Context, raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return 0
	}
	var servers map[string]MCPServer
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		slog.Error("AGENT_MCP_SERVERS 不是合法 JSON，已忽略", "err", err)
		return 0
	}
	total := 0
	for name, cfg := range servers {
		if cfg.URL == "" {
			slog.Warn("MCP server 缺 url，已跳过", "server", name)
			continue
		}
		n, err := loadOneMCPServer(ctx, name, cfg)
		if err != nil {
			// 降级而非报错：外部 server 不可达不该让内置工具跟着不可用
			slog.Error("加载 MCP 工具失败，已跳过该 server", "server", name, "err", err)
			continue
		}
		total += n
	}
	if total > 0 {
		slog.Info("已加载 MCP 工具", "tools", total, "servers", len(servers))
	}
	return total
}

func loadOneMCPServer(ctx context.Context, name string, cfg MCPServer) (int, error) {
	c := &mcpClient{name: name, cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if _, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tms-gateway", "version": "1.0"},
	}); err != nil {
		return 0, fmt.Errorf("initialize: %w", err)
	}
	c.notify(ctx, "notifications/initialized")

	res, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return 0, fmt.Errorf("tools/list: %w", err)
	}
	var listed struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := json.Unmarshal(res, &listed); err != nil {
		return 0, fmt.Errorf("tools/list 响应异常：%w", err)
	}

	risk := riskLow
	if strings.EqualFold(cfg.Risk, riskHigh) {
		risk = riskHigh
	}
	n := 0
	for _, t := range listed.Tools {
		if t.Name == "" {
			continue
		}
		// 前缀带 server 名：不同 server 完全可能都叫 search，撞名会静默互相覆盖
		local := "mcp." + name + "." + t.Name
		if _, dup := registryByName[local]; dup {
			slog.Warn("MCP 工具重名，已跳过", "tool", local)
			continue
		}
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		remote := t.Name
		register(ToolSpec{
			Name:            local,
			Description:     t.Description,
			InputSchema:     schema,
			Risk:            risk,
			RequiresConfirm: risk == riskHigh,
			Fn:              c.invoker(remote, local),
		})
		n++
	}
	return n, nil
}

// invoker 把一次 tools/call 包成本地工具的执行函数
func (c *mcpClient) invoker(remote, local string) func(context.Context, *pgxpool.Pool, map[string]any) (map[string]any, *toolErr) {
	return func(ctx context.Context, _ *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		res, err := c.call(callCtx, "tools/call", map[string]any{"name": remote, "arguments": args})
		if err != nil {
			return nil, &toolErr{502, "MCP_CALL_FAILED", "调用外部工具失败：" + err.Error()}
		}
		var out struct {
			Content []map[string]any `json:"content"`
			IsError bool             `json:"isError"`
		}
		_ = json.Unmarshal(res, &out)
		if out.IsError {
			return nil, &toolErr{502, "MCP_TOOL_ERROR", "外部工具返回错误：" + mcpText(out.Content)}
		}
		// 保留原始 content（结构化块，调用方可能要图片/资源），同时给一份拼好的文本
		return map[string]any{
			"tool": local, "content": out.Content, "text": mcpText(out.Content),
		}, nil
	}
}

// mcpText 把 content 块里的文本拼起来
func mcpText(content []map[string]any) string {
	var b strings.Builder
	for _, c := range content {
		if s, ok := c["text"].(string); ok {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(s)
		}
	}
	return b.String()
}
