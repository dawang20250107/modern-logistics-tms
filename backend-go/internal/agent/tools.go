package agent

// Agent 工具注册表（对齐 apps/ai/services/tools.py）。
//
// 原则不变：模型只能「请求」工具，业务执行在服务端完成并返回 evidence 证据链；
// 命中风险时落 AgentSuggestion 等待人工确认，AI 不自动落地高风险动作。
//
// 与 Python 版的结构性差异：工具自带的 JSON Schema 直接作为 LLM function 声明透传，
// 无需 LangChain 的 pydantic 适配层（那 77 行在这里整体消失）。

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	riskLow  = "low"
	riskHigh = "high"
)

// ToolSpec 工具声明；Fn 执行业务并返回结构化结果（含 evidence / suggestion）
type ToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
	Risk        string
	Fn          func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr)
}

type toolErr struct {
	Status  int
	Code    string
	Message string
}

var waybillSchema = map[string]any{
	"type":       "object",
	"required":   []any{"waybill_no"},
	"properties": map[string]any{"waybill_no": map[string]any{"type": "string"}},
}

// registry 保序：/agent/tools 的输出顺序与 Django 注册序一致
var registry []ToolSpec
var registryByName = map[string]*ToolSpec{}

func register(s ToolSpec) {
	if s.Risk == "" {
		s.Risk = riskLow
	}
	registry = append(registry, s)
	registryByName[s.Name] = &registry[len(registry)-1]
}

// ListTools 工具清单（不含实现）
func ListTools() []map[string]any {
	out := make([]map[string]any, 0, len(registry))
	for _, s := range registry {
		out = append(out, map[string]any{
			"name": s.Name, "description": s.Description,
			"input_schema": s.InputSchema, "risk": s.Risk,
		})
	}
	return out
}

// ExecuteTool 校验必填参数后执行
func ExecuteTool(ctx context.Context, db *pgxpool.Pool, name string, args map[string]any) (map[string]any, *toolErr) {
	spec, ok := registryByName[name]
	if !ok {
		return nil, &toolErr{404, "UNKNOWN_AGENT_TOOL", "未知工具：" + name}
	}
	if args == nil {
		args = map[string]any{}
	}
	if req, ok := spec.InputSchema["required"].([]any); ok {
		for _, f := range req {
			key := fmt.Sprint(f)
			if _, has := args[key]; !has {
				return nil, &toolErr{400, "INVALID_ARGUMENTS", "缺少必填参数：" + key}
			}
		}
	}
	return spec.Fn(ctx, db, args)
}

// ── 共用查询 ────────────────────────────────────────────

type wbCtx struct {
	ID, No, RouteName, RiskLevel, Status, ReceiptStatus string
	CarrierName, DriverName                             string
	EtaDrift                                            int
	PlannedArrival, EstimatedArrival                    *time.Time
	VehicleID                                           *string
}

func getWaybill(ctx context.Context, db *pgxpool.Pool, args map[string]any) (*wbCtx, *toolErr) {
	no, _ := args["waybill_no"].(string)
	w := &wbCtx{}
	err := db.QueryRow(ctx, `
		SELECT w.id::text, w.waybill_no, w.route_name, w.risk_level, w.status, w.receipt_status,
		       COALESCE(ca.name,''), COALESCE(d.name,''), w.eta_drift_minutes,
		       w.planned_arrival, w.estimated_arrival, w.vehicle_id::text
		FROM ops_waybill w
		LEFT JOIN md_carrier ca ON ca.id = w.carrier_id
		LEFT JOIN md_driver d ON d.id = w.driver_id
		WHERE w.waybill_no = $1`, no).
		Scan(&w.ID, &w.No, &w.RouteName, &w.RiskLevel, &w.Status, &w.ReceiptStatus,
			&w.CarrierName, &w.DriverName, &w.EtaDrift, &w.PlannedArrival, &w.EstimatedArrival, &w.VehicleID)
	if err == pgx.ErrNoRows {
		return nil, &toolErr{404, "WAYBILL_NOT_FOUND", "运单不存在。"}
	}
	if err != nil {
		return nil, &toolErr{500, "INTERNAL", "读取运单失败"}
	}
	return w, nil
}

