package masterdata

// 证件到期预警：GET /credentials/expiring?days=30 —— 对齐 ExpiringCredentialsView。
// 车辆(年检/保险/维保) + 司机(驾照/从业资格) + 承运商(承运资质)，
// days_left 负数=已过期；severity expired/critical(≤7)/warning；组内按 days_left 稳定排序。

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

var credCST = time.FixedZone("CST", 8*3600)

func hasPerm(perms []string, want string) bool {
	for _, p := range perms {
		if p == "*" || p == want {
			return true
		}
	}
	return false
}

func severity(daysLeft int) string {
	if daysLeft < 0 {
		return "expired"
	}
	if daysLeft <= 7 {
		return "critical"
	}
	return "warning"
}

// ExpiringCredentials GET /api/v1/credentials/expiring
func (h *Handler) ExpiringCredentials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil || !hasPerm(perms, "masterdata.view") {
		httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "无主数据查看权限")
		return
	}
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		days = v
	}
	today, _ := time.Parse("2006-01-02", time.Now().In(credCST).Format("2006-01-02"))
	deadline := today.AddDate(0, 0, days)

	entry := func(subjectKey, subject, credential, expiry string) map[string]any {
		et, _ := time.Parse("2006-01-02", expiry)
		daysLeft := int(et.Sub(today).Hours() / 24)
		return map[string]any{
			"subject": subject, subjectKey: subject, "credential": credential,
			"expiry": expiry, "days_left": daysLeft, "severity": severity(daysLeft),
		}
	}
	collect := func(sql, subjectKey string, credentials []string) []map[string]any {
		out := []map[string]any{}
		rows, err := h.DB.Query(ctx, sql, deadline.Format("2006-01-02"))
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			subject := ""
			expiries := make([]*string, len(credentials))
			dest := make([]any, 0, len(credentials)+1)
			dest = append(dest, &subject)
			for i := range expiries {
				dest = append(dest, &expiries[i])
			}
			if rows.Scan(dest...) != nil {
				return out
			}
			for i, exp := range expiries {
				if exp != nil && *exp <= deadline.Format("2006-01-02") {
					out = append(out, entry(subjectKey, subject, credentials[i], *exp))
				}
			}
		}
		// Python list.sort 稳定排序：days_left 升序，同值保持行序（模型默认排序+字段声明序）
		sort.SliceStable(out, func(i, j int) bool { return out[i]["days_left"].(int) < out[j]["days_left"].(int) })
		return out
	}

	vehicles := collect(`SELECT plate_no, inspection_expiry::text, insurance_expiry::text, maintenance_due_date::text
		FROM md_vehicle WHERE is_active AND NOT is_deleted
		  AND LEAST(inspection_expiry, insurance_expiry, maintenance_due_date) <= $1::date
		ORDER BY plate_no`, "plate_no", []string{"年检", "保险", "维保"})
	drivers := collect(`SELECT name, license_expiry::text, qualification_expiry::text
		FROM md_driver WHERE is_active AND NOT is_deleted
		  AND LEAST(license_expiry, qualification_expiry) <= $1::date
		ORDER BY name`, "name", []string{"驾照", "从业资格"})
	carriers := collect(`SELECT name, qualification_expiry::text
		FROM md_carrier WHERE is_active AND NOT is_deleted AND qualification_expiry IS NOT NULL
		  AND qualification_expiry <= $1::date
		ORDER BY code`, "name", []string{"承运资质"})

	total := len(vehicles) + len(drivers) + len(carriers)
	expired, critical, warning := 0, 0, 0
	for _, group := range [][]map[string]any{vehicles, drivers, carriers} {
		for _, r := range group {
			switch r["severity"] {
			case "expired":
				expired++
			case "critical":
				critical++
			case "warning":
				warning++
			}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"days": days,
		"summary": map[string]any{
			"total": total, "expired": expired, "critical": critical, "warning": warning,
		},
		"vehicles": vehicles, "drivers": drivers, "carriers": carriers,
	})
}
