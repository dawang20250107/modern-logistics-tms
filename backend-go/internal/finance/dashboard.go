package finance

// 大屏财务指标：GET /finance/dashboard-metrics?days=7 —— 对齐 FinancialDashboardMetricsView。
// 读侧聚合（营收/成本/毛利按日趋势 + 成本科目构成）；财务写路径冻结不受影响。

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

var finCST = time.FixedZone("CST", 8*3600)

var costItems = map[string]string{
	"TRANSPORT_COST": "运费", "FUEL_CARD": "油卡", "TOLL": "过路费", "LOADING": "装卸费",
	"DETENTION": "押车费", "INFO_FEE": "信息费", "RECEIPT_FEE": "回单费", "DEDUCTION": "扣款",
	"EXCEPTION_COST": "异常费用", "OTHER_COST": "其他成本",
}

// DashboardMetrics GET /api/v1/finance/dashboard-metrics
func (h *Handler) DashboardMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	days := 7
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		days = v
	}
	if days < 1 {
		days = 1
	}
	endD, _ := time.Parse("2006-01-02", time.Now().In(finCST).Format("2006-01-02"))
	startD := endD.AddDate(0, 0, -(days - 1))
	s, e := startD.Format("2006-01-02"), endD.Format("2006-01-02")

	daily := func(direction string) map[string]float64 {
		out := map[string]float64{}
		rows, err := h.DB.Query(ctx, `
			SELECT (created_at AT TIME ZONE 'Asia/Shanghai')::date::text, COALESCE(sum(amount),0)::float8
			FROM fin_expense_record
			WHERE direction=$1 AND (created_at AT TIME ZONE 'Asia/Shanghai')::date BETWEEN $2::date AND $3::date
			GROUP BY 1`, direction, s, e)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var d string
			var v float64
			if rows.Scan(&d, &v) != nil {
				return out
			}
			out[d] = v
		}
		return out
	}
	rev, cost := daily("receivable"), daily("payable")

	trend := make([]map[string]any, 0, days)
	for i := 0; i < days; i++ {
		d := startD.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		rv, cv := rev[key], cost[key]
		profit := float64(int64((rv-cv)*100+ternary(rv-cv >= 0, 0.5, -0.5))) / 100
		trend = append(trend, map[string]any{
			"date": d.Format("01-02"), "revenue": rv, "cost": cv, "profit": profit,
		})
	}

	// 成本构成：应付按科目聚合（Django 口径：仅 start 下界，无上界），零额不展示
	composition := []map[string]any{}
	rows, err := h.DB.Query(ctx, `
		SELECT COALESCE(expense_item_code,''), COALESCE(sum(amount),0)::float8
		FROM fin_expense_record
		WHERE direction='payable' AND (created_at AT TIME ZONE 'Asia/Shanghai')::date >= $1::date
		GROUP BY 1 ORDER BY sum(amount) DESC`, s)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var code string
			var v float64
			if rows.Scan(&code, &v) != nil {
				break
			}
			if v <= 0 {
				continue
			}
			name := costItems[code]
			if name == "" {
				name = code
				if code == "" {
					name = "未分类"
				}
			}
			composition = append(composition, map[string]any{"name": name, "value": v})
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"trend":            trend,
		"cost_composition": composition,
		"period":           fmt.Sprintf("近 %d 天", days),
	})
}

func ternary(cond bool, a, b float64) float64 {
	if cond {
		return a
	}
	return b
}
