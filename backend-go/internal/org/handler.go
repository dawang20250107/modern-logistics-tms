package org

// 组织中台读路径：/org/* —— 对齐 apps/iam（organizations/employees/roles/
// service-areas/handovers/login-audit 列表复用 masterdata 通用引擎；
// tree/overview/rbac-matrix/route-resolve 为定制聚合）。

import (
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
)

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
	MD  *masterdata.Handler // 列表引擎
}

var orgTypeLabel = map[string]string{
	"group": "集团", "company": "公司", "region": "片区", "dept": "部门", "station": "网点",
}
var orgPropertyLabel = map[string]string{
	"self": "自营", "franchise": "加盟", "outsource": "外包", "partner": "合作", "jv": "合资",
}
var areaTypeLabel = map[string]string{
	"deliver": "派送区域", "transfer": "中转区域", "special": "特殊区域",
	"no_deliver": "不派送区域", "no_transfer": "不中转区域",
}

func hasPerm(perms []string, want string) bool {
	for _, p := range perms {
		if p == "*" || p == want {
			return true
		}
	}
	return false
}

// requirePerm 鉴权 + 权限点校验；失败时已写响应，返回 false
func (h *Handler) requirePerm(w http.ResponseWriter, r *http.Request, want string) bool {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return false
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil || !hasPerm(perms, want) {
		httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "无权限："+want)
		return false
	}
	return true
}

// ── 列表资源配置（SELECT 别名即 JSON 键）──

var organizationsCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT o.id::text AS id, o.name, o.short_name, o.code, o.type,
       (CASE o.type WHEN 'group' THEN '集团' WHEN 'company' THEN '公司' WHEN 'region' THEN '片区'
                    WHEN 'dept' THEN '部门' WHEN 'station' THEN '网点' ELSE o.type END) AS type_label,
       o.org_property,
       (CASE o.org_property WHEN 'self' THEN '自营' WHEN 'franchise' THEN '加盟' WHEN 'outsource' THEN '外包'
                            WHEN 'partner' THEN '合作' WHEN 'jv' THEN '合资' ELSE o.org_property END) AS org_property_label,
       o.parent_id::text AS parent, COALESCE(p.name,'') AS parent_name, o.path,
       o.province, o.city, o.district, o.address, o.lng::text AS lng, o.lat::text AS lat,
       o.manager_name, o.manager_phone, o.business_phone, o.service_phone,
       o.complaint_phone, o.receipt_return_address, o.sort_order, o.is_active`,
	FromClause: "FROM iam_organization o LEFT JOIN iam_organization p ON p.id = o.parent_id",
	SearchCols: []string{"o.code", "o.name", "o.short_name", "o.manager_name"},
	OrderingCols: map[string]string{
		"sort_order": "o.sort_order", "code": "o.code", "created_at": "o.created_at",
	},
	DirectParams: map[string]string{
		"type": "o.type", "org_property": "o.org_property", "is_active": "o.is_active", "parent": "o.parent_id::text",
	},
	DefaultOrder: "ORDER BY o.sort_order, o.code, o.id",
}

var rolesCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT r.id::text AS id, r.code, r.name, r.data_scope,
       (CASE r.data_scope WHEN 'self' THEN '仅本人' WHEN 'org' THEN '本组织'
                          WHEN 'org_sub' THEN '本组织及下级' WHEN 'all' THEN '全部' ELSE r.data_scope END) AS data_scope_label,
       COALESCE((SELECT json_agg(rp.permission_id::text ORDER BY rp.id)
                 FROM iam_role_permissions rp WHERE rp.role_id = r.id), '[]'::json) AS permissions,
       COALESCE((SELECT json_agg(pp.code ORDER BY rp.id)
                 FROM iam_role_permissions rp JOIN iam_permission pp ON pp.id = rp.permission_id
                 WHERE rp.role_id = r.id), '[]'::json) AS permission_codes,
       (SELECT count(*) FROM iam_role_permissions rp WHERE rp.role_id = r.id)::int AS permission_count,
       r.is_active`,
	FromClause:   "FROM iam_role r",
	SearchCols:   []string{"r.code", "r.name"},
	OrderingCols: map[string]string{"code": "r.code"},
	DirectParams: map[string]string{"is_active": "r.is_active", "data_scope": "r.data_scope"},
	DefaultOrder: "ORDER BY r.code, r.id",
}

var serviceAreasCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT a.id::text AS id, a.organization_id::text AS organization, COALESCE(o.name,'') AS organization_name,
       a.area_type,
       (CASE a.area_type WHEN 'deliver' THEN '派送区域' WHEN 'transfer' THEN '中转区域' WHEN 'special' THEN '特殊区域'
                         WHEN 'no_deliver' THEN '不派送区域' WHEN 'no_transfer' THEN '不中转区域' ELSE a.area_type END) AS area_type_label,
       a.province, a.city, a.district, a.region_code, a.region_name,
       a.priority, a.note, a.is_active`,
	FromClause:   "FROM iam_service_area a LEFT JOIN iam_organization o ON o.id = a.organization_id",
	SearchCols:   []string{"a.region_name", "a.region_code", "a.city", "a.province"},
	OrderingCols: map[string]string{"priority": "a.priority", "region_name": "a.region_name"},
	DirectParams: map[string]string{
		"organization": "a.organization_id::text", "area_type": "a.area_type",
		"is_active": "a.is_active", "province": "a.province", "city": "a.city",
	},
	DefaultOrder: "ORDER BY a.priority DESC, a.region_name, a.id",
}

var employeesCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT e.id::text AS id, e.employee_no, e.name, e.phone, e.email, e.id_no,
       e.organization_id::text AS organization, COALESCE(og.name,'') AS organization_name,
       e.department_id::text AS department, COALESCE(dp.name,'') AS department_name,
       e.supervisor_id::text AS supervisor, COALESCE(sp.name,'') AS supervisor_name,
       COALESCE((SELECT json_agg(eg.employeegroup_id::text ORDER BY eg.id)
                 FROM iam_employee_groups eg WHERE eg.employee_id = e.id), '[]'::json) AS groups,
       NULL::text AS group_names,
       e.position, e.status,
       (CASE e.status WHEN 'active' THEN '在职' WHEN 'disabled' THEN '停用' WHEN 'left' THEN '离职' ELSE e.status END) AS status_label,
       e.hire_date::text AS hire_date, e.leave_date::text AS leave_date,
       e.user_id::text AS "user", COALESCE(u.username,'') AS username,
       COALESCE(u.is_active, false) AS account_active,
       COALESCE((SELECT json_agg(DISTINCT ro.name)
                 FROM iam_role_assignment ra JOIN iam_role ro ON ro.id = ra.role_id
                 WHERE ra.user_id = e.user_id), '[]'::json) AS role_names`,
	FromClause: `FROM iam_employee e
LEFT JOIN iam_organization og ON og.id = e.organization_id
LEFT JOIN iam_department dp ON dp.id = e.department_id
LEFT JOIN iam_employee sp ON sp.id = e.supervisor_id
LEFT JOIN accounts_user u ON u.id = e.user_id`,
	SearchCols: []string{"e.employee_no", "e.name", "e.phone", "e.position"},
	OrderingCols: map[string]string{
		"employee_no": "e.employee_no", "hire_date": "e.hire_date", "created_at": "e.created_at",
	},
	DirectParams: map[string]string{
		"organization": "e.organization_id::text", "department": "e.department_id::text",
		"status": "e.status", "supervisor": "e.supervisor_id::text",
	},
	DefaultOrder: "ORDER BY e.employee_no, e.id",
}

var handoversCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT h.id::text AS id, h.from_employee_id::text AS from_employee, COALESCE(fe.name,'') AS from_name,
       h.to_employee_id::text AS to_employee, COALESCE(te.name,'') AS to_name,
       COALESCE(u.username,'') AS operator_name, h.reason, h.moved_reports, h.moved_departments,
       h.disabled_account, h.created_at`,
	FromClause: `FROM iam_account_handover h
