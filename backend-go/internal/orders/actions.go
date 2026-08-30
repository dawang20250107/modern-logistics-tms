package orders

// 订单剩余写动作：
//   POST   /orders/{id}/approve · /reject        主管审批闸
//   POST   /orders/{id}/split                    按货物明细拆单
//   POST   /orders/merge                         合单
//   POST   /orders/batch · /batch-update         批量操作与批量改字段
//   POST   /orders/import                        批量建单（逐行隔离）
//   GET/POST /orders/{id}/attachments            附件列表 / 上传
//   DELETE /orders/{id}/attachments/{att_id}     删附件
//
// 对齐 apps/ops/intake.{approve_order, reject_order, split_order, merge_orders,
// batch_orders, batch_update_orders, import_orders} 与 OrderViewSet.attachments。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// maxBatchSize 对齐 intake.MAX_BATCH_SIZE
const maxBatchSize = 500

// notSplittable 已派单/完成/取消的订单不可拆/合
var notSplittable = []string{"converted", "completed", "cancelled"}

func inList(s string, xs []string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// approvalGate approve/reject 共用：仅待审批可动，落审批事件后回整份订单
func (h *Handler) approvalGate(w http.ResponseWriter, r *http.Request, approved bool) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	id, ok := h.resolveOrder(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Remark string `json:"remark"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	var status string
	_ = h.DB.QueryRow(ctx, `SELECT approval_status FROM ops_order WHERE id=$1::uuid`, id).Scan(&status)
	if status != "pending" {
		httpx.Err(w, http.StatusConflict, "NOT_PENDING_APPROVAL", "订单不在待审批状态。")
		return
	}
	var sql, eventType string
	if approved {
		sql = `UPDATE ops_order SET approval_status='approved', approval_remark=$2,
		       approved_by_id=$3::uuid, approved_at=now(), updated_at=now() WHERE id=$1::uuid`
		eventType = "approved"
	} else {
		sql = `UPDATE ops_order SET approval_status='rejected', approval_remark=$2,
		       updated_at=now() WHERE id=$1::uuid`
		eventType = "rejected"
	}
	args := []any{id, body.Remark}
	if approved {
		args = append(args, me.ID)
	}
	if _, err := h.DB.Exec(ctx, sql, args...); err != nil {
		httpx.Fail(w, r, "INTERNAL", "写入失败", err)
		return
	}
	h.orderEvent(ctx, id, eventType, "", "", me.ID, "approval", map[string]any{"remark": body.Remark})
	h.respondOneStatus(w, r, id, me, http.StatusOK)
}

// Approve POST /api/v1/orders/{id}/approve
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) { h.approvalGate(w, r, true) }

// RejectApproval POST /api/v1/orders/{id}/reject
func (h *Handler) RejectApproval(w http.ResponseWriter, r *http.Request) { h.approvalGate(w, r, false) }

// inTx 把一段写操作裹进事务：任一步失败整体回滚
func (h *Handler) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// respondMany 按 id 顺序回一组订单序列化
func (h *Handler) respondMany(w http.ResponseWriter, r *http.Request, ids []string, me *auth.UserRow, code int) {
	ctx := r.Context()
	isChief, _ := h.isChiefDispatcher(ctx, me)
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		rows, err := h.DB.Query(ctx, selectOrderSQL+fromClause+" WHERE o.id = $1::uuid", id)
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
			return
		}
		if rows.Next() {
			it, err := scanOrder(rows, me.ID, isChief)
			if err != nil {
				rows.Close()
				httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
				return
			}
			out = append(out, it)
		}
		rows.Close()
	}
	httpx.JSON(w, code, out)
}

// orderEvent 落一条订单事件（非事务版，供不需要原子性的动作用）
func (h *Handler) orderEvent(ctx context.Context, orderID, eventType, from, to, actorID, source string, payload map[string]any) {
	if payload == nil {
		payload = map[string]any{}
	}
	pj, _ := json.Marshal(payload)
	eid, _ := uuid.NewV7()
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO ops_order_event (id, created_at, updated_at, event_time, order_id, event_type,
		  from_status, to_status, actor_id, source, payload)
		VALUES ($1, now(), now(), clock_timestamp(), $2::uuid, $3, $4, $5, $6::uuid, $7, $8)`,
		eid.String(), orderID, eventType, from, to, nilIfBlank(actorID), source, pj); err != nil {
		slog.Warn("订单写库失败", "err", err)
	}
}

