package agent

// AI 域 HTTP 层：/ai/deepseek/status、/agent/tools、/agent/tools/execute、
// /agent/chat、/ai/suggestions（列表 + 人工确认闭环）。
//
// 两个尚未原生化的工具（logistics.dispatch_recommendation 依赖报价规则引擎、
// logistics.intelligent_consolidation 依赖拼单配载算法）由 Fallback 透传回 Django，
// 对外契约不变；待各自所属域移植后接管。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/analytics"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
)

type Handler struct {
	DB       *pgxpool.Pool
	Svc      *auth.Service
	MD       *masterdata.Handler
	Fallback http.Handler // 未原生化工具 → Django 代理
}

const permAIUse = "ai.use"

// computeMetric 供 analytics.query_metric 工具调用（口径与经营看板同源）
func computeMetric(ctx context.Context, db *pgxpool.Pool, code string, days int) (map[string]any, *toolErr) {
	m, err := analytics.ComputeMetric(ctx, db, code, days)
	if err != nil {
		if err.Error() == "UNKNOWN_METRIC" {
			return nil, &toolErr{404, "UNKNOWN_METRIC", "未知指标：" + code}
		}
		return nil, &toolErr{500, "INTERNAL", "指标计算失败"}
	}
	return m, nil
}

func (h *Handler) require(w http.ResponseWriter, r *http.Request) (*auth.UserRow, bool) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return nil, false
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil || !hasPerm(perms, permAIUse) {
		httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "无 AI 使用权限")
		return nil, false
	}
	// LLM 成本闸：与 Django scope="ai" 同档（默认 30/min，按用户计）
	if ok, wait := aiThrottle.Allow(me.ID); !ok {
		httpx.Err(w, http.StatusTooManyRequests, "throttled",
			fmt.Sprintf("请求已被限流。 预计 %d 秒后可用。", wait))
		return nil, false
	}
	return me, true
}

func hasPerm(perms []string, want string) bool {
	for _, p := range perms {
		if p == "*" || p == want {
			return true
		}
	}
	return false
}

// Status GET /api/v1/ai/deepseek/status
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.require(w, r); !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, DefaultClient().Status())
}

// Tools GET /api/v1/agent/tools
func (h *Handler) Tools(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.require(w, r); !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tools": mergedToolList()})
}

