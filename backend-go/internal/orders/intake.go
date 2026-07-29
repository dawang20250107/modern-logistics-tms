package orders

// 建单写路径：POST /orders/intake，对齐 apps/ops/intake.create_order_from_intake。
// 文本解析走规则引擎（DeepSeek 增强属阶段 4 AI 域；当前环境 Django 侧同样走规则兜底）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

var (
	rePhone  = regexp.MustCompile(`1[3-9]\d{9}`)
	reWeight = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(?:吨|t|T)`)
	reVolume = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(?:方|立方|m3|cbm|CBM)`)
	reQty    = regexp.MustCompile(`(\d+)\s*(?:件|箱|托|板|pcs|PCS)`)
	reRoute  = regexp.MustCompile(`([\p{Han}A-Za-z]{2,10})\s*(?:到|至|发往|发|->|→|—|-)\s*([\p{Han}A-Za-z]{2,10})`)
	reCity   = regexp.MustCompile(`^([\p{Han}]{2,4}市?)`)
)

var orderFields = []string{
	"contact_name", "contact_phone", "origin", "destination",
	"pickup_address", "pickup_contact_name", "pickup_contact_phone",
	"delivery_address", "delivery_contact_name", "delivery_contact_phone",
	"cargo_desc", "cargo_quantity", "cargo_weight_ton", "cargo_volume_cbm",
	"cargo_value", "package_type", "is_hazardous", "temperature_range",
	"source_type", "business_type", "priority", "settlement_type", "quoted_amount",
	"freight_term", "freight_payer", "cod_amount",
	"expected_pickup_at", "expected_delivery_at", "remark",
}
var cargoItemFields = []string{"name", "quantity", "weight_ton", "volume_cbm", "package_type", "temperature_range", "remark"}
var stopFields = []string{"stop_type", "city", "address", "contact_name", "contact_phone", "expected_start", "expected_end", "cargo_note"}

var cities = []string{
	"北京", "上海", "广州", "深圳", "天津", "重庆", "杭州", "南京", "苏州", "无锡", "常州",
	"宁波", "合肥", "福州", "厦门", "南昌", "济南", "青岛", "郑州", "武汉", "长沙", "成都",
	"西安", "昆明", "贵阳", "南宁", "海口", "石家庄", "太原", "沈阳", "大连", "长春", "哈尔滨",
	"温州", "金华", "绍兴", "台州", "嘉兴", "泉州", "东莞", "佛山", "中山", "珠海", "惠州",
	"徐州", "扬州", "泰州", "南通", "盐城", "淮安", "镇江", "芜湖", "马鞍山",
}

var validStatuses = map[string]bool{
	"draft": true, "pending_confirm": true, "confirmed": true, "pooled": true,
	"dispatching": true, "converted": true, "completed": true, "cancelled": true,
}

func parseTextRule(text string) map[string]any {
	f := map[string]any{}
	if m := rePhone.FindString(text); m != "" {
		f["contact_phone"] = m
	}
	if m := reWeight.FindStringSubmatch(text); m != nil {
		v, _ := strconv.ParseFloat(m[1], 64)
		f["cargo_weight_ton"] = v
	}
	if m := reVolume.FindStringSubmatch(text); m != nil {
		v, _ := strconv.ParseFloat(m[1], 64)
		f["cargo_volume_cbm"] = v
	}
	if m := reQty.FindStringSubmatch(text); m != nil {
		v, _ := strconv.Atoi(m[1])
		f["cargo_quantity"] = v
	}
	if m := reRoute.FindStringSubmatch(text); m != nil {
		f["origin"], f["destination"] = m[1], m[2]
	}
	desc := strings.TrimSpace(text)
	if r := []rune(desc); len(r) > 255 {
		desc = string(r[:255])
	}
	f["cargo_desc"] = desc
	return f
}

func extractCity(address string) string {
	if address == "" {
		return ""
	}
	for _, c := range cities {
		if strings.Contains(address, c) {
			return c
		}
	}
	clean := strings.NewReplacer("江苏省", "", "浙江省", "", "广东省", "", "安徽省", "").Replace(address)
	if m := reCity.FindStringSubmatch(clean); m != nil {
		return strings.TrimSuffix(m[1], "市")
	}
	return ""
}