// spawnOrder 以蓝本订单表头新建订单（不含货物/站点），供拆单/合单复用。
// 返回新订单主键与单号。
func (h *Handler) spawnOrder(ctx context.Context, tx pgx.Tx, parentID, status, actorID string, quoted string) (string, string, error) {
	no, err := nextNo(ctx, tx, "DD")
	if err != nil {
		return "", "", err
	}
	id, _ := uuid.NewV7()
	// 表头整列复制：白名单之外的列（单号/状态/建单人/审批/认领等）显式重置
	cols := make([]string, 0, len(orderFields))
	for _, f := range orderFields {
		if f == "quoted_amount" {
			continue // 由调用方给定（拆单归 0，合单取合计）
		}
		cols = append(cols, f)
	}
	sel := joinComma(cols)
	pooledAt := "NULL"
	if status == "pooled" {
		pooledAt = "now()"
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO ops_order (id, created_at, updated_at, order_no, channel, source, customer_id,
		  created_by_id, status, pooled_at, quoted_amount, %s,
		  raw_text, ai_conversation_id, parse_meta, approval_status, approval_remark, is_deleted,
		  sla_status, cod_status)
		SELECT $1::uuid, now(), now(), $2, p.channel, p.source, p.customer_id,
		  $3::uuid, $4, %s, $5::numeric, %s,
		  '', '', '{}'::jsonb, 'none', '', false,
		  'pending', 'none'
		FROM ops_order p WHERE p.id = $6::uuid`,
		sel, pooledAt, prefixCols("p.", cols)), id.String(), no, nilIfBlank(actorID), status, quoted, parentID); err != nil {
		return "", "", err
	}
	return id.String(), no, nil
}

func prefixCols(prefix string, cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = prefix + c
	}
	return joinComma(out)
}

// copyStops 把站点整体复制到目标订单
func copyStops(ctx context.Context, tx pgx.Tx, srcID, dstID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO ops_order_stop (id, created_at, updated_at, order_id, seq, stop_type, city, address,
		  contact_name, contact_phone, expected_start, expected_end, cargo_note)
		SELECT gen_random_uuid(), now(), now(), $2::uuid, s.seq, s.stop_type, s.city, s.address,
		  s.contact_name, s.contact_phone, s.expected_start, s.expected_end, s.cargo_note
		FROM ops_order_stop s WHERE s.order_id = $1::uuid`, srcID, dstID)
	return err
}