// createSuggestion 落 AgentSuggestion（人工确认闭环入口）
func createSuggestion(ctx context.Context, db *pgxpool.Pool, waybillID, sType, title, body string,
	evidence map[string]any, toolName string) map[string]any {
	id, _ := uuid.NewV7()
	ej, _ := json.Marshal(evidence)
	if _, err := db.Exec(ctx, `
		INSERT INTO ai_agent_suggestion (id, created_at, updated_at, waybill_id, suggestion_type,
		  title, body, status, evidence, tool_name)
		VALUES ($1, now(), now(), $2::uuid, $3, $4, $5, 'pending', $6, $7)`,
		id.String(), waybillID, sType, title, body, ej, toolName); err != nil {
		return nil
	}
	return map[string]any{
		"suggestion_id": id.String(), "suggestion_type": sType, "title": title,
		"body": body, "status": "pending", "evidence": evidence,
	}
}

// pyISO 复刻 Python datetime.isoformat()：时区永远是数字偏移（+00:00，绝不是 Z），
// 微秒为 0 时不带小数部分、非 0 时补满 6 位。evidence 会落库并直接展示，需逐字符一致。
func pyISO(t time.Time) string {
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05-07:00")
	}
	return t.Format("2006-01-02T15:04:05.000000-07:00")
}

func isoOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return pyISO(*t)
}

// ── 工具实现 ────────────────────────────────────────────

