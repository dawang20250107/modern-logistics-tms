// Package finance 财务域读路径：对账总览看板聚合（驾驶舱 + 对账中心共用）。
// 契约对齐 apps/finance/services.statement_overview（逐键）。
package finance

import (
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

type Handler struct{ DB *pgxpool.Pool }

var cst = time.FixedZone("CST", 8*3600) // Asia/Shanghai（业务日界）

type dirSummary struct {
	Total        float64 `json:"total"`
	Settled      float64 `json:"settled"`
	Outstanding  float64 `json:"outstanding"`
	Count        int     `json:"count"`
	Draft        int     `json:"draft"`
	Confirmed    int     `json:"confirmed"`
	Partial      int     `json:"partial"`
	SettledCount int     `json:"settled_count"`
}

// StatementOverview GET /api/v1/finance/statement-overview
func (h *Handler) StatementOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().In(cst)
	today := now.Format("2006-01-02")
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, cst).Format("2006-01-02")

	dirSum := func(direction string) (dirSummary, error) {
		s := dirSummary{}
		err := h.DB.QueryRow(ctx, `
			SELECT COALESCE(sum(total_amount),0)::float8, COALESCE(sum(settled_amount),0)::float8,
			       COALESCE(sum(total_amount)-sum(settled_amount),0)::float8, count(*),
			       count(*) FILTER (WHERE status='draft'), count(*) FILTER (WHERE status='confirmed'),
			       count(*) FILTER (WHERE status='partial'), count(*) FILTER (WHERE status='settled')
			FROM fin_statement WHERE direction=$1`, direction,
		).Scan(&s.Total, &s.Settled, &s.Outstanding, &s.Count, &s.Draft, &s.Confirmed, &s.Partial, &s.SettledCount)
		return s, err
	}
	overdue := func(direction string) (map[string]any, error) {
		var amt float64
		var n int
		err := h.DB.QueryRow(ctx, `
			SELECT COALESCE(sum(total_amount - settled_amount),0)::float8, count(*)
			FROM fin_statement WHERE direction=$1 AND due_date < $2::date AND status <> 'settled'`,
			direction, today).Scan(&amt, &n)
		return map[string]any{"amount": amt, "count": n}, err
	}
	top := func(direction string) ([]map[string]any, error) {
		rows, err := h.DB.Query(ctx, `
			SELECT counterparty_id::text, max(counterparty_name),
			       sum(total_amount - settled_amount)::float8 AS outstanding, count(*)
			FROM fin_statement
			WHERE direction=$1 AND status <> 'settled' AND total_amount - settled_amount > 0
			GROUP BY counterparty_id`, direction)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name string
			var amt float64
			var n int
			if err := rows.Scan(&id, &name, &amt, &n); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"counterparty_id": id, "counterparty_name": name, "outstanding": amt, "count": n})
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["outstanding"].(float64) > out[j]["outstanding"].(float64) })
		if len(out) > 6 {
			out = out[:6]
		}
		return out, rows.Err()
	}

	ar, err1 := dirSum("receivable")
	ap, err2 := dirSum("payable")
	odAR, err3 := overdue("receivable")
	odAP, err4 := overdue("payable")
	topAR, err5 := top("receivable")
	topAP, err6 := top("payable")

	var periodCount int
	var periodAR, periodAP float64
	err7 := h.DB.QueryRow(ctx, `
		SELECT count(*),
		       COALESCE(sum(total_amount) FILTER (WHERE direction='receivable'),0)::float8,
		       COALESCE(sum(total_amount) FILTER (WHERE direction='payable'),0)::float8
		FROM fin_statement WHERE (created_at AT TIME ZONE 'Asia/Shanghai')::date >= $1::date`, monthStart,
	).Scan(&periodCount, &periodAR, &periodAP)

	for _, err := range []error{err1, err2, err3, err4, err5, err6, err7} {
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
			return
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"receivable": ar, "payable": ap,
		"overdue": map[string]any{"receivable": odAR, "payable": odAP},
		"period": map[string]any{
			"label": now.Format("2006-01"), "count": periodCount,
			"receivable": periodAR, "payable": periodAP,
		},
		"top_receivable": topAR, "top_payable": topAP,
		"net_position": round2(ar.Outstanding - ap.Outstanding),
	})
}

func round2(f float64) float64 {
	return float64(int64(f*100+func() float64 {
		if f < 0 {
			return -0.5
		}
		return 0.5
	}())) / 100
}

// ── 对账单台账列表（复用 masterdata 通用列表引擎）──

var statementsCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT s.id::text AS id, s.statement_no, s.direction, s.counterparty_type,
       s.counterparty_id::text AS counterparty_id, s.counterparty_name,
       s.period_start::text AS period_start, s.period_end::text AS period_end, s.due_date::text AS due_date,
       s.total_amount::text AS total_amount, s.item_count, s.external_total::text AS external_total,
       (s.total_amount - s.external_total)::text AS diff,
       s.settled_amount::text AS settled_amount,
       (s.total_amount - s.settled_amount)::text AS outstanding,
       s.settled_at, s.status,
       (CASE s.status WHEN 'draft' THEN '草稿' WHEN 'confirmed' THEN '已确认'
                      WHEN 'partial' THEN '部分结算' WHEN 'settled' THEN '已结算' ELSE s.status END) AS status_label,
       s.confirmed_at, s.audited_at, s.created_at`,
	FromClause: "FROM fin_statement s",
	SearchCols: []string{"s.statement_no", "s.counterparty_name"},
	OrderingCols: map[string]string{
		"statement_no": "s.statement_no", "counterparty_name": "s.counterparty_name",
		"direction": "s.direction", "status": "s.status", "total_amount": "s.total_amount",
		"settled_amount": "s.settled_amount", "created_at": "s.created_at",
		"outstanding_anno": "(s.total_amount - s.settled_amount)",
		"diff_anno":        "abs(s.total_amount - s.external_total)",
	},
	FilterFields: map[string]filters.FilterField{
		"no":     {Type: filters.Text, Cols: []string{"s.statement_no"}},
		"cp":     {Type: filters.Text, Cols: []string{"s.counterparty_name"}},
		"dir":    {Type: filters.Enum, Cols: []string{"s.direction"}},
		"status": {Type: filters.Enum, Cols: []string{"s.status"}},
		"amt":    {Type: filters.Number, Cols: []string{"s.total_amount"}},
		"out":    {Type: filters.Number, Cols: []string{"(s.total_amount - s.settled_amount)"}},
		"diff":   {Type: filters.Number, Cols: []string{"abs(s.total_amount - s.external_total)"}},
	},
	DirectParams: map[string]string{
		"direction": "s.direction", "status": "s.status",
		"counterparty_type": "s.counterparty_type", "counterparty_id": "s.counterparty_id::text",
	},
	DefaultOrder: "ORDER BY s.created_at DESC, s.id",
}

// Statements GET /api/v1/finance/statements（列表模式，无 lines 明细）
func (h *Handler) Statements(md *masterdata.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { md.List(w, r, statementsCfg) }
}