// recomputeCargo 有货物明细时按明细回写订单货量/件数/体积
func recomputeCargo(ctx context.Context, tx pgx.Tx, orderID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE ops_order o SET
		  cargo_quantity = COALESCE(t.q, 0),
		  cargo_weight_ton = COALESCE(t.w, 0),
		  cargo_volume_cbm = COALESCE(t.v, 0),
		  updated_at = now()
		FROM (SELECT sum(quantity) q, sum(weight_ton) w, sum(volume_cbm) v
		      FROM ops_order_cargo_item WHERE order_id = $1::uuid) t
		WHERE o.id = $1::uuid
		  AND EXISTS (SELECT 1 FROM ops_order_cargo_item WHERE order_id = $1::uuid)`, orderID)
	return err
}

// Split POST /api/v1/orders/{id}/split {groups:[{cargo_item_ids:[...]}]}
func (h *Handler) Split(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	id, ok := h.resolveOrder(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Groups []struct {
			CargoItemIDs []string `json:"cargo_item_ids"`
		} `json:"groups"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	var status, orderNo string
	_ = h.DB.QueryRow(ctx, `SELECT status, order_no FROM ops_order WHERE id=$1::uuid`, id).Scan(&status, &orderNo)
	if inList(status, notSplittable) {
		httpx.Err(w, http.StatusConflict, "ORDER_NOT_SPLITTABLE", "已派单/完成/取消的订单不可拆单。")
		return
	}
	items := map[string]bool{}
	rows, err := h.DB.Query(ctx, `SELECT id::text FROM ops_order_cargo_item WHERE order_id=$1::uuid`, id)
	if err == nil {
		for rows.Next() {
			var i string
			if rows.Scan(&i) != nil {
				break
			}
			items[i] = true
		}
		rows.Close()
	}
	if len(items) < 2 {
		httpx.Err(w, http.StatusConflict, "SPLIT_NEEDS_ITEMS", "需至少 2 项货物明细才能拆单。")
		return
	}
	valid := [][]string{}
	for _, g := range body.Groups {
		picked := []string{}
		for _, i := range g.CargoItemIDs {
			if items[i] {
				picked = append(picked, i)
			}
		}
		if len(picked) > 0 {
			valid = append(valid, picked)
		}
	}
	if len(valid) < 2 {
		httpx.Err(w, http.StatusBadRequest, "SPLIT_NEEDS_GROUPS", "至少拆成两组（每组至少一项货物）。")
		return
	}
	// 每一项货物明细都必须被分到某一组，且只能进一组。
	//
	// 拆单会把明细搬到子订单、然后把原单作废。没被任何一组选中的明细
	// 就留在那张已作废的原单上——从流程里消失，不报错、不提示。
	// 实测 3 项只分 2 项：60 件 / 12 吨，拆完只剩 30 件 / 6 吨，而接口回 201。
	//
	// 界面上不会发出这种请求（未分组的默认归第 1 组），但这个端点对所有
	// 已鉴权调用方开放，而"货不能凭空少"该由服务端保证。
	assigned := map[string]bool{}
	dupCount := 0
	for _, g := range valid {
		for _, i := range g {
			if assigned[i] {
				dupCount++
			}
			assigned[i] = true
		}
	}
	if dupCount > 0 {
		// 同一项被分进两组：搬运是"最后一次写入生效"，等于凭空少一份货
		httpx.Err(w, http.StatusBadRequest, "SPLIT_ITEM_DUPLICATED",
			fmt.Sprintf("有 %d 项货物被分到了多个组里，请检查分组。", dupCount))
		return
	}
	if missing := len(items) - len(assigned); missing > 0 {
		httpx.Err(w, http.StatusBadRequest, "SPLIT_ITEMS_UNASSIGNED",
			fmt.Sprintf("还有 %d 项货物没有分到任何一组。拆单会作废原单，"+
				"没分组的货会跟着原单一起失效——请把每一项都分到组里。", missing))
		return
	}

	childIDs := []string{}
	childNos := []string{}
	err = h.inTx(ctx, func(tx pgx.Tx) error {
		for _, ids := range valid {
			cid, cno, err := h.spawnOrder(ctx, tx, id, status, me.ID, "0")
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE ops_order_cargo_item SET order_id=$1::uuid, updated_at=now() WHERE id::text = ANY($2)`,
				cid, ids); err != nil {
				return err
			}
			if err := recomputeCargo(ctx, tx, cid); err != nil {
				return err
			}
			if err := copyStops(ctx, tx, id, cid); err != nil {
				return err
			}
			if err := txEvent(ctx, tx, cid, "created", "", status, me.ID, "split",
				map[string]any{"split_from": orderNo}); err != nil {
				return err
			}
			childIDs = append(childIDs, cid)
			childNos = append(childNos, cno)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE ops_order SET status='cancelled', updated_at=now() WHERE id=$1::uuid`, id); err != nil {
			return err
		}
		return txEvent(ctx, tx, id, "split", status, "", me.ID, "ops",
			map[string]any{"children": childNos})
	})
	if err != nil {
		httpx.Fail(w, r, "INTERNAL", "拆单失败", err)
		return
	}
	h.respondMany(w, r, childIDs, me, http.StatusCreated)
}