// Execute POST /api/v1/agent/tools/execute {tool_name, arguments}
func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	me, ok := h.require(w, r)
	if !ok {
		return
	}
	var body struct {
		ToolName  string          `json:"tool_name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ToolName == "" {
		httpx.Err(w, http.StatusBadRequest, "TOOL_NAME_REQUIRED", "tool_name 必填。")
		return
	}
	args := map[string]any{}
	if len(body.Arguments) > 0 && string(body.Arguments) != "null" {
		if err := json.Unmarshal(body.Arguments, &args); err != nil {
			httpx.Err(w, http.StatusBadRequest, "INVALID_ARGUMENTS", "arguments 必须是对象。")
			return
		}
	}
	if _, native := registryByName[body.ToolName]; !native {
		// 未原生化的工具透传回 Django（对外契约不变）
		if h.Fallback != nil {
			h.Fallback.ServeHTTP(w, r)
			return
		}
		httpx.Err(w, http.StatusNotFound, "UNKNOWN_AGENT_TOOL", "未知工具："+body.ToolName)
		return
	}
	res, terr := ExecuteTool(r.Context(), h.DB, body.ToolName, args)
	if terr != nil {
		httpx.Err(w, terr.Status, terr.Code, terr.Message)
		return
	}
	h.audit(r, me, body.ToolName, args, res)
	// 对齐 Django：结果包在 result 键下，与 tool_name 并列
	httpx.JSON(w, http.StatusOK, map[string]any{"tool_name": body.ToolName, "result": res})
}

// audit 工具执行留痕（对齐 Django AgentToolExecuteView._audit）
func (h *Handler) audit(r *http.Request, me *auth.UserRow, name string, args map[string]any, res map[string]any) {
	id, _ := uuid.NewV7()
	payload, _ := json.Marshal(map[string]any{
		"arguments": args, "risk_detected": res["risk_detected"],
	})
	resourceID, _ := args["waybill_no"].(string)
	_, _ = h.DB.Exec(r.Context(), `
		INSERT INTO audit_log (id, created_at, updated_at, actor_id, action, resource_type, resource_id,
		  request_id, method, path, status_code, ip, payload)
		VALUES ($1, now(), now(), $2::uuid, $3, 'waybill', $4, '', $5, $6, 200, NULLIF($7,'')::inet, $8)`,
		id.String(), me.ID, "agent_tool:"+name, resourceID, r.Method, r.URL.Path, clientIP(r), payload)
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		for i := 0; i < len(v); i++ {
			if v[i] == ',' {
				return v[:i]
			}
		}
		return v
	}
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}

// Chat POST /api/v1/agent/chat {message, thread_id}
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.require(w, r); !ok {
		return
	}
	var body struct {
		Message  string `json:"message"`
		ThreadID string `json:"thread_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Message == "" {
		httpx.Err(w, http.StatusBadRequest, "MESSAGE_REQUIRED", "message 必填。")
		return
	}
	res, err := Run(r.Context(), h.DB, body.Message, body.ThreadID)
	if err == ErrNotConfigured {
		httpx.Err(w, http.StatusServiceUnavailable, "DEEPSEEK_NOT_CONFIGURED", "DEEPSEEK_API_KEY 未配置，无法启动 Agent。")
		return
	}
	if err != nil {
		httpx.Err(w, http.StatusBadGateway, "AGENT_FAILED", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// ── AI 建议（人工确认闭环）──

var suggestionsCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT g.id::text AS id, g.waybill_id::text AS waybill, COALESCE(w.waybill_no,'') AS waybill_no,
       g.suggestion_type, g.title, g.body, g.status, g.evidence, g.tool_name,
       g.confirmed_at, g.created_at`,
	FromClause:   "FROM ai_agent_suggestion g LEFT JOIN ops_waybill w ON w.id = g.waybill_id",
	SearchCols:   []string{"g.title", "g.body"},
	OrderingCols: map[string]string{"created_at": "g.created_at"},
	DirectParams: map[string]string{
		"suggestion_type": "g.suggestion_type", "status": "g.status", "waybill": "g.waybill_id::text",
	},
	DefaultOrder: "ORDER BY g.created_at DESC, g.id",
}

// Suggestions GET /api/v1/ai/suggestions（数据范围按运单组织，含无运单的建议）
func (h *Handler) Suggestions(w http.ResponseWriter, r *http.Request) {
	me, ok := h.require(w, r)
	if !ok {
		return
	}
	cfg := suggestionsCfg
	scopeIDs, err := h.Svc.ScopeOrgIDs(r.Context(), me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if scopeIDs != nil {
		// include_null=True：未挂运单的建议同样可见
		cfg.FromClause += " AND (w.organization_id IS NULL OR w.organization_id::text = ANY(" + pgArray(scopeIDs) + "))"
	}
	h.MD.List(w, r, cfg)
}

// pgArray 把组织 ID 列表内联为 SQL 数组字面量（元素为服务端生成的 UUID，非用户输入）
func pgArray(ids []string) string {
	if len(ids) == 0 {
		return "ARRAY[]::text[]"
	}
	out := "ARRAY["
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		out += "'" + id + "'"
	}
	return out + "]::text[]"
}

// ConfirmSuggestion POST /api/v1/ai/suggestions/{id}/confirm {status}
func (h *Handler) ConfirmSuggestion(w http.ResponseWriter, r *http.Request) {
	me, ok := h.require(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No AgentSuggestion matches the given query.")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Status != "accepted" && body.Status != "rejected" {
		httpx.Err(w, http.StatusBadRequest, "INVALID_DECISION", "status 必须是 accepted 或 rejected。")
		return
	}
	ct, err := h.DB.Exec(r.Context(), `
		UPDATE ai_agent_suggestion SET status=$2, confirmed_by_id=$3::uuid, confirmed_at=now(), updated_at=now()
		WHERE id=$1::uuid`, id, body.Status, me.ID)
	if err != nil || ct.RowsAffected() == 0 {
		httpx.Err(w, http.StatusNotFound, "error", "No AgentSuggestion matches the given query.")
		return
	}
	it, err := h.MD.One(r.Context(), suggestionsCfg, "g.id = $1::uuid", id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// aiThrottle LLM/Agent 调用限额（对齐 DRF scope="ai"，默认 30/min）：
// 防 token 成本 DoS —— 缺这道闸，一次脚本循环就能把模型调用打爆。
var aiThrottle = httpx.NewThrottle("THROTTLE_AI", "30/min")