LEFT JOIN iam_employee fe ON fe.id = h.from_employee_id
LEFT JOIN iam_employee te ON te.id = h.to_employee_id
LEFT JOIN accounts_user u ON u.id = h.operator_id`,
	OrderingCols: map[string]string{"created_at": "h.created_at"},
	DirectParams: map[string]string{
		"from_employee": "h.from_employee_id::text", "to_employee": "h.to_employee_id::text",
	},
	DefaultOrder: "ORDER BY h.created_at DESC, h.id",
}

var loginAuditCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT l.id::text AS id, l.username, l.username AS username_display, l.user_id::text AS "user",
       l.success, l.result,
       (CASE l.result WHEN 'success' THEN '成功' WHEN 'bad_credentials' THEN '凭据错误'
                      WHEN 'inactive' THEN '账号停用' WHEN 'locked' THEN '已锁定' ELSE l.result END) AS result_label,
       host(l.ip) AS ip, l.user_agent, l.created_at`,
	FromClause:   "FROM iam_login_attempt l",
	SearchCols:   []string{"l.username", "l.ip::text"},
	OrderingCols: map[string]string{"created_at": "l.created_at"},
	DirectParams: map[string]string{"success": "l.success", "result": "l.result", "username": "l.username"},
	DefaultOrder: "ORDER BY l.created_at DESC, l.id",
}

func (h *Handler) Organizations(w http.ResponseWriter, r *http.Request) {
	if h.requirePerm(w, r, "org.view") {
		h.MD.List(w, r, organizationsCfg)
	}
}
func (h *Handler) Roles(w http.ResponseWriter, r *http.Request) {
	if h.requirePerm(w, r, "org.rbac") {
		h.MD.List(w, r, rolesCfg)
	}
}
func (h *Handler) ServiceAreas(w http.ResponseWriter, r *http.Request) {
	if h.requirePerm(w, r, "org.view") {
		h.MD.List(w, r, serviceAreasCfg)
	}
}
func (h *Handler) Employees(w http.ResponseWriter, r *http.Request) {
	if h.requirePerm(w, r, "org.view") {
		h.MD.List(w, r, employeesCfg)
	}
}
func (h *Handler) Handovers(w http.ResponseWriter, r *http.Request) {
	if h.requirePerm(w, r, "org.view") {
		h.MD.List(w, r, handoversCfg)
	}
}
func (h *Handler) LoginAudit(w http.ResponseWriter, r *http.Request) {
	if h.requirePerm(w, r, "org.view") {
		h.MD.List(w, r, loginAuditCfg)
	}
}

