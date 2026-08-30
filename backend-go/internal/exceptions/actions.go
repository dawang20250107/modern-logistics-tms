package exceptions

// 异常处置动作与详情 CRUD：对齐 ExceptionViewSet 的 retrieve/update/destroy
// 与 timeline / assign / handle / close 四个 @action。
//
// 三个处置动作的共同形态：改状态 → 落一条 ExceptionEvent → 回一份 ExceptionSerializer。
// close 额外把「有金额的责任认定」落成一条应付费用，这是异常闭环里唯一动钱的地方。

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
)

// Cfg 供外部注册通用 CRUD（详情/改/删）用；数据范围与列表同口径
var Cfg = func() masterdata.ResourceCfg {
	c := excCfg
	c.ScopeOrgCol = "SELECT sw.organization_id FROM ops_waybill sw WHERE sw.id = x.waybill_id"
	c.PartialOmit = map[string]string{"waybill_no": "x.waybill_id", "assignee_name": "x.assignee_id"}
	return c
}()

// Write 异常的可写字段；status 是只读的（read_only_fields），只能由处置动作推进
var Write = masterdata.WriteCfg{
	ReadPerm:  "waybill.view",
	WritePerm: "waybill.manage",
	Table:     "ops_exception", Model: "ExceptionRecord", Verbose: "异常", Alias: "x",
	Fields: map[string]masterdata.Field{
		"waybill":              {Kind: masterdata.FUUID, Ref: "ops_waybill"}, // null=True：不挂运单的异常也合法
		"exception_type":       {Kind: masterdata.FEnum, Required: true, Choices: exceptionTypes},
		"level":                {Kind: masterdata.FEnum, Default: "medium", Choices: []string{"low", "medium", "high"}},
		"source":               {Kind: masterdata.FText, Default: "manual"},
		"description":          {Kind: masterdata.FText},
		"assignee":             {Kind: masterdata.FUUID, Ref: "accounts_user"},
		"responsibility_party": {Kind: masterdata.FText},
		"amount":               {Kind: masterdata.FDecimal, Default: "0"},
		"resolution":           {Kind: masterdata.FText},
	},
	// create 被 Create() 完全接管（要写异常事件与 order_id 回填）
	NoCreate: true,
	// 异常事件是 CASCADE：不先清就撞外键
	CascadeTables: map[string]string{"ops_exception_event": "exception_id"},
}

var exceptionTypes = func() []string {
	out := make([]string, 0, len(exceptionTypeLabel))
	for k := range exceptionTypeLabel {
		out = append(out, k)
	}
	return out
}()

// object 取异常并做数据范围校验；未命中时已写 404
// need 权限闸。
//
// 这几个动作原先**一个权限检查都没有**：只要登录了、而且那张运单在你的数据范围里，
// 就能指派、处理、乃至定责关闭——而关闭会按赔付金额落一条应付，
// 把钱带进对账。数据范围挡住的是"看得见谁的单"，不是"能不能做这件事"，
// 拿它当权限用是把两个不同的问题混成一个：同一个网点的客服照样能替公司定责赔钱。
//
// 这一批是发布前系统性排查 22 个读写配置时顺出来的（见 authz_test.go 的清单）。
func (h *Handler) need(w http.ResponseWriter, r *http.Request, perm string) bool {
	return h.MD.Allow(w, r, perm)
}

func (h *Handler) object(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No ExceptionRecord matches the given query.")
		return "", false
	}
	it, err := h.MD.OneScoped(r, Cfg, "x.id = $1::uuid", id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusNotFound, "error", "No ExceptionRecord matches the given query.")
		return "", false
	}
	return id, true
}

