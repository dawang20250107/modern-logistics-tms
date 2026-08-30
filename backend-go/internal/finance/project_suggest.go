package finance

// 建单时的项目智能推荐。
//
// 项目是对账用得最多的维度，但录单是高频动作——每次都让客服在几十个项目里翻找
// 是不现实的，字段会被跳过，然后对账那头就归不了集。所以推荐必须"基本猜对"：
//
// 打分口径（越靠上权重越高，都是真实可观测的信号，不做玄学）：
//   1. 线路完全一致的历史单最多  —— 同客户同线路几乎必然是同一个项目
//   2. 起点或终点一致的历史单     —— 部分匹配，弱一些
//   3. 最近 30 天用过            —— 在跑的项目优先于沉睡项目
//   4. 历史用量                  —— 长期主力项目兜底
// 每条推荐都附 reason，让客服一眼看懂"为什么推它"，而不是盲选第一个。

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// SuggestProjects GET /finance/projects/suggest?customer=&origin=&destination=&q=
func (h *Handler) SuggestProjects(w http.ResponseWriter, r *http.Request) {
	if h.Svc.Guard(w, r, PermView, denyView) == nil {
		return
	}
	q := r.URL.Query()
	customer := strings.TrimSpace(q.Get("customer"))
	origin := strings.TrimSpace(q.Get("origin"))
	dest := strings.TrimSpace(q.Get("destination"))
	keyword := strings.TrimSpace(q.Get("q"))

	rows, err := h.DB.Query(r.Context(), `
		WITH cand AS (
		  SELECT pj.id, pj.project_no, pj.name, pj.customer_id,
		         COALESCE(cm.name,'') AS customer_name,
		         pj.start_date, pj.end_date
		  FROM fin_project pj
		  LEFT JOIN md_customer cm ON cm.id = pj.customer_id
		  WHERE NOT pj.is_deleted AND pj.status = 'active'
		    -- 指定客户时只推该客户的项目；未指定则推全部在跑项目
		    AND ($1 = '' OR pj.customer_id = $1::uuid)
		    AND ($4 = '' OR pj.name ILIKE '%'||$4||'%' OR pj.project_no ILIKE '%'||$4||'%')
		), stat AS (
		  SELECT w.project_id,
		         count(*) FILTER (WHERE $2 <> '' AND $3 <> ''
		                            AND w.origin = $2 AND w.destination = $3) AS exact_lane,
		         count(*) FILTER (WHERE ($2 <> '' AND w.origin = $2)
		                             OR ($3 <> '' AND w.destination = $3))    AS partial_lane,
		         count(*) FILTER (WHERE w.created_at >= now() - interval '30 days') AS recent,
		         count(*) AS total
		  FROM ops_waybill w
		  WHERE w.project_id IS NOT NULL
		  GROUP BY w.project_id
		)
		SELECT c.id::text, c.project_no, c.name, c.customer_id::text, c.customer_name,
		       COALESCE(s.exact_lane,0), COALESCE(s.partial_lane,0),
		       COALESCE(s.recent,0), COALESCE(s.total,0),
		       COALESCE(s.exact_lane,0) * 100
		     + COALESCE(s.partial_lane,0) * 20
		     + LEAST(COALESCE(s.recent,0), 20) * 5
		     + LEAST(COALESCE(s.total,0), 50)          AS score
		FROM cand c LEFT JOIN stat s ON s.project_id = c.id
		ORDER BY score DESC, c.start_date DESC NULLS LAST, c.project_no
		LIMIT 8`, customer, origin, dest, keyword)
	if err != nil {
		httpx.Fail(w, r, "INTERNAL", "查询失败", err)
		return
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, no, name, custID, custName string
		var exact, partial, recent, total, score int
		if rows.Scan(&id, &no, &name, &custID, &custName,
			&exact, &partial, &recent, &total, &score) != nil {
			break
		}
		items = append(items, map[string]any{
			"id": id, "project_no": no, "name": name,
			"customer": custID, "customer_name": custName,
			"score": score, "reason": suggestReason(exact, partial, recent, total),
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// suggestReason 把打分翻译成人话；没有任何历史时说清楚是"新项目"而不是硬凑理由
func suggestReason(exact, partial, recent, total int) string {
	parts := []string{}
	switch {
	case exact > 0:
		parts = append(parts, "该线路历史 "+plural(exact)+"单")
	case partial > 0:
		parts = append(parts, "起终点部分匹配 "+plural(partial)+"单")
	}
	if recent > 0 {
		parts = append(parts, "近 30 天 "+plural(recent)+"单")
	}
	if len(parts) == 0 {
		if total > 0 {
			return "历史 " + plural(total) + "单"
		}
		return "新项目，暂无历史单"
	}
	return strings.Join(parts, "，")
}

func plural(n int) string {
	if n > 99 {
		return "99+"
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// EnsureProject 按名称取或建项目（建单表单里"+ 新建项目"用）。
// 同客户同名视为同一个项目，避免客服重复敲出「XX年度配送」「XX年度配送 」两条。
func (h *Handler) EnsureProject(ctx context.Context, name, customerID string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", nil
	}
	var id string
	err := h.DB.QueryRow(ctx, `
		SELECT id::text FROM fin_project
		WHERE NOT is_deleted AND lower(name) = lower($1)
		  AND (customer_id IS NOT DISTINCT FROM $2::uuid)
		ORDER BY created_at LIMIT 1`, name, nullIfEmpty(customerID)).Scan(&id)
	if err == nil {
		return id, name, nil
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	no, err := nextProjectNo(ctx, tx)
	if err != nil {
		return "", "", err
	}
	nid, _ := uuid.NewV7()
	if _, err := tx.Exec(ctx, `
		INSERT INTO fin_project (id, created_at, updated_at, project_no, name, customer_id,
		  start_date, status, remark)
		VALUES ($1, now(), now(), $2, $3, $4::uuid, (now() AT TIME ZONE 'Asia/Shanghai')::date, 'active', '')`,
		nid.String(), no, name, nullIfEmpty(customerID)); err != nil {
		return "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return nid.String(), name, nil
}

// nextProjectNo 原子取号 XM+日期+4 位序
func nextProjectNo(ctx context.Context, tx pgx.Tx) (string, error) {
	day := time.Now().In(shanghai()).Format("20060102")
	var v int
	if err := tx.QueryRow(ctx, `
		INSERT INTO ops_number_counter (scope, value) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET value = ops_number_counter.value + 1
		RETURNING value`, "project:"+day).Scan(&v); err != nil {
		return "", err
	}
	return fmt.Sprintf("XM%s%04d", day, v), nil
}
