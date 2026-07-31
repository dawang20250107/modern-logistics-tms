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
	"log/slog"
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

func numStr(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case float64:
		return fmt.Sprintf("%v", v)
	case string:
		return strings.TrimSpace(v)
	}
	return ""
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
	// 管理员重置密码同样要作废该账号既有会话：常见触发原因就是"这人离职了"
	// 或"账号可能被盗"，不踢会话等于只改了个门锁没换掉已配出去的钥匙。
	_ = auth.RevokeAllForUser(ctx, h.DB, *userID)
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
		if _, err := tx.Exec(ctx, `UPDATE iam_employee SET status='left', updated_at=now() WHERE id=$1::uuid`, empID); err != nil {
			slog.Error("移交停用员工失败", "employee_id", empID, "err", err)
		}
		if userID != nil {
			// 只有登录账号真的停掉了才报 disabled=true：这条要回给前端，
			// 说"已停用"而账号还能登录，比不停用更糟
			if _, err := tx.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1::uuid`, *userID); err != nil {
				slog.Error("移交停用登录账号失败", "user_id", *userID, "err", err)
			} else {
				disabled = true
			}
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

// ── 通用 CRUD 引擎的写后钩子 ──

// rebuildOrgPath 组织落库/改父后重建物化路径。
// path 是数据范围（org_sub 子树可见性）的唯一依据，父子关系一变就必须跟着重算，
// 否则会出现「调整了组织归属，但权限范围还停在老位置」的越权/漏看。
func rebuildOrgPath(ctx context.Context, h *masterdata.Handler, id string, _ map[string]any, _ bool) error {
	// 先修本节点，再级联修其整棵子树（子树成员的 path 以本节点 path 为前缀）
	if _, err := h.DB.Exec(ctx, `
		UPDATE iam_organization o SET path = COALESCE(
		    (SELECT COALESCE(NULLIF(p.path,''), p.id::text) || '/' || o.id::text
		     FROM iam_organization p WHERE p.id = o.parent_id),
		    o.id::text), updated_at = now()
		WHERE o.id = $1::uuid`, id); err != nil {
		return err
	}
	// 子树逐层重建：层数有限，用递归 CTE 一次算出所有后代的新 path
	_, err := h.DB.Exec(ctx, `
		WITH RECURSIVE tree AS (
		  -- 锚定项必须显式转 text：varchar(512) 与递归项拼出的 varchar 类型不一致，PG 会拒绝
		  SELECT id, path::text AS path FROM iam_organization WHERE id = $1::uuid
		  UNION ALL
		  SELECT c.id, t.path || '/' || c.id::text
		  FROM iam_organization c JOIN tree t ON c.parent_id = t.id
		)
		UPDATE iam_organization o SET path = tree.path, updated_at = now()
		FROM tree WHERE o.id = tree.id AND o.id <> $1::uuid`, id)
	return err
}

// detachOrgDependents 删组织前把「部门」这条二级依赖链拆干净。
// 部门本身会被 CascadeTables 删掉，但部门上还挂着员工归属与子部门自引用，
// 不先断开就会撞外键；Django 的收集器是递归的，声明式的一层覆盖不到这里。
func detachOrgDependents(ctx context.Context, h *masterdata.Handler, id string) error {
	const dept = `SELECT id FROM iam_department WHERE organization_id = $1::uuid`
	if _, err := h.DB.Exec(ctx,
		`UPDATE iam_employee SET department_id = NULL WHERE department_id IN (`+dept+`)`, id); err != nil {
		return err
	}
	if _, err := h.DB.Exec(ctx,
		`UPDATE iam_department SET parent_id = NULL WHERE parent_id IN (`+dept+`)`, id); err != nil {
		return err
	}
	// 子组织提级到顶：断父之后 path 必须立刻重算，否则前缀里还留着已删组织的 id，
	// 数据范围就会按一段不存在的祖先来判可见性。
	rows, err := h.DB.Query(ctx, `SELECT id::text FROM iam_organization WHERE parent_id = $1::uuid`, id)
	if err != nil {
		return err
	}
	kids := []string{}
	for rows.Next() {
		var k string
		if rows.Scan(&k) != nil {
			break
		}
		kids = append(kids, k)
	}
	rows.Close()
	for _, k := range kids {
		if _, err := h.DB.Exec(ctx,
			`UPDATE iam_organization SET parent_id = NULL WHERE id = $1::uuid`, k); err != nil {
			return err
		}
		if err := rebuildOrgPath(ctx, h, k, nil, false); err != nil {
			return err
		}
	}
	return nil
}

// dropEmployeeHandovers 删员工前清掉移交台账（两端外键都指向员工，都是 CASCADE）
func dropEmployeeHandovers(ctx context.Context, h *masterdata.Handler, id string) error {
	_, err := h.DB.Exec(ctx,
		`DELETE FROM iam_account_handover WHERE from_employee_id = $1::uuid OR to_employee_id = $1::uuid`, id)
	return err
}

// setRolePermissionsFromBody 请求体里带 permissions 时覆盖式写角色权限点
func setRolePermissionsFromBody(ctx context.Context, h *masterdata.Handler, id string, body map[string]any, _ bool) error {
	raw, has := body["permissions"]
	if !has {
		return nil
	}
	return replaceM2M(ctx, h, "iam_role_permissions", "role_id", "permission_id", id, raw)
}

// setEmployeeGroupsFromBody 请求体里带 groups 时覆盖式写员工分组
func setEmployeeGroupsFromBody(ctx context.Context, h *masterdata.Handler, id string, body map[string]any, _ bool) error {
	raw, has := body["groups"]
	if !has {
		return nil
	}
	return replaceM2M(ctx, h, "iam_employee_groups", "employee_id", "employeegroup_id", id, raw)
}

// replaceM2M 覆盖式重写一张 M2M 中间表（非法 id 跳过，不因一条脏数据整体失败）
func replaceM2M(ctx context.Context, h *masterdata.Handler, table, ownCol, refCol, ownID string, raw any) error {
	list, _ := raw.([]any)
	if _, err := h.DB.Exec(ctx, "DELETE FROM "+table+" WHERE "+ownCol+"=$1::uuid", ownID); err != nil {
		return err
	}
	for _, it := range list {
		v, _ := it.(string)
		if _, err := uuid.Parse(v); err != nil {
			continue
		}
		if _, err := h.DB.Exec(ctx,
			"INSERT INTO "+table+" ("+ownCol+", "+refCol+") VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING",
			ownID, v); err != nil {
			return err
		}
	}
	return nil
}

// UnlockLogin POST /org/login-audit/unlock —— 解除某用户名的登录失败锁定。
// 权限口径与登录审计读取一致的更严一档（org.rbac）：能解锁等于能绕过防爆破闸。
func (h *Handler) UnlockLogin(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.rbac") {
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	username := strings.TrimSpace(body.Username)
	if username == "" {
		httpx.Err(w, http.StatusBadRequest, "error", "缺少 username")
		return
	}
	auth.ClearFailures(username)
	httpx.JSON(w, http.StatusOK, map[string]any{"username": username, "unlocked": true})
}
