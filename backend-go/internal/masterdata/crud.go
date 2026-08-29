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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/blob"
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
	Table string // 物理表
	Model string // 404 文案里的模型名（DRF 用 _meta.object_name，即英文类名）
	// Verbose 唯一冲突文案里的模型名。Django 的 unique 报错取 _meta.verbose_name
	// （中文），与 404 用的英文类名不是同一个东西，故单列一项；留空则退回 Model。
	Verbose    string
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

	// NullifyTables 对齐 on_delete=SET_NULL：表名 → 指向本资源的外键列，删除前置 NULL。
	// 与 CascadeTables 的区别是「引用方要保留」，例如组织被删后员工仍在、只是没了归属。
	NullifyTables map[string]string

	// BeforeDelete 删除前的自定义收集：依赖链超过一层（删 A → 级联删 B → B 还有引用方）
	// 时用它兜底，Django 的 ORM 收集器是递归的，声明式的两张表覆盖不到。
	BeforeDelete func(ctx context.Context, h *Handler, id string) error

	// ModelDefaults 模型层默认值：库列 → 字面量。用于「不在序列化器里、但模型 default
	// 非零值」的 NOT NULL 列（如 DriverReminder.level='important'）——通用零值补齐会写成
	// ''/0，与 Django 不符，故需显式声明。
	ModelDefaults map[string]any

	// ReadPerm / WritePerm 对齐 iam.permissions.HasPermission 的 required_permissions：
	// 安全方法查 read、其余查 write；留空表示该 ViewSet 未声明，已认证即可。
	// 这层闸门必须由引擎统一执行——只在少数自定义 handler 里手写，等于给通用
	// CRUD 开了一道无人看守的写入口。
	ReadPerm  string
	WritePerm string

	// ReadOnly 对齐 ReadOnlyModelViewSet：只暴露 list/retrieve
	ReadOnly bool
	// NoCreate / NoUpdate / NoDelete 对齐 http_method_names 收窄或缺失的 mixin；
	// NoCreate 也用于「create 被 ViewSet 完全重写」的资源（由域内自定义 handler 接管）
	NoCreate bool
	NoUpdate bool
	NoDelete bool

	// Upload 声明本资源接受 multipart 文件上传，并指定存到哪儿。
	// 不声明的资源收到 multipart 会被明确拒绝，而不是继续按 JSON 解析
	// 然后回一句"请求体不是合法 JSON"——那句话把"这个接口不收文件"
	// 说成了"你的请求写错了"，排查方向整个反了。
	Upload *UploadCfg
}

// UploadCfg 一个资源上的文件字段。
type UploadCfg struct {
	// Field 表单里的文件字段名，同时也是落库的列名（如 "file"）
	Field string
	// Prefix 存放键前缀，对齐 Django 的 upload_to（如 "receipts/"）
	Prefix string
	// MaxBytes 单文件上限，0 表示用 defaultUploadMax
	MaxBytes int64
}

// defaultUploadMax 凭证类文件的默认上限。回单、证件多是手机拍的照片。
const defaultUploadMax = 32 << 20