// Merge POST /api/v1/orders/merge {ids:[...]}
func (h *Handler) Merge(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ids := []string{}
	for _, i := range body.IDs {
		if _, err := uuid.Parse(i); err == nil {
			ids = append(ids, i)
		}
	}
	type ord struct{ id, no, status, quoted string }
	list := []ord{}
	if len(ids) > 0 {
		// Django 的 filter(id__in=...) 无显式排序 → 取模型默认序 -created_at
		rows, err := h.DB.Query(ctx, `
			SELECT id::text, order_no, status, quoted_amount::text FROM ops_order
			WHERE id::text = ANY($1) AND NOT is_deleted ORDER BY created_at DESC, id`, ids)
		if err == nil {
			for rows.Next() {
				var o ord
				if rows.Scan(&o.id, &o.no, &o.status, &o.quoted) != nil {
					break
				}
				list = append(list, o)
			}
			rows.Close()
		}
	}
	if len(list) < 2 {
		httpx.Err(w, http.StatusBadRequest, "MERGE_NEEDS_ORDERS", "至少选择 2 张订单合并。")
		return
	}
	for _, o := range list {
		if inList(o.status, notSplittable) {
			httpx.Err(w, http.StatusConflict, "ORDER_NOT_MERGEABLE",
				fmt.Sprintf("订单 %s 当前状态不可合并。", o.no))
			return
		}
	}
	base := list[0]
	var mergedID string
	nos := make([]string, len(list))
	for i, o := range list {
		nos[i] = o.no
	}
	err = h.inTx(ctx, func(tx pgx.Tx) error {
		var totalQuote string
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(sum(quoted_amount),0)::text FROM ops_order WHERE id::text = ANY($1)`,
			ids).Scan(&totalQuote); err != nil {
			return err
		}
		mid, mno, err := h.spawnOrder(ctx, tx, base.id, base.status, me.ID, totalQuote)
		if err != nil {
			return err
		}
		mergedID = mid
		for _, o := range list {
			if _, err := tx.Exec(ctx,
				`UPDATE ops_order_cargo_item SET order_id=$1::uuid, updated_at=now() WHERE order_id=$2::uuid`,
				mid, o.id); err != nil {
				return err
			}
			if err := copyStops(ctx, tx, o.id, mid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE ops_order SET status='cancelled', updated_at=now() WHERE id=$1::uuid`, o.id); err != nil {
				return err
			}
			if err := txEvent(ctx, tx, o.id, "merged", o.status, "", me.ID, "ops",
				map[string]any{"into": mno}); err != nil {
				return err
			}
		}
		if err := recomputeCargo(ctx, tx, mid); err != nil {
			return err
		}
		// 没有货物明细行的订单，货量只写在表头。
		//
		// recomputeCargo 带着 "EXISTS 明细行" 的条件（对拆单是对的），
		// 于是这种订单合完之后，新单的表头是从第一张源单**整列复制**来的：
		// 合并 A(3 件) 和 B(7 件) 得到一张写着 7 件的新单，另一张的货凭空没了。
		// 而这恰恰是最常见的订单形态——库里 5 万单只有 28 单有明细行。
		if _, err := tx.Exec(ctx, `
			UPDATE ops_order o SET
			  cargo_quantity = t.q, cargo_weight_ton = t.w, cargo_volume_cbm = t.v,
			  updated_at = now()
			FROM (SELECT COALESCE(sum(cargo_quantity),0) q,
			             COALESCE(sum(cargo_weight_ton),0) w,
			             COALESCE(sum(cargo_volume_cbm),0) v
			      FROM ops_order WHERE id::text = ANY($2)) t
			WHERE o.id = $1::uuid
			  AND NOT EXISTS (SELECT 1 FROM ops_order_cargo_item WHERE order_id = $1::uuid)`,
			mid, ids); err != nil {
			return err
		}
		return txEvent(ctx, tx, mid, "created", "", base.status, me.ID, "merge",
			map[string]any{"merged_from": nos})
	})
	if err != nil {
		httpx.Fail(w, r, "INTERNAL", "合单失败", err)
		return
	}
	h.respondOneStatus(w, r, mergedID, me, http.StatusCreated)
}

