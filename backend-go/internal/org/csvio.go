package org

// 组织中台的 CSV 出入口：
//   GET  /org/organizations/export  组织导出
//   GET  /org/employees/export      员工导出（列与导入格式一致，支持往返）
//   POST /org/employees/import      员工导入（按工号 upsert，两遍处理回填直接上级）

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// writeCSV 统一的 CSV 响应：一个 UTF-8 BOM + 表头 + 数据行。
//
// Django 侧把 charset 设成 utf-8-sig 又手写了一次 BOM，于是 HttpResponse 每次
// write 都再补一个——表头前 3 个、每条数据行前 1 个。那是 Excel 里每行首格都带
// 一个不可见字符的缺陷，不是契约，这里只发一个。
func writeCSV(w http.ResponseWriter, filename string, header []string, rows pgx.Rows, cols int) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8-sig")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	_ = cw.Write(header)
	defer rows.Close()
	for rows.Next() {
		rec := make([]string, cols)
		ptrs := make([]any, cols)
		for i := range rec {
			ptrs[i] = &rec[i]
		}
		if rows.Scan(ptrs...) != nil {
			break
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
}

// ExportOrganizations GET /api/v1/org/organizations/export
func (h *Handler) ExportOrganizations(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.view") {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT o.code, o.name, o.short_name,
		       (CASE o.type WHEN 'group' THEN '集团' WHEN 'company' THEN '公司' WHEN 'region' THEN '片区'
		            WHEN 'dept' THEN '部门' WHEN 'station' THEN '网点' ELSE o.type END),
		       (CASE o.org_property WHEN 'self' THEN '自营' WHEN 'franchise' THEN '加盟'
		            WHEN 'outsource' THEN '外包' WHEN 'partner' THEN '合作' WHEN 'jv' THEN '合资'
		            ELSE o.org_property END),
		       COALESCE(p.code,''), o.manager_name, o.manager_phone, o.city,
		       (CASE WHEN o.is_active THEN '是' ELSE '否' END)
		FROM iam_organization o LEFT JOIN iam_organization p ON p.id = o.parent_id
		ORDER BY o.sort_order, o.code LIMIT 5000`)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	writeCSV(w, "organizations.csv",
		[]string{"编码", "名称", "简称", "类型", "经营属性", "上级编码", "负责人", "负责人电话", "城市", "启用"},
		rows, 10)
}

// ExportEmployees GET /api/v1/org/employees/export
func (h *Handler) ExportEmployees(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.view") {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT e.employee_no, e.name, e.phone, COALESCE(o.code,''), e.position,
		       COALESCE(s.employee_no,''),
		       (CASE e.status WHEN 'active' THEN '在职' WHEN 'disabled' THEN '停用'
		            WHEN 'left' THEN '离职' ELSE e.status END)
		FROM iam_employee e
		LEFT JOIN iam_organization o ON o.id = e.organization_id
		LEFT JOIN iam_employee s ON s.id = e.supervisor_id
		ORDER BY e.employee_no LIMIT 5000`)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	writeCSV(w, "employees.csv",
		[]string{"工号", "姓名", "手机", "组织编码", "职位", "直接上级工号", "状态"}, rows, 7)
}

// ImportEmployees POST /api/v1/org/employees/import（multipart，字段名 file）
//
// 两遍处理：先按工号 upsert 全部，再回填直接上级——否则上级还没建出来时那一列会丢。
func (h *Handler) ImportEmployees(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, "org.employee") {
		return
	}
	ctx := r.Context()
	file, _, err := r.FormFile("file")
	if err != nil {
		// Django 直返 Response({"detail": ...}, 400)，走渲染器包成 success:true
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"detail": "缺少文件 file"})
		return
	}
	defer file.Close()

	cr := csv.NewReader(bomReader(file))
	cr.FieldsPerRecord = -1
	orgByCode := map[string]string{}
	if rows, err := h.DB.Query(ctx, `SELECT code, id::text FROM iam_organization`); err == nil {
		for rows.Next() {
			var code, id string
			if rows.Scan(&code, &id) != nil {
				break
			}
			orgByCode[code] = id
		}
		rows.Close()
	}

	created, updated := 0, 0
	errs := []map[string]any{}
	supLinks := [][2]string{}
	idx := 0
	for {
		row, err := cr.Read()
		if err != nil {
			break
		}
		idx++
		if !anyNonBlank(row) {
			continue
		}
		cells := pad(row, 6)
		empNo, name, phone := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1]), strings.TrimSpace(cells[2])
		orgCode, position, supNo := strings.TrimSpace(cells[3]), strings.TrimSpace(cells[4]), strings.TrimSpace(cells[5])
		if idx == 1 && (empNo == "工号" || empNo == "employee_no") {
			continue // 表头可有可无
		}
		if empNo == "" || name == "" {
			errs = append(errs, map[string]any{"row": idx, "error": "工号与姓名必填"})
			continue
		}
		var orgID any
		if orgCode != "" {
			id, ok := orgByCode[orgCode]
			if !ok {
				errs = append(errs, map[string]any{"row": idx, "error": "组织编码不存在：" + orgCode})
				continue
			}
			orgID = id
		}
		// xmax = 0 区分本次是插入还是更新，对齐 update_or_create 的 created/updated 计数
		var isNew bool
		if err := h.DB.QueryRow(ctx, `
			INSERT INTO iam_employee (id, created_at, updated_at, employee_no, name, phone,
			  organization_id, position, email, id_no, status)
			VALUES (gen_random_uuid(), now(), now(), $1, $2, $3, $4::uuid, $5, '', '', 'active')
			ON CONFLICT (employee_no) DO UPDATE SET
			  name = EXCLUDED.name, phone = EXCLUDED.phone,
			  organization_id = EXCLUDED.organization_id, position = EXCLUDED.position,
			  updated_at = now()
			RETURNING (xmax = 0)`, empNo, name, phone, orgID, position).Scan(&isNew); err != nil {
			errs = append(errs, map[string]any{"row": idx, "error": err.Error()})
			continue
		}
		if isNew {
			created++
		} else {
			updated++
		}
		if supNo != "" {
			supLinks = append(supLinks, [2]string{empNo, supNo})
		}
	}
	for _, link := range supLinks {
		_, _ = h.DB.Exec(ctx, `
			UPDATE iam_employee e SET supervisor_id = s.id, updated_at = now()
			FROM iam_employee s
			WHERE e.employee_no = $1 AND s.employee_no = $2 AND e.id <> s.id`, link[0], link[1])
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"created": created, "updated": updated, "errors": errs,
	})
}

// bomReader 剥掉文件开头的 UTF-8 BOM（对齐 Python 的 decode("utf-8-sig")）
func bomReader(r io.Reader) io.Reader {
	br := bufio.NewReader(r)
	if b, err := br.Peek(3); err == nil && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		_, _ = br.Discard(3)
	}
	return br
}

func anyNonBlank(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return true
		}
	}
	return false
}

func pad(row []string, n int) []string {
	out := make([]string, n)
	copy(out, row)
	return out
}