func str(m map[string]any, k string) string {
	if v, ok := m[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}
func numOf(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

// enrich 对齐 B2B 补全管道：地址推城市 + 货量均值补齐（enrich_cargo_metrics）
func enrich(data map[string]any) {
	for src, dst := range map[string]string{"pickup_address": "origin", "delivery_address": "destination"} {
		cur := str(data, dst)
		if cur == "" || cur == "-" || cur == "—" || cur == "无" {
			if c := extractCity(str(data, src)); c != "" {
				data[dst] = c
			}
		}
	}
	qty := int(numOf(data["cargo_quantity"]))
	desc := strings.ToLower(str(data, "cargo_desc") + " " + str(data, "package_type"))
	setIfZero := func(k string, v float64) {
		if numOf(data[k]) == 0 {
			data[k] = strconv.FormatFloat(v, 'f', 2, 64)
		}
	}
	if qty > 0 {
		wf, vf := 0.005, 0.01
		switch {
		case strings.ContainsAny(desc, "箱") || strings.Contains(desc, "box") || strings.Contains(desc, "carton"):
			wf, vf = 0.015, 0.025
		case strings.Contains(desc, "托") || strings.Contains(desc, "板") || strings.Contains(desc, "pallet") || strings.Contains(desc, "plt"):
			wf, vf = 0.400, 1.5
		case strings.Contains(desc, "袋") || strings.Contains(desc, "bag") || strings.Contains(desc, "sack"):
			wf, vf = 0.025, 0.03
		case strings.Contains(desc, "桶") || strings.Contains(desc, "barrel") || strings.Contains(desc, "drum"):
			wf, vf = 0.200, 0.35
		}
		setIfZero("cargo_weight_ton", float64(qty)*wf)
		setIfZero("cargo_volume_cbm", float64(qty)*vf)
	} else if desc != "" && (strings.Contains(desc, "整车") || strings.Contains(desc, "一车") || strings.Contains(desc, "ftl")) {
		setIfZero("cargo_weight_ton", 10.00)
	}
}

// matchCustomer 三级客户对齐：微信群/API 来源名 → 联系电话 → 模糊名称
func (h *Handler) matchCustomer(ctx context.Context, data map[string]any, channel, source, explicit string) *string {
	var id string
	if explicit != "" {
		if h.DB.QueryRow(ctx, "SELECT id::text FROM md_customer WHERE id=$1::uuid", explicit).Scan(&id) == nil {
			return &id
		}
	}
	if (channel == "wechat_group" || channel == "api") && source != "" {
		if h.DB.QueryRow(ctx, "SELECT id::text FROM md_customer WHERE wechat_group=$1 AND is_active LIMIT 1", source).Scan(&id) == nil {
			return &id
		}
	}
	if phone := str(data, "contact_phone") + str(data, "pickup_contact_phone") + str(data, "delivery_contact_phone"); phone != "" {
		p := str(data, "contact_phone")
		if p == "" {
			p = str(data, "pickup_contact_phone")
		}
		if p == "" {
			p = str(data, "delivery_contact_phone")
		}
		if h.DB.QueryRow(ctx, "SELECT id::text FROM md_customer WHERE contact_phone=$1 AND is_active LIMIT 1", p).Scan(&id) == nil {
			return &id
		}
	}
	name := str(data, "customer_name")
	if name == "" {
		name = str(data, "contact_name")
	}
	if len([]rune(name)) >= 2 {
		if h.DB.QueryRow(ctx, "SELECT id::text FROM md_customer WHERE name ILIKE '%'||$1||'%' AND is_active LIMIT 1", name).Scan(&id) == nil {
			return &id
		}
	}
	return nil
}

func nextNo(ctx context.Context, tx pgx.Tx, prefix string) (string, error) {
	day := time.Now().In(cstZone).Format("20060102")
	scope := fmt.Sprintf("%s:%s", map[string]string{"DD": "order", "YD": "waybill"}[prefix], day)
	var v int
	err := tx.QueryRow(ctx, `
		INSERT INTO ops_number_counter (scope, value) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET value = ops_number_counter.value + 1
		RETURNING value`, scope).Scan(&v)
	return fmt.Sprintf("%s%s%06d", prefix, day, v), err
}

var cstZone = time.FixedZone("CST", 8*3600)

func coerceDT(v string) any {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, v, cstZone); err == nil {
			return t
		}
	}
	return nil
}