// Batch POST /api/v1/orders/batch {action, ids}
func (h *Handler) Batch(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.IDs) == 0 {
		httpx.Err(w, http.StatusBadRequest, "IDS_REQUIRED", "ids 必填。")
		return
	}
	if !inList(body.Action, []string{"confirm", "pool", "cancel", "delete"}) {
		httpx.Err(w, http.StatusBadRequest, "INVALID_BATCH_ACTION", "不支持的操作："+body.Action)
		return
	}
	if len(body.IDs) > maxBatchSize {
		httpx.Err(w, http.StatusBadRequest, "BATCH_TOO_LARGE",
			fmt.Sprintf("单次最多操作 %d 单，请分批。", maxBatchSize))
		return
	}
	ids := validUUIDs(body.IDs)
	ok := []string{}
	failed := []map[string]any{}
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, order_no, status, approval_status FROM ops_order
		WHERE id::text = ANY($1) AND NOT is_deleted ORDER BY created_at DESC, id`, ids)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	type row struct{ id, no, status, approval string }
	list := []row{}
	for rows.Next() {
		var x row
		if rows.Scan(&x.id, &x.no, &x.status, &x.approval) != nil {
			break
		}
		list = append(list, x)
	}
	rows.Close()

	for _, o := range list {
		code, msg := h.applyBatchAction(ctx, body.Action, o.id, o.status, o.approval, me.ID)
		if code != "" {
			failed = append(failed, map[string]any{"order_no": o.no, "error": msg})
			continue
		}
		ok = append(ok, o.no)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"action": body.Action, "ok": ok, "failed": failed, "ok_count": len(ok),
	})
}

// applyBatchAction 单条执行；返回非空错误码表示该条失败（不影响其余）。
//
// 这里原先四个 UPDATE 全是 `_, _ = h.DB.Exec(...)`——**错误被丢掉，
// 然后一律 return "", ""（成功）**。于是批量确认 50 单时，哪怕每一条
// UPDATE 都失败，接口照样回 `ok_count: 50`，界面弹一句"已处理 50 单"，
// 而库里一条都没变。这套系统这一轮已经因为同一个写法栽过一次
// （回单状态重算，静默不生效）。
//
// 现在每条都判错并把失败原因带回去：批量操作的价值全在"哪几条没成功"上，
// 报一个笼统的成功数，等于把核对的活推给用户，而他手上没有核对的依据。
func (h *Handler) applyBatchAction(ctx context.Context, action, id, status, approval, actorID string) (string, string) {
	// exec 统一判错。失败时给出的是**这一条**为什么没成，不是整批的笼统失败。
	exec := func(sql string, args ...any) (string, string) {
		if _, err := h.DB.Exec(ctx, sql, args...); err != nil {
			slog.Error("批量操作写库失败", "action", action, "order", id, "err", err)
			return "DB_WRITE_FAILED", "写入失败，请重试。"
		}
		return "", ""
	}
	switch action {
	case "confirm":
		if status != "pending_confirm" && status != "confirmed" {
			return "INVALID_ORDER_STATUS", "仅待确认订单可确认。"
		}
		if code, msg := exec(`UPDATE ops_order SET status='confirmed', updated_at=now() WHERE id=$1::uuid`, id); code != "" {
			return code, msg
		}
		h.orderEvent(ctx, id, "confirmed", status, "confirmed", actorID, "cs", nil)
	case "pool":
		if status != "confirmed" && status != "pending_confirm" {
			return "INVALID_ORDER_STATUS", "仅已确认/待确认订单可进池。"
		}
		if approval == "pending" {
			return "ORDER_NEEDS_APPROVAL", "订单需主管审批通过后方可进池。"
		}
		if approval == "rejected" {
			return "ORDER_APPROVAL_REJECTED", "订单审批被驳回，不可进池。"
		}
		if code, msg := exec(`UPDATE ops_order SET status='pooled', pooled_at=now(), updated_at=now() WHERE id=$1::uuid`, id); code != "" {
			return code, msg
		}
		h.orderEvent(ctx, id, "pooled", status, "pooled", actorID, "cs", nil)
	case "cancel":
		if status == "converted" || status == "completed" {
			return "INVALID_ORDER_STATUS", "已派单/已完成订单不可取消。"
		}
		if code, msg := exec(`UPDATE ops_order SET status='cancelled', updated_at=now() WHERE id=$1::uuid`, id); code != "" {
			return code, msg
		}
		h.orderEvent(ctx, id, "cancelled", status, "cancelled", actorID, "cs", nil)
	case "delete":
		if code, msg := exec(
			`UPDATE ops_order SET is_deleted=true, deleted_at=now(), updated_at=now() WHERE id=$1::uuid`, id); code != "" {
			return code, msg
		}
	}
	return "", ""
}

// batchFieldChoices 批量可改字段白名单（取自模型 choices，避免脏值入库）
var batchFieldChoices = map[string][]string{
	"priority":        {"normal", "urgent", "vip"},
	"settlement_type": {"monthly", "cash", "prepaid"},
}

// BatchUpdate POST /api/v1/orders/batch-update {field, value, ids}
func (h *Handler) BatchUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	ctx := r.Context()
	if _, err := h.Svc.UserByID(ctx, auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Field string   `json:"field"`
		Value string   `json:"value"`
		IDs   []string `json:"ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.IDs) == 0 {
		httpx.Err(w, http.StatusBadRequest, "IDS_REQUIRED", "ids 必填。")
		return
	}
	choices, ok := batchFieldChoices[body.Field]
	if !ok {
		httpx.Err(w, http.StatusBadRequest, "INVALID_BATCH_FIELD", "不支持批量修改字段："+body.Field)
		return
	}
	if !inList(body.Value, choices) {
		httpx.Err(w, http.StatusBadRequest, "INVALID_FIELD_VALUE",
			fmt.Sprintf("字段 %s 的取值非法：%s", body.Field, body.Value))
		return
	}
	if len(body.IDs) > maxBatchSize {
		httpx.Err(w, http.StatusBadRequest, "BATCH_TOO_LARGE",
			fmt.Sprintf("单次最多操作 %d 单，请分批。", maxBatchSize))
		return
	}
	ids := validUUIDs(body.IDs)
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, order_no, status FROM ops_order
		WHERE id::text = ANY($1) AND NOT is_deleted ORDER BY created_at DESC, id`, ids)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	okList := []string{}
	failed := []map[string]any{}
	type row struct{ id, no, status string }
	list := []row{}
	for rows.Next() {
		var x row
		if rows.Scan(&x.id, &x.no, &x.status) != nil {
			break
		}
		list = append(list, x)
	}
	rows.Close()
	for _, o := range list {
		if inList(o.status, notSplittable) {
			failed = append(failed, map[string]any{"order_no": o.no, "error": "订单已派单/完成/取消，不可批量改。"})
			continue
		}
		// 字段名来自白名单，拼进 SQL 安全
		if _, err := h.DB.Exec(ctx,
			"UPDATE ops_order SET "+body.Field+"=$2, updated_at=now() WHERE id=$1::uuid", o.id, body.Value); err != nil {
			failed = append(failed, map[string]any{"order_no": o.no, "error": err.Error()})
			continue
		}
		okList = append(okList, o.no)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"field": body.Field, "value": body.Value, "ok": okList, "failed": failed, "ok_count": len(okList),
	})
}

func validUUIDs(in []string) []string {
	out := []string{}
	for _, s := range in {
		if _, err := uuid.Parse(s); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// Import POST /api/v1/orders/import {rows:[...], channel, source}
//
// 逐行建单、失败隔离：一行脏数据不该让整批白跑。
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	if !h.allowAny(w, r, "waybill.create", "waybill.manage") {
		return
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		Rows    []any  `json:"rows"`
		Channel string `json:"channel"`
		Source  string `json:"source"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.Rows) == 0 {
		httpx.Err(w, http.StatusBadRequest, "IMPORT_EMPTY", "rows 必须是非空数组。")
		return
	}
	if len(body.Rows) > maxBatchSize {
		httpx.Err(w, http.StatusBadRequest, "BATCH_TOO_LARGE",
			fmt.Sprintf("单次最多导入 %d 行，请分批。", maxBatchSize))
		return
	}
	channel := body.Channel
	if channel == "" {
		channel = "cs"
	}
	ok := []map[string]any{}
	failed := []map[string]any{}
	for idx, raw := range body.Rows {
		row, isMap := raw.(map[string]any)
		if !isMap {
			failed = append(failed, map[string]any{"row": idx, "error": "行数据必须是对象"})
			continue
		}
		data := map[string]any{}
		for k, v := range row {
			data[k] = v
		}
		enrich(data)
		custID := h.matchCustomer(ctx, data, channel, body.Source, str(row, "customer"))
		id, code, msg := h.createOrder(ctx, createParams{
			Data: data, Channel: channel, Source: body.Source, Status: "pending_confirm",
			CustomerID: custID, ActorID: me.ID,
			CargoItems: mapList(row["cargo_items"]), Stops: mapList(row["stops"]),
			ParseMeta: map[string]any{"source": "import"},
		})
		if code != "" {
			failed = append(failed, map[string]any{"row": idx, "error": msg})
			continue
		}
		var no string
		_ = h.DB.QueryRow(ctx, `SELECT order_no FROM ops_order WHERE id=$1::uuid`, id).Scan(&no)
		ok = append(ok, map[string]any{"row": idx, "order_no": no})
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"ok": ok, "failed": failed, "ok_count": len(ok), "failed_count": len(failed),
	})
}