// decodeWriteBody 解请求体：JSON 照旧，multipart 则把表单字段铺成 map，
// 并把上传的文件真的存下来、把存放键写进 wc.Upload.Field。
//
// 为什么要在引擎这一层做：/receipts 和 /driver-credentials 前端都是 multipart
// （运单详情传回单、资源库传证件），而引擎只解 JSON，于是这两个功能在页面上
// 是**按下去就 400**的。司机自己那条路（/driver/credentials）另有实现且是好的，
// 所以只用 App 验收不会发现。放在引擎里是因为往后每个带 FileField 的资源
// 都会遇到同一件事，各写一份必然又会漏掉某一个。
func (h *Handler) decodeWriteBody(w http.ResponseWriter, r *http.Request, wc WriteCfg) (map[string]any, bool) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
				map[string]any{"detail": []string{"请求体不是合法 JSON。"}})
			return nil, false
		}
		return body, true
	}
	if wc.Upload == nil {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{"该接口不接受文件上传，请用 JSON 提交。"}})
		return nil, false
	}
	maxBytes := wc.Upload.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultUploadMax
	}
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{"表单解析失败，请确认文件大小。"}})
		return nil, false
	}
	body := map[string]any{}
	for k, v := range r.MultipartForm.Value {
		if len(v) > 0 {
			body[k] = coerceFormValue(wc.Fields[k].Kind, v[0])
		}
	}
	// 存放键由服务端生成，客户端传什么都不算数——否则表单里塞一个
	// file=../../etc/x 就能指到任意路径。
	delete(body, wc.Upload.Field)

	f, fh, err := r.FormFile(wc.Upload.Field)
	if err != nil {
		return body, true // 没带文件也允许：纯字段的 multipart 提交
	}
	defer f.Close()
	buf, rerr := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if rerr != nil {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{"文件读取失败。"}})
		return nil, false
	}
	if int64(len(buf)) > maxBytes {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败",
			map[string]any{"detail": []string{fmt.Sprintf("文件过大，请控制在 %dMB 内。", maxBytes>>20)}})
		return nil, false
	}
	rel := wc.Upload.Prefix + uuid.NewString() + safeExt(fh.Filename)
	if err := h.store().Put(r.Context(), rel, bytes.NewReader(buf),
		int64(len(buf)), http.DetectContentType(buf)); err != nil {
		// 存不下就报错。静默建一条没有文件的行，等于告诉用户"传好了"，
		// 而凭证其实不存在——那比直接报错坏得多。
		slog.Error("上传文件写入失败", "err", err, "table", wc.Table)
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "文件保存失败，请重试。")
		return nil, false
	}
	body[wc.Upload.Field] = rel
	return body, true
}

// coerceFormValue 把表单里的字符串按字段声明还原成对应类型。
//
// multipart 没有类型，什么都是字符串——前端一个 fd.append("self_uploaded", "false")，
// 到这边就是字符串 "false"，而布尔校验只认真正的 bool，于是回 400
// 「该字段必须是布尔值」。用户看到的是"上传失败"，没人猜得到是这个原因。
// 数字和日期那几类校验本来就接受字符串，不用动；只有 bool 和 JSON 需要还原。
//
// 这条是补第一版的漏：那一版只把表单字段原样铺进 body，用后端用例
// （只传了必填的几个字段）验过就以为成了，而页面上真正发的那一组里
// 带着 self_uploaded——照抄前端的字段清单去测，才碰得到这条路。
func coerceFormValue(kind FieldKind, s string) any {
	switch kind {
	case FBool:
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "on", "yes":
			return true
		case "false", "0", "off", "no", "":
			return false
		}
		return s // 不认识的值交给校验去报错，别在这里猜
	case FJSON:
		var v any
		if err := json.Unmarshal([]byte(s), &v); err == nil {
			return v
		}
		return s
	}
	return s
}

// safeExt 从用户给的文件名里取一个能安全拼进存放键的扩展名。
// 只留纯字母数字的短扩展名；拿不到就不要——键本来是 uuid，扩展名只为方便人看。
func safeExt(filename string) string {
	ext := filepath.Ext(filename)
	if len(ext) < 2 || len(ext) > 12 {
		return ""
	}
	for _, c := range ext[1:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return ""
		}
	}
	return ext
}

// store 取媒体存放实现。Blob 为 nil 时退回本地盘（老的构造方式）。
func (h *Handler) store() blob.Store {
	if h.Blob != nil {
		return h.Blob
	}
	root := h.MediaRoot
	if root == "" {
		root = "./media"
	}
	return blob.NewLocal(root)
}