// Intake POST /api/v1/orders/intake
func (h *Handler) Intake(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Text       string           `json:"text"`
		Fields     map[string]any   `json:"fields"`
		Channel    string           `json:"channel"`
		CargoItems []map[string]any `json:"cargo_items"`
		Stops      []map[string]any `json:"stops"`
		Status     string           `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.Err(w, http.StatusBadRequest, "VALIDATION", "请求体不是合法 JSON")
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Text == "" && len(body.Fields) == 0 && len(body.CargoItems) == 0 {
		httpx.Err(w, http.StatusBadRequest, "INTAKE_EMPTY", "text、fields 或 cargo_items 至少其一。")
		return
	}
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	// 来源标识由系统账号确立（组织·姓名），不接受前端手工填写
	sourceParts := []string{}
	if me.OrgName != nil && *me.OrgName != "" {
		sourceParts = append(sourceParts, *me.OrgName)
	}
	if me.Nickname != "" {
		sourceParts = append(sourceParts, me.Nickname)
	} else {
		sourceParts = append(sourceParts, me.Username)
	}
	source := strings.Join(sourceParts, "·")
	channel := body.Channel
	if channel == "" {
		channel = "cs"
	}
	status := body.Status
	if !validStatuses[status] {
		status = "pending_confirm"
	}

	data := map[string]any{}
	if body.Text != "" {
		for k, v := range parseTextRule(body.Text) {
			data[k] = v
		}
	}
	explicitCustomer := ""
	for k, v := range body.Fields {
		if k == "customer" {
			explicitCustomer = fmt.Sprint(v)
			continue
		}
		data[k] = v
	}
	customerID := h.matchCustomer(ctx, data, channel, source, explicitCustomer)
	enrich(data)

	// 白名单 + 时间字段解析（非法值丢弃，对齐 _coerce_datetimes）
	cols := []string{}
	vals := []any{}
	for _, k := range orderFields {
		v, ok := data[k]
		if !ok || v == nil || v == "" {
			continue
		}
		if k == "expected_pickup_at" || k == "expected_delivery_at" {
			if s, isStr := v.(string); isStr {
				if dt := coerceDT(s); dt != nil {
					v = dt
				} else {
					continue
				}
			}
		}
		cols = append(cols, k)
		vals = append(vals, v)
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orderNo, err := nextNo(ctx, tx, "DD")
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "取号失败")
		return
	}
	id, _ := uuid.NewV7()
	orderID := id.String()

	// 基础 INSERT（模型默认值全集），业务字段以“默认+覆盖”方式合并
	base := map[string]any{
		"id": orderID, "created_at": time.Now(), "updated_at": time.Now(), "is_deleted": false,
		"order_no": orderNo, "channel": channel, "source": source, "status": status,
		"source_type": "enterprise", "business_type": "ftl", "priority": "normal",
		"settlement_type": "monthly", "freight_term": "prepaid", "freight_payer": "shipper",
		"cod_amount": "0", "cod_status": "none",
		"contact_name": "", "contact_phone": "", "origin": "", "destination": "",
		"pickup_address": "", "pickup_contact_name": "", "pickup_contact_phone": "",
		"delivery_address": "", "delivery_contact_name": "", "delivery_contact_phone": "",
		"cargo_desc": "", "cargo_quantity": 0, "cargo_weight_ton": "0", "cargo_volume_cbm": "0",
		"cargo_value": "0", "package_type": "", "is_hazardous": false, "temperature_range": "",
		"quoted_amount": "0", "sla_status": "pending", "approval_status": "none", "approval_remark": "",
		"raw_text": body.Text, "ai_conversation_id": str(data, "ai_conversation_id"), "parse_meta": "{}", "remark": "",
		"customer_id": customerID, "created_by_id": me.ID,
	}
	for i, c := range cols {
		base[c] = vals[i]
	}
	insCols, insVals, phs := []string{}, []any{}, []string{}
	for c, v := range base {
		insCols = append(insCols, c)
		insVals = append(insVals, v)
		phs = append(phs, fmt.Sprintf("$%d", len(insVals)))
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO ops_order ("+strings.Join(insCols, ",")+") VALUES ("+strings.Join(phs, ",")+")", insVals...); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "建单失败："+err.Error())
		return
	}

	// 货物明细 + 汇总回写
	seq := 0
	for _, item := range body.CargoItems {
		if strings.TrimSpace(str(item, "name")) == "" {
			continue
		}
		seq++
		row := map[string]any{"quantity": 0, "weight_ton": "0", "volume_cbm": "0", "package_type": "", "temperature_range": "", "remark": ""}
		for _, k := range cargoItemFields {
			if v, ok := item[k]; ok && v != nil && v != "" {
				row[k] = v
			}
		}
		cid, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `
			INSERT INTO ops_order_cargo_item (id, created_at, updated_at, order_id, seq, name, quantity, weight_ton, volume_cbm, package_type, temperature_range, remark)
			VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			cid.String(), orderID, seq, row["name"], row["quantity"], row["weight_ton"], row["volume_cbm"],
			row["package_type"], row["temperature_range"], row["remark"]); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "货物明细写入失败")
			return
		}
	}
	if seq > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE ops_order o SET cargo_quantity = s.q, cargo_weight_ton = s.w, cargo_volume_cbm = s.v, updated_at = now()
			FROM (SELECT COALESCE(sum(quantity),0) q, COALESCE(sum(weight_ton),0) w, COALESCE(sum(volume_cbm),0) v
			      FROM ops_order_cargo_item WHERE order_id = $1) s WHERE o.id = $1`, orderID); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "货量汇总失败")
			return
		}
	}

	// 站点
	seq = 0
	for _, sp := range body.Stops {
		if str(sp, "address") == "" && str(sp, "city") == "" {
			continue
		}
		seq++
		row := map[string]any{"stop_type": "pickup", "city": "", "address": "", "contact_name": "", "contact_phone": "", "cargo_note": ""}
		var es, ee any
		for _, k := range stopFields {
			if v, ok := sp[k]; ok && v != nil && v != "" {
				if k == "expected_start" || k == "expected_end" {
					if s, isStr := v.(string); isStr {
						v = coerceDT(s)
					}
					if k == "expected_start" {
						es = v
					} else {
						ee = v
					}
					continue
				}
				row[k] = v
			}
		}
		sid, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `
			INSERT INTO ops_order_stop (id, created_at, updated_at, order_id, seq, stop_type, city, address, contact_name, contact_phone, expected_start, expected_end, cargo_note)
			VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			sid.String(), orderID, seq, row["stop_type"], row["city"], row["address"],
			row["contact_name"], row["contact_phone"], es, ee, row["cargo_note"]); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "站点写入失败")
			return
		}
	}

	// 建单事件 + 审批闸（报价≥5万 或 货值≥50万 → 待审批）
	evt := func(eventType, toStatus string, payload map[string]any) error {
		pj, _ := json.Marshal(payload)
		eid, _ := uuid.NewV7()
		_, err := tx.Exec(ctx, `
			INSERT INTO ops_order_event (id, created_at, updated_at, event_time, order_id, event_type, from_status, to_status, actor_id, source, payload)
			VALUES ($1, now(), now(), now(), $2, $3, '', $4, $5, $6, $7)`,
			eid.String(), orderID, eventType, toStatus, me.ID, map[bool]string{true: "cs", false: "system"}[eventType == "created"], pj)
		return err
	}
	if err := evt("created", status, map[string]any{"channel": channel}); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事件写入失败")
		return
	}
	var quoted, cargoValue float64
	var approvalPending bool
	_ = tx.QueryRow(ctx, "SELECT quoted_amount::float8, cargo_value::float8 FROM ops_order WHERE id=$1", orderID).Scan(&quoted, &cargoValue)
	if quoted >= 50000 || cargoValue >= 500000 {
		if _, err := tx.Exec(ctx, "UPDATE ops_order SET approval_status='pending', updated_at=now() WHERE id=$1", orderID); err == nil {
			approvalPending = true
			_ = evt("approval_required", "", map[string]any{
				"quoted_amount": strconv.FormatFloat(quoted, 'f', 2, 64), "cargo_value": strconv.FormatFloat(cargoValue, 'f', 2, 64),
			})
		}
	}
	_ = approvalPending

	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}

	// 回读完整序列化（与列表同一 SQL 面），201 返回
	h.respondOne(w, r, orderID, me)
}

// respondOne 建单后回读完整序列化（复用列表同一 SELECT 面），HTTP 201。
func (h *Handler) respondOne(w http.ResponseWriter, r *http.Request, orderID string, me *auth.UserRow) {
	ctx := r.Context()
	isChief, _ := h.isChiefDispatcher(ctx, me)
	rows, err := h.DB.Query(ctx, selectOrderSQL+fromClause+" WHERE o.id = $1::uuid", orderID)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	defer rows.Close()
	if rows.Next() {
		it, err := scanOrder(rows, me.ID, isChief)
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
			return
		}
		httpx.JSON(w, http.StatusCreated, it)
		return
	}
	httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "订单未找到")
}
