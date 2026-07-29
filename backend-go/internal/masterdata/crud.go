package masterdata

// 通用 CRUD 引擎：在 ResourceCfg（列表列面）之上补齐 DRF ModelViewSet 的
// retrieve / create / update / partial_update / destroy，使绝大多数标准资源
// 只需一份配置即可全量原生化，无需逐资源手写 handler。
//
// 契约对齐要点：
//   - 创建 201、更新 200、删除 204（DRF 默认）
//   - 未命中 404 + {"code":"error","message":"No <Model> matches the given query."}
//   - 字段校验失败 400 + {"code":"invalid","message":"请求参数校验失败","details":{field:[msg]}}
//   - 写入后一律用列表列面回读，保证读写序列化完全一致

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// FieldKind 决定入参归一化方式（与 Django 字段类型对应）
type FieldKind int

const (
	FText     FieldKind = iota // CharField/TextField
	FEnum                      // CharField(choices=…)
	FInt                       // IntegerField
	FDecimal                   // DecimalField
	FBool                      // BooleanField
	FDate                      // DateField
	FDateTime                  // DateTimeField
	FUUID                      // ForeignKey
	FJSON                      // JSONField
)

// Field 一个可写字段的声明
type Field struct {
	Kind     FieldKind
	Column   string   // 库列名（默认与键同名）
	Required bool     // 创建时必填
	Choices  []string // FEnum 的合法取值
	Default  any      // 创建时缺省值（nil 表示交给库默认）
	Ref      string   // FUUID 的外键表名（校验存在性）
	Unique   bool     // 唯一约束（重复时给 DRF 同款文案）
	Label    string   // 唯一性报错文案里的字段中文名
}

// WriteCfg 资源的写侧配置
type WriteCfg struct {
	Table      string           // 物理表
	Model      string           // 404 文案里的模型名
	Fields     map[string]Field // 可写字段（JSON 键 → 声明）
	SoftDelete bool             // true 走 is_deleted 软删（对齐 SoftDeleteModel）
	Alias      string           // 列表 cfg 里该表的别名（回读 where 用）
	// PKColumn 主键列，默认 id
	PKColumn string
	// AfterWrite 可选钩子：落库后处理 M2M 等
	AfterWrite func(ctx context.Context, h *Handler, id string, body map[string]any, creating bool) error
}

func (c WriteCfg) pk() string {
	if c.PKColumn != "" {
		return c.PKColumn
	}
	return "id"
}

func (c WriteCfg) notFound() string {
	m := c.Model
	if m == "" {
		m = "object"
	}
	return "No " + m + " matches the given query."
}

func (c WriteCfg) alias() string {
	if c.Alias != "" {
		return c.Alias
	}
	return ""
}

// whereByID 生成回读/更新用的主键条件（带表别名）
func (c WriteCfg) whereByID() string {
	a := c.alias()
	if a != "" {
		a += "."
	}
	return a + c.pk() + " = $1::uuid"
}

// normalize 把 JSON 入参按字段声明归一化为可直接入库的值；返回校验错误
func normalize(key string, f Field, raw any) (any, string) {
	if raw == nil {
		return nil, ""
	}
	switch f.Kind {
	case FText:
		s, ok := raw.(string)
		if !ok {
			return nil, "该字段必须是字符串。"
		}
		return strings.TrimSpace(s), ""
	case FEnum:
		s, ok := raw.(string)
		if !ok {
			return nil, "该字段必须是字符串。"
		}
		s = strings.TrimSpace(s)
		if s != "" && len(f.Choices) > 0 {
			for _, c := range f.Choices {
				if c == s {
					return s, ""
				}
			}
			return nil, fmt.Sprintf("“%s” 不是合法选项。", s)
		}
		return s, ""
	case FInt:
		switch v := raw.(type) {
		case float64:
			return int64(v), ""
		case string:
			if v == "" {
				return nil, ""
			}
			var n int64
			if _, err := fmt.Sscan(v, &n); err != nil {
				return nil, "请输入合法整数。"
			}
			return n, ""
		}
		return nil, "请输入合法整数。"
	case FDecimal:
		switch v := raw.(type) {
		case float64:
			return fmt.Sprintf("%v", v), ""
		case string:
			if strings.TrimSpace(v) == "" {
				return nil, ""
			}
			return strings.TrimSpace(v), ""
		}
		return nil, "请输入合法数字。"
	case FBool:
		b, ok := raw.(bool)
		if !ok {
			return nil, "该字段必须是布尔值。"
		}
		return b, ""
	case FDate, FDateTime:
		s, ok := raw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, ""
		}
		return strings.TrimSpace(s), ""
	case FUUID:
		s, ok := raw.(string)
		if !ok || s == "" {
			return nil, ""
		}
		if _, err := uuid.Parse(s); err != nil {
			return nil, fmt.Sprintf("“%s” 不是合法 UUID。", s)
		}
		return s, ""
	case FJSON:
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, "该字段必须是合法 JSON。"
		}
		return string(b), ""
	}
	return raw, ""
}

