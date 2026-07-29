package org

// 组织中台写路径：组织/员工/服务区创建、员工启停/重置密码/角色/移交、角色权限覆盖。
// 对齐 DRF ModelViewSet create 与各 @action 的请求/响应契约（含直返 400 的
// {"detail": ...} 经 EnvelopeJSONRenderer 包裹为 success:true 的历史怪癖）。

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
)

// detail400 复刻 DRF 直返 Response({"detail": …}, 4xx) 被信封渲染器包裹后的形态
func detail400(w http.ResponseWriter, code int, msg string) {
	httpx.JSON(w, code, map[string]any{"detail": msg})
}

func strField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func uuidField(m map[string]any, key string) *string {
	if v, ok := m[key].(string); ok && v != "" {
		if _, err := uuid.Parse(v); err == nil {
			return &v
		}
	}
	return nil
}

func intField(m map[string]any, key string, def int) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return def
}

func boolField(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

// validationErr 复刻 DRF 校验失败：{"code":"invalid","message":"请求参数校验失败","details":{field:[msg]}}
func validationErr(w http.ResponseWriter, details map[string]any) {
	httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
}

// CreateOrganization POST /api/v1/org/organizations
func (h *Handler) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.manage") {
		return
	}
	ctx := r.Context()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	name, code := strField(body, "name"), strField(body, "code")
	details := map[string]any{}
	if name == "" {
		details["name"] = []string{"该字段是必填项。"}
	}
	if code == "" {
		details["code"] = []string{"该字段是必填项。"}
	} else {
		var exists bool
		_ = h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM iam_organization WHERE code=$1)`, code).Scan(&exists)
		if exists {
			details["code"] = []string{"具有 编码 的 组织 已存在。"}
		}
	}
	typ := strField(body, "type")
	if typ == "" {
		typ = "dept"
	} else if orgTypeLabel[typ] == "" {
		details["type"] = []string{fmt.Sprintf("“%s” 不是合法选项。", typ)}
	}
	prop := strField(body, "org_property")
	if prop == "" {
		prop = "self"
	} else if orgPropertyLabel[prop] == "" {
		details["org_property"] = []string{fmt.Sprintf("“%s” 不是合法选项。", prop)}
	}
	parent := uuidField(body, "parent")
	var parentPath string
	if parent != nil {
		if err := h.DB.QueryRow(ctx, `SELECT COALESCE(NULLIF(path,''), id::text) FROM iam_organization WHERE id=$1::uuid`, *parent).Scan(&parentPath); err != nil {
			details["parent"] = []string{"上级组织不存在。"}
		}
	}
	if len(details) > 0 {
		validationErr(w, details)
		return
	}
	id, _ := uuid.NewV7()
	path := id.String()
	if parent != nil {
		path = parentPath + "/" + id.String()
	}
	_, err := h.DB.Exec(ctx, `
		INSERT INTO iam_organization (id, created_at, updated_at, name, short_name, code, type, org_property,
		  parent_id, path, province, city, district, address, lng, lat,
		  manager_name, manager_phone, business_phone, service_phone, complaint_phone,
		  receipt_return_address, sort_order, is_active)
		VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7::uuid, $8, $9, $10, $11, $12,
		  NULLIF($13,'')::numeric, NULLIF($14,'')::numeric, $15, $16, $17, $18, $19, $20, $21, $22)`,
		id.String(), name, strField(body, "short_name"), code, typ, prop,
		parent, path, strField(body, "province"), strField(body, "city"), strField(body, "district"),
		strField(body, "address"), numStr(body, "lng"), numStr(body, "lat"),
		strField(body, "manager_name"), strField(body, "manager_phone"), strField(body, "business_phone"),
		strField(body, "service_phone"), strField(body, "complaint_phone"),
		strField(body, "receipt_return_address"), intField(body, "sort_order", 0), boolField(body, "is_active", true))
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
		return
	}
	h.respondRow(w, r.Context(), organizationsCfg, "o.id = $1::uuid", id.String(), http.StatusCreated)
}

func numStr(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case float64:
		return fmt.Sprintf("%v", v)
	case string:
		return strings.TrimSpace(v)
	}
	return ""
}

// CreateEmployee POST /api/v1/org/employees
func (h *Handler) CreateEmployee(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.employee") {
		return
	}
	ctx := r.Context()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	empNo, name := strField(body, "employee_no"), strField(body, "name")
	details := map[string]any{}
	if empNo == "" {
		details["employee_no"] = []string{"该字段是必填项。"}
	} else {
		var exists bool
		_ = h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM iam_employee WHERE employee_no=$1)`, empNo).Scan(&exists)
		if exists {
			details["employee_no"] = []string{"具有 工号 的 员工 已存在。"}
		}
	}
	if name == "" {
		details["name"] = []string{"该字段是必填项。"}
	}
	status := strField(body, "status")
	if status == "" {
		status = "active"
	} else if status != "active" && status != "disabled" && status != "left" {
		details["status"] = []string{fmt.Sprintf("“%s” 不是合法选项。", status)}
	}
	if len(details) > 0 {
		validationErr(w, details)
		return
	}
	id, _ := uuid.NewV7()
	_, err := h.DB.Exec(ctx, `
		INSERT INTO iam_employee (id, created_at, updated_at, employee_no, name, phone, email, id_no,
		  organization_id, department_id, supervisor_id, position, status,
		  hire_date, leave_date, user_id)
		VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7::uuid, $8::uuid, $9::uuid, $10, $11,
		  NULLIF($12,'')::date, NULLIF($13,'')::date, $14::uuid)`,
		id.String(), empNo, name, strField(body, "phone"), strField(body, "email"), strField(body, "id_no"),
		uuidField(body, "organization"), uuidField(body, "department"), uuidField(body, "supervisor"),
		strField(body, "position"), status,
		strField(body, "hire_date"), strField(body, "leave_date"), uuidField(body, "user"))
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
		return
	}
	if groups, ok := body["groups"].([]any); ok {
		for _, g := range groups {
			if gid, ok := g.(string); ok {
				_, _ = h.DB.Exec(ctx, `INSERT INTO iam_employee_groups (employee_id, employeegroup_id)
					SELECT $1::uuid, $2::uuid WHERE EXISTS(SELECT 1 FROM iam_employee_group WHERE id=$2::uuid)
					ON CONFLICT DO NOTHING`, id.String(), gid)
			}
		}
	}
	h.respondRow(w, ctx, employeesCfg, "e.id = $1::uuid", id.String(), http.StatusCreated)
}

