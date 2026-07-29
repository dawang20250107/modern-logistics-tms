package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeMCP 一个最小 MCP server：支持 initialize / tools/list / tools/call。
// sse 为 true 时用 text/event-stream 回，验证两种响应形态都吃得下。
func fakeMCP(t *testing.T, sse bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			result = map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "fake"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "echo", "description": "回声",
				"inputSchema": map[string]any{
					"type": "object", "required": []any{"text"},
					"properties": map[string]any{"text": map[string]any{"type": "string"}},
				},
			}}}
		case "tools/call":
			args, _ := req.Params["arguments"].(map[string]any)
			result = map[string]any{"content": []map[string]any{
				{"type": "text", "text": fmt.Sprint(args["text"])}}}
		default:
			result = map[string]any{}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
}

func TestLoadMCPToolsJSONTransport(t *testing.T) {
	srv := fakeMCP(t, false)
	defer srv.Close()

	cfg := fmt.Sprintf(`{"demo":{"url":%q}}`, srv.URL)
	if n := LoadMCPTools(context.Background(), cfg); n != 1 {
		t.Fatalf("应注册 1 个工具，实得 %d", n)
	}
	spec, ok := registryByName["mcp.demo.echo"]
	if !ok {
		t.Fatal("工具未按 mcp.<server>.<tool> 命名注册")
	}
	if spec.Risk != riskLow {
		t.Errorf("默认风险应为 low，实得 %s", spec.Risk)
	}

	// 走公共入口，顺带验证必填参数校验对 MCP 工具同样生效
	if _, e := ExecuteTool(context.Background(), nil, "mcp.demo.echo", map[string]any{}); e == nil {
		t.Error("缺必填参数应报错")
	}
	out, e := ExecuteTool(context.Background(), nil, "mcp.demo.echo", map[string]any{"text": "你好"})
	if e != nil {
		t.Fatalf("调用失败：%+v", e)
	}
	if out["text"] != "你好" {
		t.Errorf("返回文本不对：%v", out["text"])
	}
}

func TestLoadMCPToolsSSETransportAndRisk(t *testing.T) {
	srv := fakeMCP(t, true)
	defer srv.Close()

	cfg := fmt.Sprintf(`{"stream":{"url":%q,"risk":"high"}}`, srv.URL)
	if n := LoadMCPTools(context.Background(), cfg); n != 1 {
		t.Fatalf("SSE 传输应注册 1 个工具，实得 %d", n)
	}
	spec := registryByName["mcp.stream.echo"]
	if spec == nil || spec.Risk != riskHigh {
		t.Fatalf("配置 risk=high 未生效：%+v", spec)
	}
	out, e := ExecuteTool(context.Background(), nil, "mcp.stream.echo", map[string]any{"text": "sse"})
	if e != nil {
		t.Fatalf("SSE 调用失败：%+v", e)
	}
	if out["text"] != "sse" {
		t.Errorf("SSE 返回文本不对：%v", out["text"])
	}
}

// server 连不上必须降级：返回 0，不 panic，内置工具数量不受影响。
func TestLoadMCPToolsDegradesGracefully(t *testing.T) {
	before := len(registry)
	cases := []string{
		``,                                       // 未配置
		`{}`,                                     // 空配置
		`not json`,                               // 配置本身非法
		`{"x":{}}`,                               // 缺 url
		`{"dead":{"url":"http://127.0.0.1:1/"}}`, // 连不上
	}
	for _, c := range cases {
		if n := LoadMCPTools(context.Background(), c); n != 0 {
			t.Errorf("配置 %q 应加载 0 个工具，实得 %d", c, n)
		}
	}
	if len(registry) != before {
		t.Errorf("降级路径不该改动工具表：%d → %d", before, len(registry))
	}
}