func (f Field) column(key string) string {
	if f.Column != "" {
		return f.Column
	}
	if f.Kind == FUUID {
		return key + "_id"
	}
	return key
}

// castFor 给占位符补类型转换（pgx 对 text→uuid/date 等需要显式 cast）
func castFor(f Field) string {
	switch f.Kind {
	case FUUID:
		return "::uuid"
	case FDate:
		return "::date"
	case FDateTime:
		return "::timestamptz"
	case FDecimal:
		return "::numeric"
	case FJSON:
		return "::jsonb"
	}
	return ""
}

// requiredZeros 表级缓存：NOT NULL 且无库默认值的列 → 该列类型的零值。
// Django 的 CharField(blank=True) 在库里是 NOT NULL，空值由 Python 侧补 ”；
// 原生 INSERT 不走模型层，必须自己补齐，否则漏字段即违反非空约束。
var (
	zerosMu    sync.RWMutex
	zerosCache = map[string]map[string]any{}
)

func (h *Handler) requiredZeros(ctx context.Context, table string) map[string]any {
	zerosMu.RLock()
	if z, ok := zerosCache[table]; ok {
		zerosMu.RUnlock()
		return z
	}
	zerosMu.RUnlock()

	z := map[string]any{}
	rows, err := h.DB.Query(ctx, `
		SELECT column_name, data_type FROM information_schema.columns
		WHERE table_name=$1 AND is_nullable='NO' AND column_default IS NULL`, table)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var col, typ string
			if rows.Scan(&col, &typ) != nil {
				break
			}
			switch {
			case col == "id" || col == "created_at" || col == "updated_at":
				// 由引擎显式赋值
			case strings.Contains(typ, "char") || typ == "text":
				z[col] = ""
			case typ == "boolean":
				z[col] = false
			case strings.Contains(typ, "int"):
				z[col] = 0
			case typ == "numeric" || strings.Contains(typ, "double"):
				z[col] = "0"
			case typ == "jsonb" || typ == "json":
				z[col] = "{}"
			}
		}
	}
	zerosMu.Lock()
	zerosCache[table] = z
	zerosMu.Unlock()
	return z
}