// CreateServiceArea POST /api/v1/org/service-areas
func (h *Handler) CreateServiceArea(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.manage") {
		return
	}
	ctx := r.Context()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	details := map[string]any{}
	orgID := uuidField(body, "organization")
	if orgID == nil {
		details["organization"] = []string{"该字段是必填项。"}
	}
	regionName := strField(body, "region_name")
	if regionName == "" {
		details["region_name"] = []string{"该字段是必填项。"}
	}
	areaType := strField(body, "area_type")
	if areaType == "" {
		areaType = "deliver"
	} else if areaTypeLabel[areaType] == "" {
		details["area_type"] = []string{fmt.Sprintf("“%s” 不是合法选项。", areaType)}
	}
	if len(details) > 0 {
		validationErr(w, details)
		return
	}
	id, _ := uuid.NewV7()
	_, err := h.DB.Exec(ctx, `
		INSERT INTO iam_service_area (id, created_at, updated_at, organization_id, area_type,
		  province, city, district, region_code, region_name, priority, note, is_active)
		VALUES ($1, now(), now(), $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id.String(), *orgID, areaType,
		strField(body, "province"), strField(body, "city"), strField(body, "district"),
		strField(body, "region_code"), regionName, intField(body, "priority", 0),
		strField(body, "note"), boolField(body, "is_active", true))
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
		return
	}
	h.respondRow(w, ctx, serviceAreasCfg, "a.id = $1::uuid", id.String(), http.StatusCreated)
}

// SetRolePermissions POST /api/v1/org/roles/{id}/set-permissions {permissions:[ids]}
func (h *Handler) SetRolePermissions(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.rbac") {
		return
	}
	ctx := r.Context()
	roleID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(roleID); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Role matches the given query.")
		return
	}
	var exists bool
	_ = h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM iam_role WHERE id=$1::uuid)`, roleID).Scan(&exists)
	if !exists {
		httpx.Err(w, http.StatusNotFound, "error", "No Role matches the given query.")
		return
	}
	var body struct {
		Permissions []string `json:"permissions"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ids := make([]string, 0, len(body.Permissions))
	for _, p := range body.Permissions {
		if _, err := uuid.Parse(p); err == nil {
			ids = append(ids, p)
		}
	}
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM iam_role_permissions WHERE role_id=$1::uuid`, roleID); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO iam_role_permissions (role_id, permission_id)
		SELECT $1::uuid, p.id FROM iam_permission p WHERE p.id::text = ANY($2)`, roleID, ids); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	h.respondRow(w, ctx, rolesCfg, "r.id = $1::uuid", roleID, http.StatusOK)
}

// employeeByID 取员工 + 绑定账号（不存在时返回 nil 并已写 404）
func (h *Handler) employeeByID(w http.ResponseWriter, ctx context.Context, id string) (empID string, userID *string, empNo string, ok bool) {
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Employee matches the given query.")
		return "", nil, "", false
	}
	err := h.DB.QueryRow(ctx, `SELECT id::text, user_id::text, employee_no FROM iam_employee WHERE id=$1::uuid`, id).
		Scan(&empID, &userID, &empNo)
	if err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Employee matches the given query.")
		return "", nil, "", false
	}
	return empID, userID, empNo, true
}

// ToggleEmployee POST /api/v1/org/employees/{id}/enable|disable
func (h *Handler) ToggleEmployee(active bool) http.HandlerFunc {
	status := "disabled"
	if active {
		status = "active"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.requirePerm(w, r, "org.employee") {
			return
		}
		ctx := r.Context()
		empID, userID, _, ok := h.employeeByID(w, ctx, chi.URLParam(r, "id"))
		if !ok {
			return
		}
		if userID != nil {
			_, _ = h.DB.Exec(ctx, `UPDATE accounts_user SET is_active=$2 WHERE id=$1::uuid`, *userID, active)
		}
		_, _ = h.DB.Exec(ctx, `UPDATE iam_employee SET status=$2, updated_at=now() WHERE id=$1::uuid`, empID, status)
		h.respondRow(w, ctx, employeesCfg, "e.id = $1::uuid", empID, http.StatusOK)
	}
}

// ResetPassword POST /api/v1/org/employees/{id}/reset-password
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.employee") {
		return
	}
	ctx := r.Context()
	empID, userID, empNo, ok := h.employeeByID(w, ctx, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	_ = empID
	if userID == nil {
		detail400(w, http.StatusBadRequest, "该员工尚未绑定登录账号")
		return
	}
	// secrets.token_urlsafe(9) 等价：9 字节随机 → base64url 无填充（12 字符）
	raw := make([]byte, 9)
	_, _ = rand.Read(raw)
	newPwd := base64.RawURLEncoding.EncodeToString(raw)
	hash := auth.MakeDjangoPassword(newPwd)
	var username string
	_ = h.DB.QueryRow(ctx, `UPDATE accounts_user SET password=$2 WHERE id=$1::uuid RETURNING username`, *userID, hash).Scan(&username)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"employee_no": empNo, "username": username, "password": newPwd,
	})
}

// EmployeeRoles GET/POST /api/v1/org/employees/{id}/roles
func (h *Handler) EmployeeRoles(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.rbac") {
		return
	}
	ctx := r.Context()
	empID, userID, _, ok := h.employeeByID(w, ctx, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if userID == nil {
		detail400(w, http.StatusBadRequest, "该员工尚未绑定登录账号")
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Roles []string `json:"roles"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ids := make([]string, 0, len(body.Roles))
		for _, v := range body.Roles {
			if _, err := uuid.Parse(v); err == nil {
				ids = append(ids, v)
			}
		}
		var orgID *string
		_ = h.DB.QueryRow(ctx, `SELECT organization_id::text FROM iam_employee WHERE id=$1::uuid`, empID).Scan(&orgID)
		tx, err := h.DB.Begin(ctx)
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `DELETE FROM iam_role_assignment WHERE user_id=$1::uuid`, *userID); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
			return
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO iam_role_assignment (id, created_at, updated_at, user_id, role_id, organization_id)
			SELECT gen_random_uuid(), now(), now(), $1::uuid, ro.id, $3::uuid
			FROM iam_role ro WHERE ro.id::text = ANY($2)`, *userID, ids, orgID); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
			return
		}
		// 授权即时生效：清用户权限缓存由 Django 侧 TTL 兜底（PORTING.md 差异清单）
	}
	var out json.RawMessage
	_ = h.DB.QueryRow(ctx, `
		SELECT COALESCE(json_agg(json_build_object(
		    'id', ra.id::text, 'user', ra.user_id::text, 'username', COALESCE(u.username,''),
		    'role', ra.role_id::text, 'role_code', ro.code, 'role_name', ro.name,
		    'organization', ra.organization_id::text, 'organization_name', COALESCE(og.name,'')
		  )), '[]'::json)
		FROM iam_role_assignment ra
		JOIN iam_role ro ON ro.id = ra.role_id
		LEFT JOIN accounts_user u ON u.id = ra.user_id
		LEFT JOIN iam_organization og ON og.id = ra.organization_id
		WHERE ra.user_id = $1::uuid`, *userID).Scan(&out)
	httpx.JSON(w, http.StatusOK, out)
}

// Handover POST /api/v1/org/employees/{id}/handover {to_employee, reason, disable}
func (h *Handler) Handover(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.employee") {
		return
	}
	ctx := r.Context()
	me, _ := h.Svc.UserByID(ctx, auth.UserID(r))
	empID, userID, _, ok := h.employeeByID(w, ctx, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	toID := strField(body, "to_employee")
	if toID == "" {
		detail400(w, http.StatusBadRequest, "缺少 to_employee")
		return
	}
	var toExists bool
	if _, err := uuid.Parse(toID); err == nil {
		_ = h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM iam_employee WHERE id=$1::uuid)`, toID).Scan(&toExists)
	}
	if !toExists {
		detail400(w, http.StatusNotFound, "接收人不存在")
		return
	}
	if toID == empID {
		detail400(w, http.StatusBadRequest, "移交人与接收人不能相同")
		return
	}
	disable := boolField(body, "disable", true)
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var movedReports, movedDepts int64
	if ct, err := tx.Exec(ctx, `UPDATE iam_employee SET supervisor_id=$2::uuid, updated_at=now()
		WHERE supervisor_id=$1::uuid`, empID, toID); err == nil {
		movedReports = ct.RowsAffected()
	}
	if ct, err := tx.Exec(ctx, `UPDATE iam_department SET manager_id=$2::uuid, updated_at=now()
		WHERE manager_id=$1::uuid`, empID, toID); err == nil {
		movedDepts = ct.RowsAffected()
	}
	disabled := false
	if disable {
		_, _ = tx.Exec(ctx, `UPDATE iam_employee SET status='left', updated_at=now() WHERE id=$1::uuid`, empID)
		if userID != nil {
			_, _ = tx.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1::uuid`, *userID)
			disabled = true
		}
	}
	hid, _ := uuid.NewV7()
	var operatorID *string
	if me != nil {
		operatorID = &me.ID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO iam_account_handover (id, created_at, updated_at, from_employee_id, to_employee_id,
		  operator_id, reason, moved_reports, moved_departments, disabled_account)
		VALUES ($1, now(), now(), $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8)`,
		hid.String(), empID, toID, operatorID, strField(body, "reason"), movedReports, movedDepts, disabled); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	h.respondRow(w, ctx, handoversCfg, "h.id = $1::uuid", hid.String(), http.StatusCreated)
}

// respondRow 用列表 cfg 回读单行序列化（Go 写→与列表同一列面回显）
func (h *Handler) respondRow(w http.ResponseWriter, ctx context.Context, cfg masterdata.ResourceCfg, where, id string, code int) {
	it, err := h.MD.One(ctx, cfg, where, id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, code, it)
}