// respond 回一份异常序列化（与列表列面同一份 SQL）
func (h *Handler) respond(w http.ResponseWriter, r *http.Request, id string) {
	it, err := h.MD.One(r.Context(), excCfg, "x.id = $1::uuid", id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// Timeline GET /api/v1/exceptions/{id}/timeline
func (h *Handler) Timeline(w http.ResponseWriter, r *http.Request) {
	if !h.need(w, r, "waybill.view") {
		return
	}
	id, ok := h.object(w, r)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT e.id::text, e.event_type, e.from_status, e.to_status,
		       COALESCE(u.username,'') AS actor_name, e.note, e.payload, e.event_time
		FROM ops_exception_event e LEFT JOIN accounts_user u ON u.id = e.actor_id
		WHERE e.exception_id = $1::uuid
		ORDER BY e.event_time, e.id`, id)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取事件失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var eid, et, from, to, actor, note string
		var payload map[string]any
		var at any
		if rows.Scan(&eid, &et, &from, &to, &actor, &note, &payload, &at) != nil {
			break
		}
		out = append(out, map[string]any{
			"id": eid, "event_type": et, "from_status": from, "to_status": to,
			"actor_name": actor, "note": note, "payload": payload,
			"event_time": at,
		})
	}
	httpx.JSON(w, http.StatusOK, out)
}

// Assign POST /api/v1/exceptions/{id}/assign {assignee}
func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	if !h.need(w, r, "waybill.manage") {
		return
	}
	ctx := r.Context()
	id, ok := h.object(w, r)
	if !ok {
		return
	}
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Assignee string `json:"assignee"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var from string
	_ = h.DB.QueryRow(ctx, `SELECT status FROM ops_exception WHERE id=$1::uuid`, id).Scan(&from)

	var assignee any
	if _, err := uuid.Parse(body.Assignee); err == nil {
		assignee = body.Assignee
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE ops_exception SET assignee_id=$2::uuid, status='handling', updated_at=now()
		WHERE id=$1::uuid`, id, assignee); err != nil {
		httpx.Fail(w, r, "INTERNAL", "写入失败", err)
		return
	}
	note := "取消指派"
	if assignee != nil {
		var username string
		_ = h.DB.QueryRow(ctx, `SELECT username FROM accounts_user WHERE id=$1::uuid`, assignee).Scan(&username)
		note = "指派给 " + username
	}
	payload := map[string]any{"assignee_id": nil}
	if assignee != nil {
		payload["assignee_id"] = assignee
	}
	h.event(r, id, "assign", from, "handling", me.ID, note, payload)
	h.respond(w, r, id)
}

// Handle POST /api/v1/exceptions/{id}/handle {resolution}
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if !h.need(w, r, "waybill.manage") {
		return
	}
	ctx := r.Context()
	id, ok := h.object(w, r)
	if !ok {
		return
	}
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	var from, resolution string
	_ = h.DB.QueryRow(ctx, `SELECT status, resolution FROM ops_exception WHERE id=$1::uuid`, id).Scan(&from, &resolution)
	// 未传 resolution 时保留原值（Django 的 data.get(key, 原值) 语义）
	newRes := resolution
	if v, has := body["resolution"].(string); has {
		newRes = v
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE ops_exception SET status='pending_audit', resolution=$2, updated_at=now()
		WHERE id=$1::uuid`, id, newRes); err != nil {
		httpx.Fail(w, r, "INTERNAL", "写入失败", err)
		return
	}
	note, _ := body["resolution"].(string) // 事件 note 取的是本次入参而非合并后的值
	h.event(r, id, "handle", from, "pending_audit", me.ID, note, map[string]any{})
	h.respond(w, r, id)
}

// Close POST /api/v1/exceptions/{id}/close {responsibility_party, amount, resolution}
func (h *Handler) Close(w http.ResponseWriter, r *http.Request) {
	if !h.need(w, r, "waybill.manage") {
		return
	}
	ctx := r.Context()
	id, ok := h.object(w, r)
	if !ok {
		return
	}
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from, party, amount, resolution string
	var waybillID *string
	// 读状态必须在事务里、并且锁住这一行。
	//
	// 下面那个 `from == "closed"` 的守卫原先读在事务**外面**，
	// 写又不带状态条件——串行点两次挡得住，并发就一起穿过去。
	// 实测 6 个并发关闭：**6 个都返回 200，落了 6 条应付，
	// 800 元的赔付计成 4800**。前端重试一次、或者两个调度同时处理
	// 同一条异常，承运商就被多扣几倍。
	if err := tx.QueryRow(ctx, `SELECT status, responsibility_party, amount::text, resolution, waybill_id::text
		FROM ops_exception WHERE id=$1::uuid FOR UPDATE`, id).
		Scan(&from, &party, &amount, &resolution, &waybillID); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No ExceptionRecord matches the given query.")
		return
	}

	// 已关闭的异常不能再关一次。
	//
	// 关闭不是改个状态：责任金额 > 0 时会落一条应付，把异常成本带进对账。
	// 这里原先没有任何守卫——同一条异常连关三次就生成三条应付，
	// 实测 800 元的赔付被计成 2400 元，而三次都返回 200。
	// 操作员双击一下、或者网络重试一次，承运商就被多扣一倍。
	//
	// 顺带也挡住"悄悄改写定责结论"：那个结论是要拿去跟承运商结算的，
	// 要改就该是一次明确的动作，而不是再点一次关闭。
	if from == "closed" {
		httpx.Err(w, http.StatusConflict, "EXCEPTION_CLOSED",
			"该异常已关闭。如需更正责任方或赔付金额，请先重新打开异常。")
		return
	}

	if v, has := body["responsibility_party"].(string); has {
		party = v
	}
	if v, has := body["amount"]; has {
		// Django 的 `data.get("amount", 旧值) or 0`：空串/0/None 一律落 0
		amount = decStr(v)
	}
	if v, has := body["resolution"].(string); has {
		resolution = v
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ops_exception SET status='closed', responsibility_party=$2,
		  amount=$3::numeric, resolution=$4, updated_at=now() WHERE id=$1::uuid`,
		id, party, amount, resolution); err != nil {
		httpx.Fail(w, r, "INTERNAL", "写入失败", err)
		return
	}
	// 责任金额 > 0 且挂了运单 → 落一条应付费用，把异常成本带进对账
	if waybillID != nil {
		var positive bool
		_ = tx.QueryRow(ctx, `SELECT $1::numeric > 0`, amount).Scan(&positive)
		if positive {
			eid, _ := uuid.NewV7()
			// 其余 NOT NULL 列按模型层的 blank/default 补零值；occurred_at 保持 NULL
			// （Django 的 create() 没传，就不该被网关擅自填成"现在"）
			if _, err := tx.Exec(ctx, `
				INSERT INTO fin_expense_record (id, created_at, updated_at, waybill_id, direction,
				  expense_item_code, amount, risk_status, source_system, external_id,
				  currency, remark, payee_ref, payee_type, charge_method, matched_condition,
				  price_source, pricing_rule_id, pricing_rule_name, quote_id,
				  calculation_detail, input_snapshot, rule_snapshot)
				VALUES ($1, now(), now(), $2::uuid, 'payable', 'EXCEPTION_COST', $3::numeric,
				  'normal', 'exception', $4, 'CNY', '', '', '', '', '', '', '', '', '',
				  '{}'::jsonb, '{}'::jsonb, '{}'::jsonb)`,
				eid.String(), *waybillID, amount, id); err != nil {
				httpx.Fail(w, r, "INTERNAL", "异常费用落库失败", err)
				return
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交事务失败")
		return
	}
	h.event(r, id, "close", from, "closed", me.ID, resolution,
		map[string]any{"responsibility_party": party, "amount": amount})
	h.respond(w, r, id)
}

// decStr 把 JSON 数值/字符串归一成十进制字面量；空值按 Django 的 `or 0` 落 0
func decStr(v any) string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return "0"
		}
		return x
	case float64:
		return trimFloat(x)
	case nil:
		return "0"
	}
	return "0"
}

func trimFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// event 落一条异常事件（payload 与 Django 的 **payload 展开一致）
func (h *Handler) event(r *http.Request, excID, eventType, from, to, actorID, note string, payload map[string]any) {
	eid, _ := uuid.NewV7()
	pj, _ := json.Marshal(payload)
	if _, err := h.DB.Exec(r.Context(), `
		INSERT INTO ops_exception_event (id, created_at, updated_at, exception_id, event_type,
		  from_status, to_status, actor_id, note, payload, event_time)
		VALUES ($1, now(), now(), $2::uuid, $3, $4, $5, $6::uuid, $7, $8, clock_timestamp())`,
		eid.String(), excID, eventType, from, to, actorID, note, pj); err != nil {
		slog.Warn("异常事件写库失败", "err", err)
	}
}