// Retrieve GET /<res>/{id}
func (h *Handler) Retrieve(w http.ResponseWriter, r *http.Request, cfg ResourceCfg, wc WriteCfg) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", wc.notFound())
		return
	}
	it, err := h.OneDetail(r.Context(), cfg, wc.whereByID(), id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusNotFound, "error", wc.notFound())
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// Create POST /<res>
func (h *Handler) Create(w http.ResponseWriter, r *http.Request, cfg ResourceCfg, wc WriteCfg) {
	ctx := r.Context()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{"请求体不是合法 JSON。"}})
		return
	}
	cols, vals, details := h.collect(ctx, wc, body, true)
	if len(details) > 0 {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
		return
	}
	id, _ := uuid.NewV7()
	cols = append([]string{wc.pk(), "created_at", "updated_at"}, cols...)
	phs := []string{"$1", "now()", "now()"}
	args := []any{id.String()}
	for i, v := range vals {
		args = append(args, v)
		phs = append(phs, fmt.Sprintf("$%d%s", len(args), castFor(fieldOf(wc, cols[i+3]))))
	}
	// 补齐未提供的 NOT NULL 列（Django 由模型层给 ''/0/false，原生写入需自己补）
	provided := map[string]bool{}
	for _, c := range cols {
		provided[c] = true
	}
	for col, zero := range h.requiredZeros(ctx, wc.Table) {
		if provided[col] {
			continue
		}
		provided[col] = true // 记账，避免与下面的软删列重复写入
		cols = append(cols, col)
		args = append(args, zero)
		phs = append(phs, fmt.Sprintf("$%d", len(args)))
	}
	if wc.SoftDelete && !provided["is_deleted"] {
		cols = append(cols, "is_deleted")
		args = append(args, false)
		phs = append(phs, fmt.Sprintf("$%d", len(args)))
	}
	sql := "INSERT INTO " + wc.Table + " (" + strings.Join(cols, ",") + ") VALUES (" + strings.Join(phs, ",") + ")"
	if _, err := h.DB.Exec(ctx, sql, args...); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败："+err.Error())
		return
	}
	if wc.AfterWrite != nil {
		_ = wc.AfterWrite(ctx, h, id.String(), body, true)
	}
	it, err := h.OneDetail(ctx, cfg, wc.whereByID(), id.String())
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, it)
}

// Update PUT/PATCH /<res>/{id}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request, cfg ResourceCfg, wc WriteCfg) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", wc.notFound())
		return
	}
	var exists bool
	_ = h.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+wc.Table+" WHERE "+wc.pk()+"=$1::uuid)", id).Scan(&exists)
	if !exists {
		httpx.Err(w, http.StatusNotFound, "error", wc.notFound())
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{"请求体不是合法 JSON。"}})
		return
	}
	// PUT 全量、PATCH 局部：二者都只写传入字段（DRF 的 required 校验仅 PUT 生效）
	cols, vals, details := h.collectUpdate(ctx, wc, body, id, r.Method == http.MethodPut)
	if len(details) > 0 {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
		return
	}
	if len(cols) > 0 {
		sets := make([]string, 0, len(cols))
		args := []any{id}
		for i, c := range cols {
			args = append(args, vals[i])
			sets = append(sets, fmt.Sprintf("%s = $%d%s", c, len(args), castFor(fieldOf(wc, c))))
		}
		sql := "UPDATE " + wc.Table + " SET " + strings.Join(sets, ", ") + ", updated_at=now() WHERE " + wc.pk() + "=$1::uuid"
		if _, err := h.DB.Exec(ctx, sql, args...); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败："+err.Error())
			return
		}
	}
	if wc.AfterWrite != nil {
		_ = wc.AfterWrite(ctx, h, id, body, false)
	}
	it, err := h.OneDetail(ctx, cfg, wc.whereByID(), id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// Delete DELETE /<res>/{id} —— 软删模型置 is_deleted，否则物理删；均返回 204
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request, wc WriteCfg) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", wc.notFound())
		return
	}
	var sql string
	if wc.SoftDelete {
		sql = "UPDATE " + wc.Table + " SET is_deleted=true, deleted_at=now(), updated_at=now() WHERE " + wc.pk() + "=$1::uuid AND NOT is_deleted"
	} else {
		sql = "DELETE FROM " + wc.Table + " WHERE " + wc.pk() + "=$1::uuid"
	}
	ct, err := h.DB.Exec(ctx, sql, id)
	if err != nil || ct.RowsAffected() == 0 {
		httpx.Err(w, http.StatusNotFound, "error", wc.notFound())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func fieldOf(wc WriteCfg, col string) Field {
	for k, f := range wc.Fields {
		if f.column(k) == col {
			return f
		}
	}
	return Field{Kind: FText}
}

// collect 创建时收集列与值（含必填与唯一性校验）
func (h *Handler) collect(ctx context.Context, wc WriteCfg, body map[string]any, creating bool) ([]string, []any, map[string]any) {
	cols, vals := []string{}, []any{}
	details := map[string]any{}
	for key, f := range wc.Fields {
		raw, has := body[key]
		if !has || raw == nil || raw == "" {
			if creating && f.Required {
				details[key] = []string{"该字段是必填项。"}
				continue
			}
			if creating && f.Default != nil {
				cols = append(cols, f.column(key))
				vals = append(vals, f.Default)
			}
			continue
		}
		v, msg := normalize(key, f, raw)
		if msg != "" {
			details[key] = []string{msg}
			continue
		}
		if f.Unique {
			var dup bool
			_ = h.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+wc.Table+" WHERE "+f.column(key)+"=$1)", v).Scan(&dup)
			if dup {
				label := f.Label
				if label == "" {
					label = key
				}
				details[key] = []string{fmt.Sprintf("具有 %s 的 %s 已存在。", label, wc.Model)}
				continue
			}
		}
		if f.Kind == FUUID && f.Ref != "" {
			var ok bool
			_ = h.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+f.Ref+" WHERE id=$1::uuid)", v).Scan(&ok)
			if !ok {
				details[key] = []string{"关联对象不存在。"}
				continue
			}
		}
		cols = append(cols, f.column(key))
		vals = append(vals, v)
	}
	// 创建时补上未传但有默认值的字段
	if creating {
		for key, f := range wc.Fields {
			if _, has := body[key]; has {
				continue
			}
			if f.Default == nil {
				continue
			}
			already := false
			for _, c := range cols {
				if c == f.column(key) {
					already = true
					break
				}
			}
			if !already {
				cols = append(cols, f.column(key))
				vals = append(vals, f.Default)
			}
		}
	}
	return cols, vals, details
}

