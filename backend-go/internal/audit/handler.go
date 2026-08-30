// Package audit 审计日志读路径（仅管理员），复用通用列表引擎。
package audit

import (
	"net/http"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
)

var logsCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT a.id::text AS id, a.actor_id::text AS actor, COALESCE(u.username,'') AS actor_name,
       a.action, a.resource_type, a.resource_id, a.request_id, a.method, a.path,
       a.status_code, host(a.ip) AS ip, a.payload, a.created_at`,
	FromClause:   "FROM audit_log a LEFT JOIN accounts_user u ON u.id = a.actor_id",
	SearchCols:   []string{"a.action", "a.path", "a.resource_id", "a.request_id"},
	OrderingCols: map[string]string{"created_at": "a.created_at", "status_code": "a.status_code"},
	FilterFields: map[string]filters.FilterField{},
	DirectParams: map[string]string{
		"action": "a.action", "resource_type": "a.resource_type", "resource_id": "a.resource_id",
		"actor": "a.actor_id::text", "status_code": "a.status_code::text", "method": "a.method",
	},
	DefaultOrder: "ORDER BY a.created_at DESC, a.id",
}

// LogsCfg / LogsWrite 供详情复用；审计日志只读，任何写口都不该开
var LogsCfg = logsCfg
var LogsWrite = masterdata.WriteCfg{
	ReadPerm: "org.rbac",
	Table:    "audit_log", Model: "AuditLog", Verbose: "审计日志", Alias: "a", ReadOnly: true,
}

// Detail GET /api/v1/audit-logs/{id}（同样限管理员）
func Detail(svc *auth.Service, md *masterdata.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me, err := svc.UserByID(r.Context(), auth.UserID(r))
		if err != nil || !me.IsStaff {
			httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "仅管理员可查审计日志")
			return
		}
		md.Retrieve(w, r, LogsCfg, LogsWrite)
	}
}

// Logs GET /api/v1/audit-logs（IsAdminUser：is_staff 校验后走通用引擎）
func Logs(svc *auth.Service, md *masterdata.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me, err := svc.UserByID(r.Context(), auth.UserID(r))
		if err != nil || !me.IsStaff {
			httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "仅管理员可查审计日志")
			return
		}
		md.List(w, r, logsCfg)
	}
}