func init() {
	register(ToolSpec{
		Name: "logistics.eta_risk_analysis", Description: "分析运单 ETA 偏移与路线风险。",
		InputSchema: waybillSchema,
		Fn: func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
			w, e := getWaybill(ctx, db, args)
			if e != nil {
				return nil, e
			}
			highRisk := w.RiskLevel == "high" || w.RiskLevel == "medium"
			driftHours := math.Round(float64(w.EtaDrift)/60*10) / 10
			evidence := map[string]any{
				"waybill_no": w.No, "route_name": w.RouteName, "risk_level": w.RiskLevel,
				"eta_drift_minutes": w.EtaDrift,
				"planned_arrival":   isoOrNil(w.PlannedArrival), "estimated_arrival": isoOrNil(w.EstimatedArrival),
			}
			var body string
			var suggestion map[string]any
			var nextActions []string
			if highRisk {
				body = fmt.Sprintf("%s ETA 偏移 %s 小时，建议确认司机路线、拥堵情况并向客户同步 ETA。", w.No, trimFloat(driftHours))
				suggestion = createSuggestion(ctx, db, w.ID, "eta_risk", "ETA 或路线风险待确认", body, evidence, "logistics.eta_risk_analysis")
				nextActions = []string{"contact_driver", "notify_customer", "monitor_next_location_event"}
			} else {
				body = fmt.Sprintf("%s 当前无活跃 ETA 风险。", w.No)
				nextActions = []string{"continue_monitoring"}
			}
			return map[string]any{
				"tool_name": "logistics.eta_risk_analysis", "waybill_no": w.No,
				"risk_detected": highRisk, "summary": body, "next_actions": nextActions,
				"evidence": evidence, "suggestion": suggestion,
			}, nil
		},
	})

	register(ToolSpec{
		Name: "logistics.receipt_reminder", Description: "生成回单催收建议。",
		InputSchema: waybillSchema,
		Fn: func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
			w, e := getWaybill(ctx, db, args)
			if e != nil {
				return nil, e
			}
			pending := w.ReceiptStatus == "pending"
			evidence := map[string]any{
				"waybill_no": w.No, "receipt_status": w.ReceiptStatus,
				"carrier_name": w.CarrierName, "driver_name": w.DriverName,
			}
			var body string
			var suggestion map[string]any
			var nextActions []string
			if pending {
				body = fmt.Sprintf("%s 电子回单待确认，建议提醒承运商上传并触发 OCR 复核。", w.No)
				suggestion = createSuggestion(ctx, db, w.ID, "receipt_reminder", "回单催收待处理", body, evidence, "logistics.receipt_reminder")
				nextActions = []string{"send_carrier_reminder", "schedule_ocr_review"}
			} else {
				body = fmt.Sprintf("%s 回单状态非待处理。", w.No)
				nextActions = []string{"continue_monitoring"}
			}
			return map[string]any{
				"tool_name": "logistics.receipt_reminder", "waybill_no": w.No,
				"reminder_required": pending, "summary": body, "next_actions": nextActions,
				"evidence": evidence, "suggestion": suggestion,
			}, nil
		},
	})

	register(ToolSpec{
		Name: "finance.expense_risk_check", Description: "检查运单成本毛利与可疑费用记录。",
		InputSchema: waybillSchema,
		Fn: func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
			w, e := getWaybill(ctx, db, args)
			if e != nil {
				return nil, e
			}
			var recv, pay, ext float64
			var riskyCount int
			_ = db.QueryRow(ctx, `
				SELECT COALESCE(sum(amount) FILTER (WHERE direction='receivable'),0)::float8,
				       COALESCE(sum(amount) FILTER (WHERE direction='payable'),0)::float8,
				       COALESCE(sum(amount) FILTER (WHERE direction='external'),0)::float8,
				       count(*) FILTER (WHERE risk_status <> 'normal')
				FROM fin_expense_record WHERE waybill_id=$1::uuid`, w.ID).Scan(&recv, &pay, &ext, &riskyCount)
			gross := recv - pay - ext
			margin := 0.0
			if recv != 0 {
				margin = gross / recv
			}
			riskDetected := margin < 0.12 || riskyCount > 0
			evidence := map[string]any{
				"waybill_no": w.No, "receivable_total": recv, "payable_total": pay,
				"external_total": ext, "gross_profit": gross, "gross_margin": margin,
				"risky_expense_count": riskyCount,
			}
			var body string
			var suggestion map[string]any
			var nextActions []string
			if riskDetected {
				// 对齐 Python f"{margin:.2%}"：百分号格式，两位小数
				body = fmt.Sprintf("%s 毛利率 %.2f%%，建议复核成本凭证与外部费用记录。", w.No, margin*100)
				suggestion = createSuggestion(ctx, db, w.ID, "expense_risk", "费用风险待复核", body, evidence, "finance.expense_risk_check")
				nextActions = []string{"review_cost_proof", "hold_payment_request"}
			} else {
				body = fmt.Sprintf("%s 成本结构在当前阈值内。", w.No)
				nextActions = []string{"continue_settlement"}
			}
			return map[string]any{
				"tool_name": "finance.expense_risk_check", "waybill_no": w.No,
				"risk_detected": riskDetected, "summary": body, "next_actions": nextActions,
				"evidence": evidence, "suggestion": suggestion,
			}, nil
		},
	})

	register(ToolSpec{
		Name: "logistics.exception_analysis", Description: "分析运单异常的可能原因与责任建议。",
		InputSchema: waybillSchema,
		Fn: func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
			w, e := getWaybill(ctx, db, args)
			if e != nil {
				return nil, e
			}
			var trackingCount, openExc int
			_ = db.QueryRow(ctx, `
				SELECT (SELECT count(*) FROM ops_tracking_point WHERE waybill_id=$1::uuid),
				       (SELECT count(*) FROM ops_exception WHERE waybill_id=$1::uuid AND status <> 'closed')`,
				w.ID).Scan(&trackingCount, &openExc)
			causes := []string{}
			if w.EtaDrift >= 240 {
				causes = append(causes, "严重延误，疑似偏航或拥堵")
			} else if w.EtaDrift > 0 {
				causes = append(causes, "存在延误")
			}
			if trackingCount == 0 && w.Status == "in_transit" {
				causes = append(causes, "在途但无轨迹，疑似 GPS 离线")
			}
			if openExc > 0 {
				causes = append(causes, fmt.Sprintf("%d 个未关闭异常待处理", openExc))
			}
			party := "none"
			body := "未发现明显异常。"
			if len(causes) > 0 {
				party = "carrier"
				body = strings.Join(causes, "；")
			}
			evidence := map[string]any{
				"eta_drift_minutes": w.EtaDrift, "tracking_points": trackingCount, "open_exceptions": openExc,
			}
			var suggestion map[string]any
			if len(causes) > 0 {
				suggestion = createSuggestion(ctx, db, w.ID, "exception_analysis", "异常分析", body, evidence, "logistics.exception_analysis")
			}
			return map[string]any{
				"tool_name": "logistics.exception_analysis", "waybill_no": w.No,
				"possible_causes": causes, "responsibility_suggestion": party,
				"summary": body, "evidence": evidence, "suggestion": suggestion,
			}, nil
		},
	})

	register(ToolSpec{
		Name:        "service.customer_reply_draft",
		Description: "生成面向客户的运单状态回复话术（DeepSeek 可用时调用，否则模板兜底）。",
		InputSchema: waybillSchema,
		Fn: func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
			w, e := getWaybill(ctx, db, args)
			if e != nil {
				return nil, e
			}
			eta := "待定"
			if w.EstimatedArrival != nil {
				eta = pyISO(*w.EstimatedArrival)
			}
			context_ := fmt.Sprintf("运单号 %s，线路 %s，当前状态 %s，预计到达 %s。", w.No, w.RouteName, w.Status, eta)
			draft := fmt.Sprintf("您好，您的运单 %s（%s）当前状态为 %s，预计 %s 送达。如有疑问请随时联系我们。",
				w.No, w.RouteName, w.Status, eta)
			source := "template"
			if c := DefaultClient(); c.IsConfigured() {
				if reply, err := c.SimpleChat(ctx,
					"你是物流客服，用简洁、礼貌的中文回复客户运单状态。", context_); err == nil && reply != "" {
					draft, source = reply, "deepseek"
				} else {
					source = "fallback"
				}
			}
			evidence := map[string]any{"context": context_, "source": source}
			suggestion := createSuggestion(ctx, db, w.ID, "customer_reply", "客服话术草稿", draft, evidence, "service.customer_reply_draft")
			return map[string]any{
				"tool_name": "service.customer_reply_draft", "waybill_no": w.No,
				"draft": draft, "source": source, "suggestion": suggestion,
			}, nil
		},
	})

	register(ToolSpec{
		Name:        "telematics.vehicle_alert_summary",
		Description: "汇总运单关联车辆的未处理车联网报警（超速/温度/油量/离线/疲劳等），用于风险归因。",
		InputSchema: waybillSchema,
		Fn: func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
			w, e := getWaybill(ctx, db, args)
			if e != nil {
				return nil, e
			}
			rows, err := db.Query(ctx, `
				SELECT alert_type, level, message, triggered_at FROM tel_alert
				WHERE waybill_id=$1::uuid AND status='open'
				ORDER BY triggered_at DESC LIMIT 20`, w.ID)
			if err != nil {
				return nil, &toolErr{500, "INTERNAL", "读取报警失败"}
			}
			defer rows.Close()
			byType := map[string]int{}
			recent := []map[string]any{}
			total, high := 0, 0
			for rows.Next() {
				var aType, level, msg string
				var at time.Time
				if rows.Scan(&aType, &level, &msg, &at) != nil {
					break
				}
				total++
				byType[aType]++
				if level == "high" {
					high++
				}
				if len(recent) < 5 {
					recent = append(recent, map[string]any{
						"type": aType, "level": level, "message": msg, "at": pyISO(at),
					})
				}
			}
			evidence := map[string]any{
				"waybill_no": w.No, "open_alert_count": total, "high_count": high,
				"by_type": byType, "recent": recent,
			}
			riskDetected := high > 0
			var body string
			if total > 0 {
				body = fmt.Sprintf("%s 关联车辆有 %d 条未处理报警（高危 %d）：%s。", w.No, total, high, pyDict(byType))
			} else {
				body = fmt.Sprintf("%s 关联车辆当前无未处理报警。", w.No)
			}
			var suggestion map[string]any
			if riskDetected {
				suggestion = createSuggestion(ctx, db, w.ID, "vehicle_alert", "车辆报警待核实", body, evidence, "telematics.vehicle_alert_summary")
			}
			return map[string]any{
				"tool_name": "telematics.vehicle_alert_summary", "waybill_no": w.No,
				"risk_detected": riskDetected, "summary": body,
				"evidence": evidence, "suggestion": suggestion,
			}, nil
		},
	})

	register(ToolSpec{
		Name:        "analytics.query_metric",
		Description: "查询经营/运营指标（运单量/在途/准时率/风险率/运力在线率/利用率/报警数/订单量/转化率/应收/应付/对账差异等）。",
		InputSchema: map[string]any{
			"type": "object", "required": []any{"metric_code"},
			"properties": map[string]any{
				"metric_code": map[string]any{"type": "string", "description": "指标 code，如 ops.on_time_rate"},
				"days":        map[string]any{"type": "integer", "description": "统计区间天数，默认 30"},
			},
		},
		Fn: func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
			code, _ := args["metric_code"].(string)
			days := 30
			switch v := args["days"].(type) {
			case float64:
				if int(v) > 0 {
					days = int(v)
				}
			case int:
				if v > 0 {
					days = v
				}
			}
			m, e := computeMetric(ctx, db, code, days)
			if e != nil {
				return nil, e
			}
			return map[string]any{
				"tool_name": "analytics.query_metric", "metric_code": code,
				"value": m["value"], "unit": m["unit"],
				"summary":  fmt.Sprintf("%s（近%d天）= %s%s", m["name"], days, fmtNum(m["value"]), m["unit"]),
				"evidence": m, "suggestion": nil,
			}, nil
		},
	})
}