// collectUpdate 更新时收集列与值（唯一性排除自身）
func (h *Handler) collectUpdate(ctx context.Context, wc WriteCfg, body map[string]any, id string, full bool) ([]string, []any, map[string]any) {
	cols, vals := []string{}, []any{}
	details := map[string]any{}
	for key, f := range wc.Fields {
		raw, has := body[key]
		if !has {
			if full && f.Required {
				details[key] = []string{"该字段是必填项。"}
			}
			continue
		}
		if raw == nil {
			cols = append(cols, f.column(key))
			vals = append(vals, nil)
			continue
		}
		v, msg := normalize(key, f, raw)
		if msg != "" {
			details[key] = []string{msg}
			continue
		}
		if f.Unique {
			var dup bool
			_ = h.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+wc.Table+" WHERE "+f.column(key)+"=$1 AND "+wc.pk()+" <> $2::uuid)", v, id).Scan(&dup)
			if dup {
				label := f.Label
				if label == "" {
					label = key
				}
				details[key] = []string{fmt.Sprintf("具有 %s 的 %s 已存在。", label, wc.Model)}
				continue
			}
		}
		if f.Kind == FUUID && f.Ref != "" && v != nil {
			var ok bool
			_ = h.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+f.Ref+" WHERE id=$1::uuid)", v).Scan(&ok)
			if !ok {
				details[key] = []string{"关联对象不存在。"}
				continue
			}
		}
		cols = append(cols, f.column(key))
		vals = append(vals, v)
	}
	return cols, vals, details
}

// CRUD 把一份读写配置绑成 chi 子路由（列表/创建/详情/更新/删除）
func (h *Handler) CRUD(cfg ResourceCfg, wc WriteCfg) func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, rq *http.Request) { h.List(w, rq, cfg) })
		r.Post("/", func(w http.ResponseWriter, rq *http.Request) { h.Create(w, rq, cfg, wc) })
		r.Get("/{id}", func(w http.ResponseWriter, rq *http.Request) { h.Retrieve(w, rq, cfg, wc) })
		r.Put("/{id}", func(w http.ResponseWriter, rq *http.Request) { h.Update(w, rq, cfg, wc) })
		r.Patch("/{id}", func(w http.ResponseWriter, rq *http.Request) { h.Update(w, rq, cfg, wc) })
		r.Delete("/{id}", func(w http.ResponseWriter, rq *http.Request) { h.Delete(w, rq, wc) })
	}
}
