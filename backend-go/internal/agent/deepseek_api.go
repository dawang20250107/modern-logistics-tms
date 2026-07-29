package agent

// AI 域剩余的两个对外端点：
//   POST /api/v1/ai/deepseek/chat  裸 LLM 补全（透传 messages）
//   POST /api/v1/ai/query-waybill  自然语言查单（规则版，按数据范围收口）
//
// 对齐 apps/ai/views.{DeepSeekChatView, QueryWaybillView}。

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/waybills"
)

// DeepSeekChat POST /api/v1/ai/deepseek/chat {messages, model, temperature}
func (h *Handler) DeepSeekChat(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.require(w, r); !ok {
		return
	}
	var body struct {
		Messages    []Message `json:"messages"`
		Model       string    `json:"model"`
		Temperature *float64  `json:"temperature"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.Messages) == 0 {
		httpx.Err(w, http.StatusBadRequest, "INVALID_MESSAGES", "messages 必须是非空数组。")
		return
	}
	c := DefaultClient()
	if body.Model != "" {
		c.Model = body.Model
	}
	if body.Temperature != nil {
		c.Temperature = *body.Temperature
	}
	msg, err := c.Chat(r.Context(), body.Messages, nil)
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			httpx.Err(w, http.StatusServiceUnavailable, "DEEPSEEK_NOT_CONFIGURED",
				"未配置 DEEPSEEK_API_KEY，AI 能力不可用。")
			return
		}
		httpx.Err(w, http.StatusBadGateway, "DEEPSEEK_ERROR", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"provider": "deepseek", "model": c.Model, "content": msg.Content,
		// raw 原样透传上游响应；这里只有归一化后的消息，故给等价的最小结构
		"raw": map[string]any{
			"model":   c.Model,
			"choices": []any{map[string]any{"message": map[string]any{"role": msg.Role, "content": msg.Content}}},
		},
	})
}

// QueryWaybill POST /api/v1/ai/query-waybill {query}
//
// 规则版查单：有关键字按单号/线路/客户/车牌检索，没关键字就回「风险或待回单」的那批。
// 数据范围先收口再过滤——跨租户查单是查不得的。
func (h *Handler) QueryWaybill(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, ok := h.require(w, r)
	if !ok {
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	query := strings.TrimSpace(body.Query)

	where := []string{"1=1"}
	args := []any{}
	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			where = append(where, "false")
		} else {
			args = append(args, scopeIDs)
			where = append(where, fmt.Sprintf("w.organization_id::text = ANY($%d)", len(args)))
		}
	}
	if query != "" {
		args = append(args, "%"+query+"%")
		p := fmt.Sprintf("$%d", len(args))
		where = append(where, fmt.Sprintf(
			"(w.waybill_no ILIKE %s OR w.route_name ILIKE %s OR c.name ILIKE %s OR v.plate_no ILIKE %s)", p, p, p, p))
	} else {
		where = append(where, "(w.risk_level IN ('high','medium') OR w.receipt_status = 'pending')")
	}
	items, err := waybills.SerializeWhere(ctx, h.DB, strings.Join(where, " AND "), 10, args...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败："+err.Error())
		return
	}
	risk := 0
	for _, it := range items {
		if lvl, _ := it["risk_level"].(string); lvl == "high" || lvl == "medium" {
			risk++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"answer":   fmt.Sprintf("找到 %d 条相关运单，其中 %d 条存在 ETA/路线风险。", len(items), risk),
		"query":    query,
		"waybills": items,
	})
}

var _ = auth.UserID