// Tree GET /api/v1/org/organizations/tree —— 嵌套组织树 + 直属/子树在职人头
func (h *Handler) Tree(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.view") {
		return
	}
	ctx := r.Context()
	type orgRow struct {
		ID, Code, Name, Short, Type, Prop, Manager string
		Active                                     bool
		ParentID                                   *string
		Path                                       string
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, code, name, short_name, type, org_property, manager_name,
		       is_active, parent_id::text, path
		FROM iam_organization WHERE is_active ORDER BY sort_order, code`)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	var orgs []orgRow
	for rows.Next() {
		var o orgRow
		if rows.Scan(&o.ID, &o.Code, &o.Name, &o.Short, &o.Type, &o.Prop, &o.Manager, &o.Active, &o.ParentID, &o.Path) != nil {
			break
		}
		orgs = append(orgs, o)
	}
	rows.Close()

	counts := map[string]int{}
	crows, err := h.DB.Query(ctx, `SELECT organization_id::text, count(*)::int FROM iam_employee
		WHERE status='active' AND organization_id IS NOT NULL GROUP BY 1`)
	if err == nil {
		for crows.Next() {
			var id string
			var n int
			if crows.Scan(&id, &n) != nil {
				break
			}
			counts[id] = n
		}
		crows.Close()
	}

	nodes := map[string]map[string]any{}
	for _, o := range orgs {
		var parentID any
		if o.ParentID != nil {
			parentID = *o.ParentID
		}
		nodes[o.ID] = map[string]any{
			"id": o.ID, "code": o.Code, "name": o.Name, "short_name": o.Short,
			"type": o.Type, "type_label": orgTypeLabel[o.Type],
			"org_property": o.Prop, "org_property_label": orgPropertyLabel[o.Prop],
			"manager_name": o.Manager, "is_active": o.Active, "parent_id": parentID,
			"direct_headcount": counts[o.ID], "total_headcount": counts[o.ID],
			"children": []map[string]any{},
		}
	}
	roots := []map[string]any{}
	for _, o := range orgs {
		node := nodes[o.ID]
		if o.ParentID != nil {
			if parent, ok := nodes[*o.ParentID]; ok {
				parent["children"] = append(parent["children"].([]map[string]any), node)
				continue
			}
		}
		roots = append(roots, node)
	}
	// 自底向上累加子树人头（path 深度倒序）
	byDepth := make([]orgRow, len(orgs))
	copy(byDepth, orgs)
	sort.SliceStable(byDepth, func(i, j int) bool {
		return strings.Count(byDepth[i].Path, "/") > strings.Count(byDepth[j].Path, "/")
	})
	for _, o := range byDepth {
		if o.ParentID == nil {
			continue
		}
		if parent, ok := nodes[*o.ParentID]; ok {
			parent["total_headcount"] = parent["total_headcount"].(int) + nodes[o.ID]["total_headcount"].(int)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tree": roots, "total": len(orgs)})
}

// Overview GET /api/v1/org/overview
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.view") {
		return
	}
	ctx := r.Context()
	countMap := func(sql string) map[string]int {
		out := map[string]int{}
		rows, err := h.DB.Query(ctx, sql)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if rows.Scan(&k, &n) != nil {
				return out
			}
			out[k] = n
		}
		return out
	}
	scalar := func(sql string) int {
		var n int
		_ = h.DB.QueryRow(ctx, sql).Scan(&n)
		return n
	}
	byProp := countMap(`SELECT org_property, count(*)::int FROM iam_organization WHERE is_active GROUP BY 1`)
	byType := countMap(`SELECT type, count(*)::int FROM iam_organization WHERE is_active GROUP BY 1`)
	byStatus := countMap(`SELECT status, count(*)::int FROM iam_employee GROUP BY 1`)
	byArea := countMap(`SELECT area_type, count(*)::int FROM iam_service_area WHERE is_active GROUP BY 1`)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"organizations": map[string]any{
			"total":       scalar(`SELECT count(*) FROM iam_organization WHERE is_active`),
			"by_property": byProp, "by_type": byType,
		},
		"employees": map[string]any{
			"total":     scalar(`SELECT count(*) FROM iam_employee`),
			"active":    byStatus["active"],
			"by_status": byStatus,
			"active_without_account": scalar(`SELECT count(*) FROM iam_employee e LEFT JOIN accounts_user u ON u.id=e.user_id
				WHERE e.status='active' AND (e.user_id IS NULL OR NOT u.is_active)`),
		},
		"departments": scalar(`SELECT count(*) FROM iam_department WHERE is_active`),
		"service_areas": map[string]any{
			"total":   scalar(`SELECT count(*) FROM iam_service_area WHERE is_active`),
			"by_type": byArea,
		},
	})
}

// RbacMatrix GET /api/v1/org/rbac/matrix
func (h *Handler) RbacMatrix(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.rbac") {
		return
	}
	ctx := r.Context()
	type permRow struct{ ID, Code, Name, Module string }
	var perms []permRow
	rows, err := h.DB.Query(ctx, `SELECT id::text, code, name, module FROM iam_permission ORDER BY code`)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	for rows.Next() {
		var p permRow
		if rows.Scan(&p.ID, &p.Code, &p.Name, &p.Module) != nil {
			break
		}
		perms = append(perms, p)
	}
	rows.Close()

	moduleMap := map[string][]map[string]any{}
	for _, p := range perms {
		m := p.Module
		if m == "" {
			m = "通用"
		}
		moduleMap[m] = append(moduleMap[m], map[string]any{"id": p.ID, "code": p.Code, "name": p.Name})
	}
	moduleNames := make([]string, 0, len(moduleMap))
	for m := range moduleMap {
		moduleNames = append(moduleNames, m)
	}
	sort.Strings(moduleNames)
	modules := make([]map[string]any, 0, len(moduleNames))
	for _, m := range moduleNames {
		modules = append(modules, map[string]any{"module": m, "permissions": moduleMap[m]})
	}

	roles := []map[string]any{}
	rrows, err := h.DB.Query(ctx, `
		SELECT r.id::text, r.code, r.name, r.data_scope, r.is_active,
		       COALESCE((SELECT json_agg(pp.code ORDER BY rp.id)
		                 FROM iam_role_permissions rp JOIN iam_permission pp ON pp.id=rp.permission_id
		                 WHERE rp.role_id=r.id), '[]'::json)
		FROM iam_role r ORDER BY r.code`)
	if err == nil {
		for rrows.Next() {
			var id, code, name, scope string
			var active bool
			var codes []string
			if rrows.Scan(&id, &code, &name, &scope, &active, &codes) != nil {
				break
			}
			roles = append(roles, map[string]any{
				"id": id, "code": code, "name": name, "data_scope": scope,
				"is_active": active, "permission_codes": codes,
			})
		}
		rrows.Close()
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"modules": modules, "roles": roles, "permission_total": len(perms),
	})
}

// RouteResolve GET /api/v1/org/route-resolve?province=&city=&district=
func (h *Handler) RouteResolve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := h.Svc.UserByID(ctx, auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	q := r.URL.Query()
	dest := q.Get("province") + q.Get("city") + q.Get("district")
	if dest == "" {
		httpx.JSON(w, http.StatusOK, map[string]any{"destination": "", "resolved": []any{}, "excluded": []any{}})
		return
	}
	type areaRow struct {
		OrgID, OrgName, OrgShort, Manager string
		AreaType, RegionName              string
		Priority                          int
	}
	rows, err := h.DB.Query(ctx, `
		SELECT a.organization_id::text, COALESCE(o.name,''), COALESCE(o.short_name,''), COALESCE(o.manager_name,''),
		       a.area_type, a.region_name, a.priority
		FROM iam_service_area a JOIN iam_organization o ON o.id = a.organization_id
		WHERE a.is_active`)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	rank := map[string]int{"deliver": 2, "transfer": 1}
	type scored struct {
		entry map[string]any
		score [2]int
	}
	positives := map[string]scored{}
	excluded := map[string]map[string]any{}
	for rows.Next() {
		var a areaRow
		if rows.Scan(&a.OrgID, &a.OrgName, &a.OrgShort, &a.Manager, &a.AreaType, &a.RegionName, &a.Priority) != nil {
			break
		}
		if a.RegionName == "" || !strings.Contains(dest, a.RegionName) {
			continue
		}
		if a.AreaType == "no_deliver" || a.AreaType == "no_transfer" {
			if _, ok := excluded[a.OrgID]; !ok {
				excluded[a.OrgID] = map[string]any{
					"organization_id": a.OrgID, "organization_name": a.OrgName,
					"reason": areaTypeLabel[a.AreaType] + "·" + a.RegionName,
				}
			}
			continue
		}
		rk, ok := rank[a.AreaType]
		if !ok {
			continue
		}
		score := [2]int{a.Priority, rk}
		cur, exists := positives[a.OrgID]
		if !exists || score[0] > cur.score[0] || (score[0] == cur.score[0] && score[1] > cur.score[1]) {
			positives[a.OrgID] = scored{
				entry: map[string]any{
					"organization_id": a.OrgID, "organization_name": a.OrgName,
					"org_short": a.OrgShort, "manager_name": a.Manager,
					"area_type": a.AreaType, "area_type_label": areaTypeLabel[a.AreaType],
					"region_name": a.RegionName, "priority": a.Priority, "matched_on": a.RegionName,
				},
				score: score,
			}
		}
	}
	rows.Close()

	resolved := []scored{}
	for orgID, s := range positives {
		if _, isExcluded := excluded[orgID]; !isExcluded {
			resolved = append(resolved, s)
		}
	}
	sort.SliceStable(resolved, func(i, j int) bool {
		if resolved[i].score[0] != resolved[j].score[0] {
			return resolved[i].score[0] > resolved[j].score[0]
		}
		return resolved[i].score[1] > resolved[j].score[1]
	})
	out := make([]map[string]any, 0, len(resolved))
	for _, s := range resolved {
		out = append(out, s.entry)
	}
	excludedList := []map[string]any{}
	for _, e := range excluded {
		excludedList = append(excludedList, e)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"destination": dest, "resolved": out, "excluded": excludedList,
	})
}
