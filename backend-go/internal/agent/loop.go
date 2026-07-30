package agent

// ReAct 循环 —— 替代 LangGraph 的 START → agent ⇄ tools → END 拓扑。
//
// 该拓扑本质就是「问 LLM → 若要调工具则执行并回灌 → 再问」的有界循环，
// 没有分支/并行/子图/人工中断，因此这里用一个 for 循环表达即可，
// 且并行工具调用天然用 goroutine（LangGraph 的 ToolNode 是串行的）。
//
// 多轮对话状态：按 thread_id 落 ai_agent_thread_message 表（等价于
// LangGraph 的 Postgres checkpointer，但只存消息，不存图状态快照）。

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const systemPrompt = "你是现代化物流 TMS 平台的智能助手，服务于控制塔与运营/财务/客服团队。" +
	"你可以调用工具分析运单的 ETA 风险、回单催收、费用风控、调度建议、异常归因，并草拟客服话术。" +
	"原则：" +
	"1) 高风险动作（派车、放款、对外承诺等）只给建议、绝不自动执行，需人工确认；" +
	"2) 结论必须基于工具返回的 evidence（证据链），不要臆测数据；" +
	"3) 用简洁专业的中文回复，必要时给出明确的下一步建议（next_actions）。" +
	"当用户提供运单号时，优先调用相应工具获取实据再作答。"

func maxToolLoops() int {
	if v, err := strconv.Atoi(os.Getenv("AGENT_MAX_TOOL_LOOPS")); err == nil && v > 0 {
		return v
	}
	return 8
}

// 工具名规范化：OpenAI function name 只允许 [a-zA-Z0-9_-]，注册表用点号命名
func normalizeName(n string) string   { return strings.ReplaceAll(n, ".", "__") }
func denormalizeName(n string) string { return strings.ReplaceAll(n, "__", ".") }

func toolDecls() []functionDecl {
	out := make([]functionDecl, 0, len(registry))
	for _, s := range registry {
		var d functionDecl
		d.Type = "function"
		d.Function.Name = normalizeName(s.Name)
		d.Function.Description = s.Description
		if s.Risk == riskHigh {
			d.Function.Description += "（高风险：仅产出建议，需人工确认后才会真正执行，不可自动落地。）"
		}
		d.Function.Parameters = s.InputSchema
		out = append(out, d)
	}
	return out
}

// RunResult 一轮问答的产出（对齐 Python run_agent 的返回结构）
type RunResult struct {
	ThreadID    string           `json:"thread_id"`
	Answer      string           `json:"answer"`
	ToolCalls   []map[string]any `json:"tool_calls"`
	Suggestions []map[string]any `json:"suggestions"`
}

