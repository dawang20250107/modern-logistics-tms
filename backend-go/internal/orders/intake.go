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
	parseMeta := map[string]any{}
	if body.Text != "" {
		for k, v := range parseTextRule(body.Text) {
			data[k] = v
		}
		parseMeta["source"] = "rule" // Go 侧建单走规则解析（DeepSeek 解析见差异清单）
	}
	explicitCustomer := ""
	for k, v := range body.Fields {
		if k == "customer" {
			explicitCustomer = fmt.Sprint(v)
			continue
		}
		if k == "_meta" { // 显式 _meta 覆盖解析元信息（对齐 explicit.pop("_meta", …)）
			if m, ok := v.(map[string]any); ok && len(m) > 0 {
				parseMeta = m
			}
			continue
		}
		data[k] = v
	}
	customerID := h.matchCustomer(ctx, data, channel, source, explicitCustomer)
	enrich(data)

	orderID, code, msg := h.createOrder(ctx, createParams{
		RawText: body.Text, Data: data, Channel: channel, Source: source, Status: status,
		CustomerID: customerID, CargoItems: body.CargoItems, Stops: body.Stops, ActorID: me.ID,
		ParseMeta: parseMeta,
	})
	if code != "" {
		httpx.Err(w, http.StatusInternalServerError, code, msg)
		return
	}

	// 回读完整序列化（与列表同一 SQL 面），201 返回
	h.respondOne(w, r, orderID, me)
}

// respondOne 建单后回读完整序列化（复用列表同一 SELECT 面），HTTP 201。
func (h *Handler) respondOne(w http.ResponseWriter, r *http.Request, orderID string, me *auth.UserRow) {
	h.respondOneStatus(w, r, orderID, me, http.StatusCreated)
}

func (h *Handler) respondOneStatus(w http.ResponseWriter, r *http.Request, orderID string, me *auth.UserRow, code int) {
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
		httpx.JSON(w, code, it)
		return
	}
	httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "订单未找到")
}