// pyDict 复刻 Python dict 的 str() 形态（工具摘要里内嵌了 by_type 的字面量）
func pyDict(m map[string]int) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("'%s': %d", k, m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	return s
}

// fmtNum 复刻 Python 的数值字符串形态：int 无小数点，float 恒带（125980.0 / 0.0）
func fmtNum(v any) string {
	switch x := v.(type) {
	case float64:
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	default:
		return fmt.Sprint(v)
	}
}

// notPortedTools 两个尚未原生化的工具声明——保持 /agent/tools 清单与 Django 完全一致，
// 执行时透传回 Django（dispatch_recommendation 依赖报价规则引擎、
// intelligent_consolidation 依赖拼单配载算法，各随所属域移植后接管）。
var notPortedTools = []map[string]any{
	{
		"name":         "logistics.dispatch_recommendation",
		"description":  "为运单推荐可用车辆/司机并预估成本毛利。",
		"input_schema": waybillSchema,
		"risk":         riskLow,
	},
	{
		"name":        "logistics.intelligent_consolidation",
		"description": "运行智能 B2B 拼单配载与最省算路算法，将同向 LTL 小单合并配载 FTL 卡车，输出降本方案与预计节省金额。",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city_filter": map[string]any{"type": "string", "description": "可选：指定始发或目的城市名称（如'无锡'），过滤匹配的拼单建议"},
			},
		},
		"risk": riskLow,
	},
}

// mergedToolList 按 Django 注册序输出完整 9 个工具
func mergedToolList() []map[string]any {
	order := []string{
		"logistics.eta_risk_analysis", "logistics.receipt_reminder", "finance.expense_risk_check",
		"logistics.dispatch_recommendation", "logistics.exception_analysis", "service.customer_reply_draft",
		"telematics.vehicle_alert_summary", "analytics.query_metric", "logistics.intelligent_consolidation",
	}
	byName := map[string]map[string]any{}
	for _, t := range ListTools() {
		byName[t["name"].(string)] = t
	}
	for _, t := range notPortedTools {
		byName[t["name"].(string)] = t
	}
	out := make([]map[string]any, 0, len(order))
	for _, n := range order {
		if t, ok := byName[n]; ok {
			out = append(out, t)
		}
	}
	return out
}
