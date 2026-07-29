package notifications

// 通知域：铃铛高频端点 —— 列表 / 未读数 / 单条已读 / 全部已读。
// 对齐 apps/notifications.NotificationViewSet（按 recipient 隔离）。

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
)

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
	MD  *masterdata.Handler
}

func cfgFor(userID string) masterdata.ResourceCfg {
	return masterdata.ResourceCfg{
		SelectSQL: `
SELECT n.id::text AS id, n.category, n.title, n.body, n.level, n.link_type, n.link_id,
       n.payload, n.is_read, n.read_at, n.created_at`,
		FromClause:   "FROM ntf_notification n JOIN accounts_user me ON me.id = n.recipient_id AND me.id = '" + userID + "'::uuid",
		OrderingCols: map[string]string{"created_at": "n.created_at"},
		DirectParams: map[string]string{"category": "n.category", "is_read": "n.is_read", "level": "n.level"},
		DefaultOrder: "ORDER BY n.created_at DESC, n.id",
	}
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) (string, bool) {
	me, err := h.Svc.UserByID(r.Context(), auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return "", false
	}
	return me.ID, true
}

// List GET /api/v1/notifications
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.me(w, r)
	if !ok {
		return
	}
	h.MD.List(w, r, cfgFor(uid))
}

// UnreadCount GET /api/v1/notifications/unread-count
func (h *Handler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.me(w, r)
	if !ok {
		return
	}
	var n int
	_ = h.DB.QueryRow(r.Context(), `SELECT count(*) FROM ntf_notification WHERE recipient_id=$1::uuid AND NOT is_read`, uid).Scan(&n)
	httpx.JSON(w, http.StatusOK, map[string]any{"unread": n})
}

// Read POST /api/v1/notifications/{id}/read
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.me(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Notification matches the given query.")
		return
	}
	ct, err := h.DB.Exec(r.Context(), `UPDATE ntf_notification SET is_read=true, read_at=now(), updated_at=now()
		WHERE id=$1::uuid AND recipient_id=$2::uuid`, id, uid)
	if err != nil || ct.RowsAffected() == 0 {
		httpx.Err(w, http.StatusNotFound, "error", "No Notification matches the given query.")
		return
	}
	it, err := h.MD.One(r.Context(), cfgFor(uid), "n.id = $1::uuid", id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// ReadAll POST /api/v1/notifications/read-all
func (h *Handler) ReadAll(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.me(w, r)
	if !ok {
		return
	}
	ct, err := h.DB.Exec(r.Context(), `UPDATE ntf_notification SET is_read=true, read_at=now(), updated_at=now()
		WHERE recipient_id=$1::uuid AND NOT is_read`, uid)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"marked": ct.RowsAffected()})
}