// Run 执行一轮对话：载入历史 → LLM ⇄ 工具循环 → 落库新消息
func Run(ctx context.Context, db *pgxpool.Pool, message, threadID string) (*RunResult, error) {
	if threadID == "" {
		threadID = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	client := DefaultClient()
	if !client.IsConfigured() {
		return nil, ErrNotConfigured
	}

	history, err := loadThread(ctx, db, threadID)
	if err != nil {
		return nil, err
	}
	msgs := append([]Message{{Role: "system", Content: systemPrompt}}, history...)
	msgs = append(msgs, Message{Role: "user", Content: message})
	newMsgs := []Message{{Role: "user", Content: message}}

	decls := toolDecls()
	toolCalls := []map[string]any{}
	suggestions := []map[string]any{}
	answer := ""

	for loop := 0; loop < maxToolLoops(); loop++ {
		reply, err := client.Chat(ctx, msgs, decls)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, *reply)
		newMsgs = append(newMsgs, *reply)

		if len(reply.ToolCalls) == 0 {
			answer = reply.Content
			break
		}

		// 并行执行本轮命中的所有工具（LangGraph 的 ToolNode 是串行的）
		results := make([]Message, len(reply.ToolCalls))
		artifacts := make([]map[string]any, len(reply.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range reply.ToolCalls {
			wg.Add(1)
			go func(i int, tc ToolCall) {
				defer wg.Done()
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				name := denormalizeName(tc.Function.Name)
				if spec, ok := registryByName[name]; ok && spec.RequiresConfirm {
					// 声明为待确认的工具不在自动循环里执行。把参数原样回给模型，
					// 让它据此把"要做什么"讲清楚，人再决定跑不跑。
					aj, _ := json.Marshal(args)
					artifacts[i] = map[string]any{
						"tool_name": name, "requires_confirm": true, "arguments": args,
						"summary": name + " 已声明为高风险，需人工确认后执行，本轮未执行。",
					}
					results[i] = Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name,
						Content: name + " 需人工确认，未执行。拟用参数：" + string(aj)}
					return
				}
				res, terr := ExecuteTool(ctx, db, name, args)
				var content string
				if terr != nil {
					content = terr.Message
				} else {
					artifacts[i] = res
					if s, ok := res["summary"].(string); ok && s != "" {
						content = s
					} else {
						content = name + " 已执行。"
					}
				}
				results[i] = Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: content}
			}(i, tc)
		}
		wg.Wait()

		for i, m := range results {
			msgs = append(msgs, m)
			newMsgs = append(newMsgs, m)
			if a := artifacts[i]; a != nil {
				toolCalls = append(toolCalls, map[string]any{
					"tool_name": a["tool_name"], "waybill_no": a["waybill_no"],
					"summary": a["summary"], "risk_detected": a["risk_detected"], "evidence": a["evidence"],
				})
				if sg, ok := a["suggestion"].(map[string]any); ok && sg != nil {
					suggestions = append(suggestions, sg)
				}
			}
		}
	}

	if err := saveThread(ctx, db, threadID, newMsgs); err != nil {
		return nil, err
	}
	return &RunResult{ThreadID: threadID, Answer: answer, ToolCalls: toolCalls, Suggestions: suggestions}, nil
}

// ── 会话状态持久化（等价 LangGraph checkpointer，只存消息）──

// EnsureSchema 建会话消息表（幂等；schema 所有权移交前与 Django 表并存互不干扰）
func EnsureSchema(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS ai_agent_thread_message (
		  id          uuid PRIMARY KEY,
		  thread_id   text        NOT NULL,
		  seq         bigserial,
		  role        text        NOT NULL,
		  content     text        NOT NULL DEFAULT '',
		  tool_calls  jsonb,
		  tool_call_id text       NOT NULL DEFAULT '',
		  name        text        NOT NULL DEFAULT '',
		  created_at  timestamptz NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS ai_agent_thread_message_thread_idx
		  ON ai_agent_thread_message (thread_id, seq);`)
	return err
}

func loadThread(ctx context.Context, db *pgxpool.Pool, threadID string) ([]Message, error) {
	rows, err := db.Query(ctx, `
		SELECT role, content, tool_calls, tool_call_id, name
		FROM ai_agent_thread_message WHERE thread_id=$1 ORDER BY seq`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var tc []byte
		if err := rows.Scan(&m.Role, &m.Content, &tc, &m.ToolCallID, &m.Name); err != nil {
			return nil, err
		}
		if len(tc) > 0 {
			_ = json.Unmarshal(tc, &m.ToolCalls)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func saveThread(ctx context.Context, db *pgxpool.Pool, threadID string, msgs []Message) error {
	for _, m := range msgs {
		id, _ := uuid.NewV7()
		var tc any
		if len(m.ToolCalls) > 0 {
			b, _ := json.Marshal(m.ToolCalls)
			tc = b
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO ai_agent_thread_message (id, thread_id, role, content, tool_calls, tool_call_id, name)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id.String(), threadID, m.Role, m.Content, tc, m.ToolCallID, m.Name); err != nil {
			return err
		}
	}
	return nil
}
