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
	"regexp"
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
	FURL                       // URLField（带 Django URLValidator 同款校验）
)

// urlRe 近似 Django URLValidator：scheme://host[:port][/path]，
// host 允许域名（需含合法 TLD）、IPv4、localhost。
var urlRe = regexp.MustCompile(`^(?i)(https?|ftps?)://` +
	`([^\s:@/]+(:[^\s:@/]*)?@)?` + // 可选 user:pass@
	`(` +
	`(\d{1,3}\.){3}\d{1,3}` + // IPv4
	`|localhost` +
	`|([a-z0-9\x{00a1}-\x{ffff}]([a-z0-9\x{00a1}-\x{ffff}-]*[a-z0-9\x{00a1}-\x{ffff}])?\.)+` +
	`[a-z\x{00a1}-\x{ffff}]{2,}\.?` +
	`)` +
	`(:\d{2,5})?([/?#]\S*)?$`)

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
	// BeforeCreate 可选钩子：落库前补服务端生成的列（单号、当前用户等）
	BeforeCreate func(ctx context.Context, h *Handler, r *http.Request, body map[string]any) (map[string]any, error)

	// CascadeTables 物理删除前需先清掉的从属行：表名 → 指向本资源的外键列。
	// Django 的 on_delete=CASCADE 由 ORM 收集器在 Python 层执行，原生 SQL 没有
	// 对应的库级 ON DELETE CASCADE 时会撞外键约束（典型是 M2M 中间表）。
	CascadeTables map[string]string

	// ModelDefaults 模型层默认值：库列 → 字面量。用于「不在序列化器里、但模型 default
	// 非零值」的 NOT NULL 列（如 DriverReminder.level='important'）——通用零值补齐会写成
	// ''/0，与 Django 不符，故需显式声明。
	ModelDefaults map[string]any

	// ReadOnly 对齐 ReadOnlyModelViewSet：只暴露 list/retrieve
	ReadOnly bool
	// NoCreate / NoUpdate / NoDelete 对齐 http_method_names 收窄或缺失的 mixin；
	// NoCreate 也用于「create 被 ViewSet 完全重写」的资源（由域内自定义 handler 接管）
	NoCreate bool
	NoUpdate bool
	NoDelete bool
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
	case FURL:
		s, ok := raw.(string)
		if !ok {
			return nil, "该字段必须是字符串。"
		}
		s = strings.TrimSpace(s)
		if s != "" && !urlRe.MatchString(s) {
			return nil, "请输入合法的URL。"
		}
		return s, ""
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

// object 按主键取单对象（含数据范围收窄）；未命中/越权时写 404 并返回 nil
func (h *Handler) object(w http.ResponseWriter, r *http.Request, cfg ResourceCfg, wc WriteCfg) map[string]any {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", wc.notFound())
		return nil
	}
	scoped, scopeErr := h.applyScope(r, cfg)
	if scopeErr != "" {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return nil
	}
	it, err := h.OneDetail(r.Context(), scoped, wc.whereByID(), id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusNotFound, "error", wc.notFound())
		return nil
	}
	return it
}

// Retrieve GET /<res>/{id}
func (h *Handler) Retrieve(w http.ResponseWriter, r *http.Request, cfg ResourceCfg, wc WriteCfg) {
	if it := h.object(w, r, cfg, wc); it != nil {
		httpx.JSON(w, http.StatusOK, it)
	}
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
	// 先跑字段级校验再跑钩子：DRF 是 is_valid() 通过后才进 create()，
	// 钩子里的业务校验（如 waybill_no 解析）必须排在字段必填之后。
	if _, _, details := h.collect(ctx, wc, body, true); len(details) > 0 {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
		return
	}
	if wc.BeforeCreate != nil {
		nb, err := wc.BeforeCreate(ctx, h, r, body)
		if err != nil {
			httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
				map[string]any{"detail": []string{err.Error()}})
			return
		}
		body = nb
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
	for col, dv := range wc.ModelDefaults {
		if provided[col] {
			continue
		}
		provided[col] = true
		cols = append(cols, col)
		args = append(args, dv)
		phs = append(phs, fmt.Sprintf("$%d", len(args)))
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
	if h.object(w, r, cfg, wc) == nil {
		return
	}
	id := chi.URLParam(r, "id")
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
	read := h.OneDetail
	if r.Method == http.MethodPatch {
		read = h.OnePartial // partial=True 下 DRF 会丢掉 source 链为 None 的只读关联字段
	}
	it, err := read(ctx, cfg, wc.whereByID(), id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// Delete DELETE /<res>/{id} —— 软删模型置 is_deleted，否则物理删；均返回 204
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request, cfg ResourceCfg, wc WriteCfg) {
	ctx := r.Context()
	if h.object(w, r, cfg, wc) == nil {
		return
	}
	id := chi.URLParam(r, "id")
	for tbl, col := range wc.CascadeTables {
		if _, err := h.DB.Exec(ctx, "DELETE FROM "+tbl+" WHERE "+col+"=$1::uuid", id); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "级联删除失败："+err.Error())
			return
		}
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

// CRUD 把一份读写配置绑成 chi 子路由（列表/创建/详情/更新/删除）。
// WriteCfg 上的 ReadOnly/NoUpdate/NoDelete 决定实际暴露哪些动作，
// 对齐各 ViewSet 选用的 mixin 组合与 http_method_names 收窄。
func (h *Handler) CRUD(cfg ResourceCfg, wc WriteCfg) func(chi.Router) {
	return func(r chi.Router) {
		if h.Fallback != nil {
			r.NotFound(h.Fallback.ServeHTTP)
		}
		// 未开放的动作走 DRF 同款 405（而非 chi 默认空体）
		r.MethodNotAllowed(func(w http.ResponseWriter, rq *http.Request) {
			httpx.Err(w, http.StatusMethodNotAllowed, "method_not_allowed",
				fmt.Sprintf("方法 “%s” 不被允许。", rq.Method))
		})
		// detail 路由：{id} 不是 UUID 时说明命中的其实是 detail=False 的自定义动作
		// （如 /drivers/lookup）——chi 的 {id} 会先吃掉它，故在此显式回代上游。
		detail := func(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
			return func(w http.ResponseWriter, rq *http.Request) {
				if _, err := uuid.Parse(chi.URLParam(rq, "id")); err != nil && h.Fallback != nil {
					h.Fallback.ServeHTTP(w, rq)
					return
				}
				next(w, rq)
			}
		}
		r.Get("/", func(w http.ResponseWriter, rq *http.Request) { h.List(w, rq, cfg) })
		r.Get("/{id}", detail(func(w http.ResponseWriter, rq *http.Request) { h.Retrieve(w, rq, cfg, wc) }))
		if wc.ReadOnly {
			return
		}
		if !wc.NoCreate {
			r.Post("/", func(w http.ResponseWriter, rq *http.Request) { h.Create(w, rq, cfg, wc) })
		}
		if !wc.NoUpdate {
			r.Put("/{id}", detail(func(w http.ResponseWriter, rq *http.Request) { h.Update(w, rq, cfg, wc) }))
			r.Patch("/{id}", detail(func(w http.ResponseWriter, rq *http.Request) { h.Update(w, rq, cfg, wc) }))
		}
		if !wc.NoDelete {
			r.Delete("/{id}", detail(func(w http.ResponseWriter, rq *http.Request) { h.Delete(w, rq, cfg, wc) }))
		}
	}
}