func mapList(v any) []map[string]any {
	items, _ := v.([]any)
	out := []map[string]any{}
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// Attachments GET/POST /api/v1/orders/{id}/attachments
func (h *Handler) Attachments(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	id, ok := h.resolveOrder(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if r.Method == http.MethodGet {
		list, err := h.childRows(ctx, attachmentSelect+" WHERE a.order_id=$1::uuid ORDER BY a.created_at, a.id", id)
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取附件失败")
			return
		}
		httpx.JSON(w, http.StatusOK, normalizeAttachments(list))
		return
	}
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	kind, name, fileURL, fileRel := "other", "", "", ""
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if v := str(body, "kind"); v != "" {
			kind = v
		}
		name, fileURL = str(body, "name"), str(body, "file_url")
	} else {
		_ = r.ParseMultipartForm(32 << 20)
		if v := r.FormValue("kind"); v != "" {
			kind = v
		}
		name, fileURL = r.FormValue("name"), r.FormValue("file_url")
		// 这里原先只取了文件名，字节直接丢掉：库里 file='' ，
		// 前端拿到的 file_display 是空串，那一行渲染成不可点的纯文字，
		// 而 toast 照样说"附件已上传"。合同、磅单、回单是这门生意的凭证，
		// 上传的人不会去点开验一遍，等到对账要证据那天才发现没有——
		// 那时候文件早就找不着了。
		if f, fh, err := r.FormFile("file"); err == nil {
			defer f.Close()
			if name == "" {
				name = fh.Filename
			}
			buf, rerr := io.ReadAll(io.LimitReader(f, attachmentMaxBytes+1))
			if rerr != nil {
				httpx.Err(w, http.StatusBadRequest, "UPLOAD_FAILED", "文件读取失败。")
				return
			}
			if int64(len(buf)) > attachmentMaxBytes {
				httpx.Err(w, http.StatusBadRequest, "FILE_TOO_LARGE", "文件过大，请控制在 32MB 内。")
				return
			}
			// 文件名不进存放路径：用户可以传"../../x"这种名字。
			// 只留扩展名，且扩展名本身也要过滤掉分隔符。
			rel := "attachments/" + uuid.NewString() + safeExt(fh.Filename)
			if err := h.store().Put(ctx, rel, bytes.NewReader(buf),
				int64(len(buf)), http.DetectContentType(buf)); err != nil {
				// 存不下就必须报错，不能静默建一条没有文件的附件行——
				// 那正是修之前的行为，用户以为存下了。
				slog.Error("附件写入失败", "err", err, "order", id)
				httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "文件保存失败，请重试。")
				return
			}
			fileRel = rel
		}
	}
	aid, _ := uuid.NewV7()
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO ops_order_attachment (id, created_at, updated_at, order_id, kind, name,
		  file, file_url, uploaded_by_id)
		VALUES ($1, now(), now(), $2::uuid, $3, $4, $5, $6, $7::uuid)`,
		aid.String(), id, kind, name, fileRel, fileURL, nilIfBlank(me.ID)); err != nil {
		httpx.Fail(w, r, "INTERNAL", "写入失败", err)
		return
	}
	list, _ := h.childRows(ctx, attachmentSelect+" WHERE a.id=$1::uuid", aid.String())
	out := normalizeAttachments(list)
	if len(out) == 0 {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, out[0])
}

const attachmentSelect = `
SELECT a.id::text AS id, a.order_id::text AS "order", a.kind, a.name,
       NULLIF(a.file,'') AS file, a.file_url,
       -- file_display：有落盘文件走 /media/，否则回落外链（对齐 get_file_display）
       (CASE WHEN a.file <> '' THEN '/media/' || a.file ELSE a.file_url END) AS file_display,
       COALESCE(u.username,'') AS uploaded_by_name, a.created_at
