package finance

// 批次一键对账：POST /api/v1/dispatch-batches/{id}/statement
//
// 批次 = 一次委托同一承运商的商务归集，天然对应一个承运商应付分组，
// 对账口径直接取派单时落下的议定应付快照，可解释、可追溯。
// 幂等：批次已生成过对账单则直接返回原单，不重复归集。

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// BatchStatement POST /api/v1/dispatch-batches/{id}/statement {external_total}
func (h *Handler) BatchStatement(w http.ResponseWriter, r *http.Request) {
	if h.Svc.Guard(w, r, PermManage, denyManage) == nil {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No DispatchBatch matches the given query.")
		return
	}
	var body struct {
		ExternalTotal any `json:"external_total"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	external := orZeroStr(decString(body.ExternalTotal))

	var carrierID *string
	var carrierName, existingNo string
	if err := h.DB.QueryRow(ctx, `
		SELECT b.carrier_id::text, COALESCE(c.name,''), COALESCE(b.statement_no,'')
		FROM ops_dispatch_batch b LEFT JOIN md_carrier c ON c.id = b.carrier_id
		WHERE b.id = $1::uuid`, id).Scan(&carrierID, &carrierName, &existingNo); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No DispatchBatch matches the given query.")
		return
	}
	if carrierID == nil {
		httpx.Err(w, http.StatusBadRequest, "BATCH_NO_CARRIER", "网货平台批次无承运商，暂不支持一键对账。")
		return
	}
	// 幂等：已出过就回原单
	if existingNo != "" {
		var sid string
		if h.DB.QueryRow(ctx, `SELECT id::text FROM fin_statement WHERE statement_no=$1`, existingNo).Scan(&sid) == nil {
			h.respondBatchStatement(w, r, sid, existingNo, true)
			return
		}
	}

	var stmtID, stmtNo string
	err := h.inTx(ctx, func(tx pgx.Tx) error {
		no, err := nextStatementNo(ctx, tx)
		if err != nil {
			return err
		}
		stmtNo = no
		sid, _ := uuid.NewV7()
		stmtID = sid.String()
		// 账期取本批次应付流水的发生日跨度；一条流水都没有时回落批次创建日
		if _, err := tx.Exec(ctx, `
			INSERT INTO fin_statement (id, created_at, updated_at, statement_no, direction,
			  counterparty_type, counterparty_id, counterparty_name,
			  period_start, period_end, total_amount, item_count, external_total,
			  settled_amount, status, scope_type, scope_id, scope_name,
			  -- 归属跟着批次走。漏了这一列，对账单落成 NULL，
			  -- 而 NULL 只有「全部」档看得见——生成它的人自己在对账中心里
			  -- 都找不到它。主生成路径（GenerateStatement）是按明细所属运单
			  -- 的组织算的，这条批次路径一直没跟上。
			  organization_id)
			SELECT $1::uuid, now(), now(), $2, 'payable', 'carrier', $3, $4,
			  COALESCE(agg.min_day, (b.created_at AT TIME ZONE 'Asia/Shanghai')::date),
			  COALESCE(agg.max_day, (b.created_at AT TIME ZONE 'Asia/Shanghai')::date),
			  COALESCE(agg.total, 0), COALESCE(agg.n, 0), $5::numeric,
			  0, 'draft', 'batch', NULL, b.batch_no, b.organization_id
			FROM ops_dispatch_batch b
			LEFT JOIN LATERAL (
			  SELECT min((e.occurred_at AT TIME ZONE 'Asia/Shanghai')::date) AS min_day,
			         max((e.occurred_at AT TIME ZONE 'Asia/Shanghai')::date) AS max_day,
			         sum(e.amount) AS total, count(*)::int AS n
			  FROM fin_expense_record e JOIN ops_waybill w ON w.id = e.waybill_id
			  WHERE e.direction='payable' AND w.batch_id = b.id
			) agg ON true
			WHERE b.id = $6::uuid`,
			stmtID, no, *carrierID, carrierName, external, id); err != nil {
			return err
		}
		// 明细逐条落账；已进过别的对账单的流水由库级唯一索引挡住，不会被重复收录
		if _, err := tx.Exec(ctx, `
			INSERT INTO fin_statement_line (id, created_at, updated_at, statement_id, expense_record_id,
			  waybill_no, expense_item_code, amount, occurred_at, is_anomaly, baseline_avg, deviation_pct)
			SELECT gen_random_uuid(), now(), now(), $1::uuid, e.id,
			  COALESCE(w.waybill_no,''), e.expense_item_code, e.amount, e.occurred_at, false, NULL, NULL
			FROM fin_expense_record e JOIN ops_waybill w ON w.id = e.waybill_id
			WHERE e.direction='payable' AND w.batch_id = $2::uuid
			ORDER BY e.occurred_at, e.id
			ON CONFLICT DO NOTHING`, stmtID, id); err != nil {
			return err
		}
		// 明细可能因唯一索引被挡掉，总额与条数按实际入账重算，别让表头自说自话
		if _, err := tx.Exec(ctx, `
			UPDATE fin_statement s SET
			  total_amount = COALESCE((SELECT sum(amount) FROM fin_statement_line WHERE statement_id=s.id), 0),
			  item_count = (SELECT count(*) FROM fin_statement_line WHERE statement_id=s.id),
			  updated_at = now()
			WHERE s.id = $1::uuid`, stmtID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE ops_dispatch_batch SET statement_no=$2, updated_at=now() WHERE id=$1::uuid`, id, no)
		return err
	})
	if err != nil {
		httpx.Fail(w, r, "INTERNAL", "生成对账单失败", err)
		return
	}
	h.respondBatchStatement(w, r, stmtID, stmtNo, false)
}

func (h *Handler) respondBatchStatement(w http.ResponseWriter, r *http.Request, id, no string, reused bool) {
	var raw []byte
	if err := h.DB.QueryRow(r.Context(), statementJSON, id).Scan(&raw); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	var stmt map[string]any
	_ = json.Unmarshal(raw, &stmt)
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"statement_no": no, "reused": reused, "statement": stmt,
	})
}

// decString 把 JSON 数值/字符串归一成十进制字面量
func decString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		b, _ := json.Marshal(x)
		return string(b)
	}
	return ""
}
