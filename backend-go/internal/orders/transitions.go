package orders

// 订单状态流转与调度锁：confirm / pool / cancel / claim / release / assign / unassign。
// 对齐 apps/ops/intake.{confirm,pool,cancel}_order 与 order_dispatch.{claim,release,assign,unassign}。
// publish_event(redis SSE) 属阶段 3 平台域，此处先落库通知（数据面等价），实时推送随 SSE 移植补齐。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

var businessTypeLabel = map[string]string{"ftl": "整车", "ltl": "零担", "express": "快递", "coldchain": "冷链", "hazmat": "危化"}
var priorityLabel = map[string]string{"normal": "普通", "urgent": "加急", "vip": "VIP"}

func txEvent(ctx context.Context, tx pgx.Tx, orderID, eventType, fromStatus, toStatus, actorID, source string, payload map[string]any) error {
	pj, _ := json.Marshal(payload)
	eid, _ := uuid.NewV7()
	_, err := tx.Exec(ctx, `
		INSERT INTO ops_order_event (id, created_at, updated_at, event_time, order_id, event_type, from_status, to_status, actor_id, source, payload)
		VALUES ($1, now(), now(), now(), $2, $3, $4, $5, $6, $7, $8)`,
		eid.String(), orderID, eventType, fromStatus, toStatus, actorID, source, pj)
	return err
}

type orderRow struct {
	ID, OrderNo, Status, ApprovalStatus, Origin, Destination, Priority, BusinessType string
	ClaimedBy, AssignedTo                                                            *string
}

func lockOrder(ctx context.Context, tx pgx.Tx, id string) (*orderRow, error) {
	o := &orderRow{}
	err := tx.QueryRow(ctx, `
		SELECT id::text, order_no, status, approval_status, origin, destination, priority, business_type,
		       claimed_by_id::text, assigned_to_id::text
		FROM ops_order WHERE id = $1::uuid FOR UPDATE`, id,
	).Scan(&o.ID, &o.OrderNo, &o.Status, &o.ApprovalStatus, &o.Origin, &o.Destination, &o.Priority, &o.BusinessType,
		&o.ClaimedBy, &o.AssignedTo)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return o, err
}

// notifyRole 按角色扇出落库通知（对齐 notifications.services.notify_role；非关键路径失败忽略）
func notifyRole(ctx context.Context, tx pgx.Tx, roleCode, category, title, body, level, linkType, linkID string) {
	_, _ = tx.Exec(ctx, `
		INSERT INTO ntf_notification (id, created_at, updated_at, recipient_id, category, title, body, level, link_type, link_id, payload, is_read)
		SELECT gen_random_uuid(), now(), now(), ra.user_id, $2, $3, $4, $5, $6, $7, '{}'::jsonb, false
		FROM iam_role_assignment ra JOIN iam_role r ON r.id = ra.role_id WHERE r.code = $1
		GROUP BY ra.user_id`, roleCode, category, title, body, level, linkType, linkID)
}

// mutate 通用骨架：行锁读单 → 校验/更新 → 事件 → 提交 → 回读完整序列化
func (h *Handler) mutate(w http.ResponseWriter, r *http.Request,
	fn func(ctx context.Context, tx pgx.Tx, o *orderRow, me *auth.UserRow) (int, string, string)) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	id := chi.URLParam(r, "id")
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	o, err := lockOrder(ctx, tx, id)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取订单失败")
		return
	}
	if o == nil {
		httpx.Err(w, http.StatusNotFound, "ORDER_NOT_FOUND", "订单不存在。")
		return
	}
	if code, appCode, msg := fn(ctx, tx, o, me); appCode != "" {
		httpx.Err(w, code, appCode, msg)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	h.respondOneStatus(w, r, id, me, http.StatusOK)
}

// Confirm POST /orders/{id}/confirm
func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	h.mutate(w, r, func(ctx context.Context, tx pgx.Tx, o *orderRow, me *auth.UserRow) (int, string, string) {
		if o.Status != "pending_confirm" && o.Status != "confirmed" {
			return 409, "INVALID_ORDER_STATUS", "仅待确认订单可确认。"
		}
		if _, err := tx.Exec(ctx, "UPDATE ops_order SET status='confirmed', updated_at=now() WHERE id=$1::uuid", o.ID); err != nil {
			return 500, "INTERNAL", "更新失败"
		}
		_ = txEvent(ctx, tx, o.ID, "confirmed", o.Status, "confirmed", me.ID, "cs", nil)
		return 0, "", ""
	})
}

// Pool POST /orders/{id}/pool
func (h *Handler) Pool(w http.ResponseWriter, r *http.Request) {
	h.mutate(w, r, func(ctx context.Context, tx pgx.Tx, o *orderRow, me *auth.UserRow) (int, string, string) {
		if o.Status != "confirmed" && o.Status != "pending_confirm" {
			return 409, "INVALID_ORDER_STATUS", "仅已确认/待确认订单可进池。"
		}
		if o.ApprovalStatus == "pending" {
			return 409, "ORDER_NEEDS_APPROVAL", "订单需主管审批通过后方可进池。"
		}
		if o.ApprovalStatus == "rejected" {
			return 409, "ORDER_APPROVAL_REJECTED", "订单审批被驳回，不可进池。"
		}
		if _, err := tx.Exec(ctx, "UPDATE ops_order SET status='pooled', pooled_at=now(), updated_at=now() WHERE id=$1::uuid", o.ID); err != nil {
			return 500, "INTERNAL", "更新失败"
		}
		_ = txEvent(ctx, tx, o.ID, "pooled", o.Status, "pooled", me.ID, "cs", nil)
		level := "info"
		if o.Priority == "urgent" || o.Priority == "vip" {
			level = "warning"
		}
		notifyRole(ctx, tx, "dispatcher", "order_pooled",
			"新订单进池："+o.OrderNo,
			o.Origin+"→"+o.Destination+" · "+businessTypeLabel[o.BusinessType]+" · "+priorityLabel[o.Priority],
			level, "order", o.ID)
		return 0, "", ""
	})
}