// uniqueMsg 对齐 Django Model.unique_error_message：具有 <字段 verbose_name> 的 <模型 verbose_name> 已存在。
func (c WriteCfg) uniqueMsg(key string, f Field) string {
	label := f.Label
	if label == "" {
		label = key
	}
	model := c.Verbose
	if model == "" {
		model = c.Model
	}
	return fmt.Sprintf("具有 %s 的 %s 已存在。", label, model)
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
	body, ok := h.decodeWriteBody(w, r, wc)
	if !ok {
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
		if err := wc.AfterWrite(ctx, h, id.String(), body, true); err != nil {
			// 行已落库，回 500 会给出「创建失败但记录存在」的错误印象；但也绝不能装作没发生
			slog.Error("写后钩子失败", "table", wc.Table, "id", id.String(), "创建", true, "err", err)
		}
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
	body, ok := h.decodeWriteBody(w, r, wc)
	if !ok {
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
		if err := wc.AfterWrite(ctx, h, id, body, false); err != nil {
			slog.Error("写后钩子失败", "table", wc.Table, "id", id, "创建", false, "err", err)
		}
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
	if wc.BeforeDelete != nil {
		if err := wc.BeforeDelete(ctx, h, id); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "级联删除失败："+err.Error())
			return
		}
	}
	for tbl, col := range wc.NullifyTables {
		if _, err := h.DB.Exec(ctx, "UPDATE "+tbl+" SET "+col+"=NULL WHERE "+col+"=$1::uuid", id); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "级联删除失败："+err.Error())
			return
		}
	}
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
	if err != nil {
		// 删失败（多半是没收干净的外键引用）不能伪装成 404——那会让调用方以为
		// "本来就不存在"，而真相是"存在但删不掉"，两者要采取的动作完全不同。
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "删除失败："+err.Error())
		return
	}
	if ct.RowsAffected() == 0 {
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

// requiredMsg 对齐 DRF 三档必填文案：键缺失 / 显式 null / 空串是三种不同的错。
func requiredMsg(has bool, raw any) string {
	switch {
	case !has:
		return "该字段是必填项。"
	case raw == nil:
		return "该字段不能为 null。"
	default:
		return "该字段不能为空。"
	}
}

// collect 创建时收集列与值（含必填与唯一性校验）
func (h *Handler) collect(ctx context.Context, wc WriteCfg, body map[string]any, creating bool) ([]string, []any, map[string]any) {
	cols, vals := []string{}, []any{}
	details := map[string]any{}
	for key, f := range wc.Fields {
		raw, has := body[key]
		if !has || raw == nil || raw == "" {
			if creating && f.Required {
				details[key] = []string{requiredMsg(has, raw)}
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
				details[key] = []string{wc.uniqueMsg(key, f)}
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
		// 传了就要合法：partial=True 只放过「没传」，传空串/null 照样撞 blank/null 校验
		if f.Required && (raw == nil || raw == "") {
			details[key] = []string{requiredMsg(true, raw)}
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
				details[key] = []string{wc.uniqueMsg(key, f)}
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
// Allow 权限点闸门：want 为空表示不设限；被拒时已写 403 响应并返回 false。
// 文案与 HasPermission.message 一致（DRF 的 PermissionDenied.default_code 是 permission_denied）。
func (h *Handler) Allow(w http.ResponseWriter, r *http.Request, want string) bool {
	if want == "" {
		return true
	}
	me, err := h.Svc.UserByID(r.Context(), auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return false
	}
	_, _, perms, err := h.Svc.RolesAndPerms(r.Context(), me)
	if err != nil || !hasPerm(perms, want) {
		httpx.Err(w, http.StatusForbidden, "permission_denied", "缺少所需权限。")
		return false
	}
	return true
}

// gate 把权限校验裹在 handler 外层
func (h *Handler) gate(want string, next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Allow(w, r, want) {
			next(w, r)
		}
	}
}

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
		r.Get("/", h.gate(wc.ReadPerm, func(w http.ResponseWriter, rq *http.Request) { h.List(w, rq, cfg) }))
		r.Get("/{id}", detail(h.gate(wc.ReadPerm, func(w http.ResponseWriter, rq *http.Request) { h.Retrieve(w, rq, cfg, wc) })))
		if wc.ReadOnly {
			return
		}
		if !wc.NoCreate {
			r.Post("/", h.gate(wc.WritePerm, func(w http.ResponseWriter, rq *http.Request) { h.Create(w, rq, cfg, wc) }))
		}
		if !wc.NoUpdate {
			up := detail(h.gate(wc.WritePerm, func(w http.ResponseWriter, rq *http.Request) { h.Update(w, rq, cfg, wc) }))
			r.Put("/{id}", up)
			r.Patch("/{id}", up)
		}
		if !wc.NoDelete {
			r.Delete("/{id}", detail(h.gate(wc.WritePerm, func(w http.ResponseWriter, rq *http.Request) { h.Delete(w, rq, cfg, wc) })))
		}
	}
}