FROM ops_order_attachment a LEFT JOIN accounts_user u ON u.id = a.uploaded_by_id`

// normalizeAttachments 补齐 childRows 会丢掉的空值键（NULL 不入 map）
func normalizeAttachments(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		for _, k := range []string{"id", "order", "kind", "name", "file_url", "file_display", "uploaded_by_name"} {
			if _, ok := m[k]; !ok {
				m[k] = ""
			}
		}
		if _, ok := m["file"]; !ok {
			m["file"] = nil // FileField 无文件时 DRF 输出 null，不是空串
		}
		out = append(out, m)
	}
	return out
}

// attachmentMaxBytes 单个附件上限。与前端 ParseMultipartForm 的 32MB 对齐。
const attachmentMaxBytes = 32 << 20

// safeExt 从用户给的文件名里取一个可以安全拼进存放路径的扩展名。
//
// filepath.Ext("../../etc/passwd") 会给出 ""，但 Ext("x.tar/../../y") 之类
// 仍可能带出分隔符，所以拿到之后再筛一遍：只留字母数字。
// 拿不到干净扩展名就不要——存放键本来就是 uuid，扩展名只是方便人看。
func safeExt(filename string) string {
	ext := filepath.Ext(filename)
	if len(ext) < 2 || len(ext) > 12 {
		return ""
	}
	for _, c := range ext[1:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return ""
		}
	}
	return ext
}

// DeleteAttachment DELETE /api/v1/orders/{id}/attachments/{att_id}
func (h *Handler) DeleteAttachment(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	id, ok := h.resolveOrder(w, r)
	if !ok {
		return
	}
	attID := chi.URLParam(r, "att_id")
	if _, err := uuid.Parse(attID); err == nil {
		// 先取存放键再删行：删完就没地方查了，文件会一直留在盘上/桶里。
		// 存放键是每行一个 uuid，不会有第二行引用它，删掉是安全的。
		var rel string
		_ = h.DB.QueryRow(r.Context(),
			`SELECT COALESCE(file,'') FROM ops_order_attachment WHERE id=$1::uuid AND order_id=$2::uuid`,
			attID, id).Scan(&rel)
		if _, derr := h.DB.Exec(r.Context(),
			`DELETE FROM ops_order_attachment WHERE id=$1::uuid AND order_id=$2::uuid`, attID, id); derr == nil && rel != "" {
			_ = h.store().Delete(r.Context(), rel)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