// Cancel POST /orders/{id}/cancel
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	h.mutate(w, r, func(ctx context.Context, tx pgx.Tx, o *orderRow, me *auth.UserRow) (int, string, string) {
		if o.Status == "converted" || o.Status == "completed" {
			return 409, "INVALID_ORDER_STATUS", "已派单/已完成订单不可取消。"
		}
		if _, err := tx.Exec(ctx, "UPDATE ops_order SET status='cancelled', updated_at=now() WHERE id=$1::uuid", o.ID); err != nil {
			return 500, "INTERNAL", "更新失败"
		}
		_ = txEvent(ctx, tx, o.ID, "cancelled", o.Status, "cancelled", me.ID, "cs", nil)
		return 0, "", ""
	})
}

// Claim POST /orders/{id}/claim —— 调度锁定（行锁防抢单）
func (h *Handler) Claim(w http.ResponseWriter, r *http.Request) {
	h.mutate(w, r, func(ctx context.Context, tx pgx.Tx, o *orderRow, me *auth.UserRow) (int, string, string) {
		if o.Status != "pooled" || o.ClaimedBy != nil {
			return 409, "ORDER_NOT_CLAIMABLE", "订单已被锁定或不在池中。"
		}
		if o.AssignedTo != nil && *o.AssignedTo != me.ID {
			if chief, _ := h.isChiefDispatcher(ctx, me); !chief {
				return 409, "ORDER_ASSIGNED_OTHER", "该订单已由总调度分派给其他调度，不可锁定。"
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ops_order SET claimed_by_id=$2::uuid, claimed_at=now(), status='dispatching', updated_at=now()
			WHERE id=$1::uuid`, o.ID, me.ID); err != nil {
			return 500, "INTERNAL", "更新失败"
		}
		_ = txEvent(ctx, tx, o.ID, "claimed", "", "dispatching", me.ID, "dispatch", nil)
		return 0, "", ""
	})
}

// Release POST /orders/{id}/release —— 退回订单池
func (h *Handler) Release(w http.ResponseWriter, r *http.Request) {
	h.mutate(w, r, func(ctx context.Context, tx pgx.Tx, o *orderRow, me *auth.UserRow) (int, string, string) {
		if o.Status != "dispatching" {
			return 409, "ORDER_NOT_DISPATCHING", "仅调度中订单可退回池。"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ops_order SET status='pooled', claimed_by_id=NULL, claimed_at=NULL, updated_at=now()
			WHERE id=$1::uuid`, o.ID); err != nil {
			return 500, "INTERNAL", "更新失败"
		}
		return 0, "", ""
	})
}

// Unassign POST /orders/{id}/unassign —— 总调度撤销分单
func (h *Handler) Unassign(w http.ResponseWriter, r *http.Request) {
	h.mutate(w, r, func(ctx context.Context, tx pgx.Tx, o *orderRow, me *auth.UserRow) (int, string, string) {
		if chief, _ := h.isChiefDispatcher(ctx, me); !chief {
			return 403, "NOT_CHIEF", "仅总调度可撤销分单。"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ops_order SET assigned_to_id=NULL, assigned_by_id=NULL, assigned_at=NULL, updated_at=now()
			WHERE id=$1::uuid`, o.ID); err != nil {
			return 500, "INTERNAL", "更新失败"
		}
		return 0, "", ""
	})
}

// Assign POST /orders/assign —— 总调度批量分单 {ids, dispatcher}
func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	if chief, _ := h.isChiefDispatcher(ctx, me); !chief {
		httpx.Err(w, http.StatusForbidden, "NOT_CHIEF", "仅总调度可分单。")
		return
	}
	var body struct {
		IDs        []string `json:"ids"`
		Dispatcher string   `json:"dispatcher"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var targetName string
	if h.DB.QueryRow(ctx, "SELECT COALESCE(NULLIF(nickname,''), username) FROM accounts_user WHERE id=$1::uuid", body.Dispatcher).Scan(&targetName) != nil {
		httpx.Err(w, http.StatusNotFound, "DISPATCHER_NOT_FOUND", "目标调度不存在。")
		return
	}
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	assigned, skipped := []string{}, []string{}
	for _, id := range body.IDs {
		o, err := lockOrder(ctx, tx, id)
		if err != nil || o == nil {
			continue
		}
		if (o.Status != "pooled" && o.Status != "dispatching") || (o.ClaimedBy != nil && *o.ClaimedBy != body.Dispatcher) {
			skipped = append(skipped, o.OrderNo)
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ops_order SET assigned_to_id=$2::uuid, assigned_by_id=$3::uuid, assigned_at=now(), updated_at=now()
			WHERE id=$1::uuid`, o.ID, body.Dispatcher, me.ID); err != nil {
			skipped = append(skipped, o.OrderNo)
			continue
		}
		_ = txEvent(ctx, tx, o.ID, "assigned", "", o.Status, me.ID, "dispatch", map[string]any{"dispatcher": targetName})
		assigned = append(assigned, o.OrderNo)
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"assigned": assigned, "skipped": skipped, "dispatcher": strings.TrimSpace(targetName), "count": len(assigned),
	})
}

var _ = time.Now
