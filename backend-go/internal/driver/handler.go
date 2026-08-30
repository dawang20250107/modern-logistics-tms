package driver

// 司机端 H5 五个端点，对齐 apps/ops/driver_portal.py。
//
// 安全语义原样保留：登录是「手机号 + 身份证后 6 位」双因子，两者缺一不可，
// 且档案里没有身份证号的司机一律拒绝登录——否则只凭一个手机号就能顶号，
// 而司机端能看到运单、能打卡推进状态、能传回单，顶号代价很高。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/waybills"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/blob"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

type Handler struct {
	DB        *pgxpool.Pool
	Secret    string // 与 Django SECRET_KEY 同源，保证 token 跨栈互认
	MediaRoot string
	// Blob 媒体存放。为 nil 时退回 MediaRoot 直接落盘。
	Blob blob.Store
}

// 司机端登录是「手机号 + 身份证后 6 位」，两道闸各挡一种打法。
//
// 原先只有一道按来源 IP 的闸（对齐 DriverLoginRateThrottle，10/min），实测两头都不对：
//
//	· 挡不住爆破——换 IP 就重置。同一个手机号连试 40 次、每次换来源 IP，
//	  一次都没被限住；有代理池就能把 6 位数字慢慢试完。
//	· 又会误伤——司机端是跑在运营商网络上的手机 App，一支车队共用一个出口 IP
//	  是常态。实测同一出口的第 11 个司机**登录成功**也被 429 挡下，
//	  早上集中上线时整队人互相挤掉。
//
// 所以：按手机号的闸挡定向爆破（被反复试的是那个号，换 IP 绕不开），
// 按 IP 的闸只对**失败**计数、挡广撒网式扫描——正常司机登录成功不消耗它，
// 共用出口就不会被连带挡住。/track 是同一套结构，见 orders/public.go。
var (
	loginByPhoneThrottle  = httpx.NewThrottle("THROTTLE_DRIVER_LOGIN", "10/min")
	loginFailByIPThrottle = httpx.NewThrottle("THROTTLE_DRIVER_LOGIN_FAIL_IP", "20/min")
)

// 在途运单状态集（司机端只看这些）
var activeWaybillStatuses = []string{
	"dispatched", "loaded", "departed", "in_transit", "arrived", "pending_dispatch",
}

// checkinNodes 打卡节点 → 中文名（对齐 DriverCheckin.NODE_CHOICES，顺序即业务顺序）
var checkinNodes = map[string]string{
	"depart": "出发", "arrive_pickup": "到达装货地", "queuing": "排队", "loading": "装货",
	"depart_loaded": "发车", "in_transit": "在途打卡", "arrive_delivery": "到达卸货地",
	"unloading": "卸货", "receipt": "回单", "finish": "订单结束",
}

// nodeToStatus 打卡节点 → 目标运单状态（对齐 workflow.NODE_TO_STATUS）
var nodeToStatus = map[string]string{
	"loading": "loaded", "depart_loaded": "departed",
	"in_transit": "in_transit", "arrive_delivery": "arrived",
}

// nextStep 司机极简流：按运单状态给出唯一「下一步动作」，司机只点一个主按钮
var nextStep = map[string]map[string]any{
	"pending_dispatch": {"node": "loading", "label": "确认装货", "kind": "checkin"},
	"dispatched":       {"node": "loading", "label": "确认装货", "kind": "checkin"},
	"loaded":           {"node": "depart_loaded", "label": "发车", "kind": "checkin"},
	"departed":         {"node": "in_transit", "label": "在途打卡", "kind": "checkin"},
	"in_transit":       {"node": "arrive_delivery", "label": "到达卸货地", "kind": "checkin"},
	"arrived":          {"node": "receipt", "label": "上传回单", "kind": "receipt"},
}

var credTypes = map[string]bool{
	"vehicle_license": true, "trailer_license": true, "driving_license": true,
	"transport_cert": true, "id_card": true,
}

type driverRow struct {
	ID, Name, Phone string
	AppRegistered   bool
}

// authDriver 从 X-Driver-Token / ?token / body.token 解析司机；失败已写响应
func (h *Handler) authDriver(w http.ResponseWriter, r *http.Request, bodyToken string) *driverRow {
	token := r.Header.Get("X-Driver-Token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		token = bodyToken
	}
	if token == "" {
		httpx.Err(w, http.StatusUnauthorized, "DRIVER_AUTH", "请先登录司机端。")
		return nil
	}
	id, err := UnsignToken(h.Secret, token)
	if err == errTokenExpired {
		httpx.Err(w, http.StatusUnauthorized, "DRIVER_TOKEN_EXPIRED", "登录已过期，请重新登录。")
		return nil
	} else if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "DRIVER_TOKEN_INVALID", "登录凭证无效。")
		return nil
	}
	d := &driverRow{}
	if _, perr := uuid.Parse(id); perr != nil {
		httpx.Err(w, http.StatusNotFound, "DRIVER_NOT_FOUND", "司机不存在。")
		return nil
	}
	err = h.DB.QueryRow(r.Context(), `
		SELECT id::text, name, COALESCE(phone,''), app_registered
		FROM md_driver WHERE id=$1::uuid`, id).Scan(&d.ID, &d.Name, &d.Phone, &d.AppRegistered)
	if err != nil {
		httpx.Err(w, http.StatusNotFound, "DRIVER_NOT_FOUND", "司机不存在。")
		return nil
	}
	return d
}

// Login POST /api/v1/driver/login —— 手机号 + 身份证后 6 位
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone  string `json:"phone"`
		IDTail string `json:"id_tail"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	phone := strings.TrimSpace(body.Phone)
	idTail := strings.TrimSpace(body.IDTail)
	if phone == "" || idTail == "" {
		httpx.Err(w, http.StatusBadRequest, "DRIVER_LOGIN_REQUIRED", "请输入手机号与身份证后 6 位。")
		return
	}
	if len(idTail) != 6 || !allDigits(idTail) {
		httpx.Err(w, http.StatusBadRequest, "DRIVER_LOGIN_REQUIRED", "身份证后 6 位格式不正确。")
		return
	}
	// 闸挂在被猜的那个号上，且要在查库**之前**——放到后面的话，
	// 爆破照样能让数据库替它把每个候选值都查一遍。
	if !loginByPhoneThrottle.GuardKey(w, phone) {
		return
	}
	d := &driverRow{}
	var idNo string
	// 始终校验身份证后 6 位；档案缺身份证号则无法验证身份，拒绝登录
	err := h.DB.QueryRow(r.Context(), `
		SELECT id::text, name, COALESCE(phone,''), app_registered, COALESCE(id_no,'')
		FROM md_driver WHERE phone=$1 AND NOT is_deleted
		ORDER BY created_at, id LIMIT 1`, phone).
		Scan(&d.ID, &d.Name, &d.Phone, &d.AppRegistered, &idNo)
	if err != nil || idNo == "" || !strings.HasSuffix(idNo, idTail) {
		// 只有这里计 IP 配额：失败才算，成功不算。
		if !loginFailByIPThrottle.Guard(w, r) {
			return
		}
		httpx.Err(w, http.StatusUnauthorized, "DRIVER_LOGIN_FAILED", "手机号或身份证后 6 位不匹配。")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token": SignToken(h.Secret, d.ID),
		"driver": map[string]any{
			"id": d.ID, "name": d.Name, "phone": d.Phone, "app_registered": d.AppRegistered,
		},
	})
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// driverTaskLimit 司机端一次最多返回多少张在途运单。
//
// 原先不限。演示库里一个司机有 3032 张在途单，实测一次 /driver/tasks：
//
//	**1.19 MB JSON**，前端把 3032 张卡片全渲染出来，
//	页面高 1,140,794 px —— 视口 844 px，也就是 1351 屏。
//
// 这是**手机上**的页面：那 1.19 MB 要走司机的流量，而他要找的是下一单在哪。
//
// 50 是按"司机手上同时有多少活"定的：干散货/整车的一天几单，
// 零担配送的一趟几十单，50 足够覆盖一天并留出余量；
// 超出的部分由界面明说，而不是悄悄截断。
const driverTaskLimit = 50

// Tasks GET /api/v1/driver/tasks —— 在途运单 + 待确认提醒（强制弹窗）
func (h *Handler) Tasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := h.authDriver(w, r, "")
	if d == nil {
		return
	}
	// 总数单独数一次：截断了就要在界面上说出来。
	// "把一部分说成全部"这一轮已经踩过四次（导出、调度池、登录审计、待核销队列），
	// 司机端这条是第五处——而且是最没被看见的一处：它在手机上。
	var total int
	if err := h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_waybill
		WHERE driver_id=$1::uuid AND status = ANY($2)`, d.ID, activeWaybillStatuses).Scan(&total); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT wb.waybill_no, wb.route_name, wb.origin, wb.destination, wb.status,
		       COALESCE(o.pickup_address,''), COALESCE(o.delivery_address,''),
		       COALESCE(o.pickup_contact_phone,''), COALESCE(o.delivery_contact_phone,''),
		       COALESCE(wb.cod_amount,0)::float8
		FROM ops_waybill wb LEFT JOIN ops_order o ON o.id = wb.order_id
		WHERE wb.driver_id=$1::uuid AND wb.status = ANY($2)
		ORDER BY wb.created_at DESC, wb.id
		LIMIT $3`, d.ID, activeWaybillStatuses, driverTaskLimit)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	wbs := []map[string]any{}
	for rows.Next() {
		var no, route, origin, dest, status, pickAddr, delAddr, pickPhone, delPhone string
		var cod float64
		if err := rows.Scan(&no, &route, &origin, &dest, &status,
			&pickAddr, &delAddr, &pickPhone, &delPhone, &cod); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取失败")
			return
		}
		var step any
		if s, ok := nextStep[status]; ok {
			step = s
		}
		wbs = append(wbs, map[string]any{
			"waybill_no": no, "route_name": route, "origin": origin, "destination": dest,
			"status": status, "status_label": labelOr(wbstatus.Label, status),
			"pickup_address": pickAddr, "delivery_address": delAddr,
			"pickup_contact_phone": pickPhone, "delivery_contact_phone": delPhone,
			"next_step": step, "cod_amount": cod,
		})
	}

	rrows, err := h.DB.Query(ctx, `
		SELECT dr.id::text, dr.title, dr.content, dr.level, dr.ack_required,
		       COALESCE(wb.waybill_no,'')
		FROM ops_driver_reminder dr LEFT JOIN ops_waybill wb ON wb.id = dr.waybill_id
		WHERE dr.driver_id=$1::uuid AND dr.status='`+wbstatus.ReminderPending+`'
		ORDER BY dr.sent_at DESC, dr.id`, d.ID)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rrows.Close()
	rems := []map[string]any{}
	for rrows.Next() {
		var id, title, content, level, wbNo string
		var ack bool
		if err := rrows.Scan(&id, &title, &content, &level, &ack, &wbNo); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取失败")
			return
		}
		rems = append(rems, map[string]any{
			"id": id, "title": title, "content": content, "level": level,
			"ack_required": ack, "waybill_no": wbNo,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"driver":            map[string]any{"name": d.Name, "phone": d.Phone},
		"waybills":          wbs,
		"waybill_total":     total,
		"pending_reminders": rems,
	})
}

func labelOr(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

// AckReminder POST /api/v1/driver/reminders/{id}/ack —— 强制弹窗点击确认
func (h *Handler) AckReminder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body := decodeJSON(r)
	d := h.authDriver(w, r, str(body, "token"))
	if d == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "REMINDER_NOT_FOUND", "提醒不存在。")
		return
	}
	var status string
	var wbID, wbNo *string
	err := h.DB.QueryRow(ctx, `
		SELECT dr.status, dr.waybill_id::text, wb.waybill_no
		FROM ops_driver_reminder dr LEFT JOIN ops_waybill wb ON wb.id = dr.waybill_id
		WHERE dr.id=$1::uuid AND dr.driver_id=$2::uuid`, id, d.ID).Scan(&status, &wbID, &wbNo)
	if err != nil {
		httpx.Err(w, http.StatusNotFound, "REMINDER_NOT_FOUND", "提醒不存在。")
		return
	}
	if status != "acknowledged" { // 幂等
		if _, err := h.DB.Exec(ctx, `
			UPDATE ops_driver_reminder SET status='`+wbstatus.ReminderAcknowledged+`', acknowledged_at=now(), updated_at=now()
			WHERE id=$1::uuid`, id); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败")
			return
		}
		status = "acknowledged"
		if wbID != nil {
			eid, _ := uuid.NewV7()
			pj, _ := json.Marshal(map[string]any{"reminder_id": id})
			res := ""
			if wbNo != nil {
				res = *wbNo
			}
			if _, err := h.DB.Exec(ctx, `
				INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type,
				  event_time, source, resource, payload)
				VALUES ($1, now(), now(), $2::uuid, 'reminder_acknowledged', clock_timestamp(), 'driver', $3, $4)`,
				eid.String(), *wbID, res, pj); err != nil {
				slog.Warn("司机端写库失败", "err", err)
			}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

// Checkin POST /api/v1/driver/checkin —— 节点 + 自动定位 + 水印照片，并自动推进运单
func (h *Handler) Checkin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	form := parseForm(r)
	d := h.authDriver(w, r, form.str("token"))
	if d == nil {
		return
	}
	var wbID, wbNo, status string
	err := h.DB.QueryRow(ctx, `
		SELECT id::text, waybill_no, status FROM ops_waybill
		WHERE waybill_no=$1 AND driver_id=$2::uuid`, form.str("waybill_no"), d.ID).
		Scan(&wbID, &wbNo, &status)
	if err != nil {
		httpx.Err(w, http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在或非本人运单。")
		return
	}
	node := form.str("node")
	if _, ok := checkinNodes[node]; !ok {
		httpx.Err(w, http.StatusBadRequest, "INVALID_NODE", "打卡节点非法。")
		return
	}
	lat, lng := coord(form.str("lat")), coord(form.str("lng"))
	note := form.str("note")
	if len([]rune(note)) > 255 {
		note = string([]rune(note)[:255])
	}

	// 打卡行的 id 先取出来：照片路径要带上它。
	id, _ := uuid.NewV7()

	// 水印照片：把拍摄时间、GPS、节点·司机·运单号焊进像素，事后无法靠改库洗掉
	photoRel := ""
	photoFailed := false
	if raw, name := form.file("photo"); raw != nil {
		_ = name
		lines := []string{
			time.Now().Format("2006-01-02 15:04:05"),
			"GPS " + coordText(lat) + "," + coordText(lng),
			checkinNodes[node] + " · " + d.Name + " · " + wbNo,
		}
		stamped := Watermark(raw, lines)
		// 路径必须带打卡行的 id。原先是 "<运单号>_<节点>.jpg"——同一节点再打一次
		// 就把上一张**原地覆盖**掉了，而弱网重试正是常规路径（界面上就有"重试"按钮）。
		// 实测连打两次：库里两行打卡记录，photo 都指向同一个文件，
		// 第一行拿到的是第二张照片——时间、GPS、节点全是第二次的。
		// 水印"焊进像素、事后无法靠改库洗掉"的意义，被一次普通重试就抹掉了。
		rel := "checkins/" + wbNo + "_" + node + "_" + id.String() + ".jpg"
		if err := h.saveMedia(rel, stamped); err == nil {
			photoRel = rel
		} else {
			// 存不下不该把打卡整个挡掉（司机在路上，打卡本身比照片要紧），
			// 但也绝不能装作成功——原先这里错误被直接吞掉，接口照样返回 201 ok，
			// 司机以为照片交了，实际上库里 photo 是 NULL。响应里如实说。
			photoFailed = true
			slog.Error("打卡照片存储失败", "waybill", wbNo, "node", node, "err", err)
		}
	}

	if _, err := h.DB.Exec(ctx, `
		INSERT INTO ops_driver_checkin (id, created_at, updated_at, waybill_id, driver_id,
		  node, lat, lng, photo, note, checkin_at)
		VALUES ($1, now(), now(), $2::uuid, $3::uuid, $4, $5::numeric, $6::numeric, $7, $8, now())`,
		id.String(), wbID, d.ID, node, lat, lng, nullIfEmpty(photoRel), note); err != nil {
		// 原始库错误只进日志：司机端的调用方是公网上的手机，
		// 把 SQLSTATE 和列类型回给它没有意义，也不该。
		slog.Error("打卡落库失败", "waybill", wbNo, "node", node, "err", err)
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "打卡失败，请稍后重试或联系调度。")
		return
	}

	// 工作流编排：打卡节点自动推进运单状态（弱网漏卡时一次补齐）
	newStatus := status
	if target, ok := nodeToStatus[node]; ok {
		tx, err := h.DB.Begin(ctx)
		if err == nil {
			if s, aerr := waybills.AdvanceTo(ctx, tx, wbNo, target, "司机打卡："+node); aerr == nil && s != "" {
				newStatus = s
				_ = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
		}
	}
	var checkinAt string
	_ = h.DB.QueryRow(ctx, "SELECT checkin_at FROM ops_driver_checkin WHERE id=$1::uuid", id.String()).
		Scan(&checkinAt)
	resp := map[string]any{
		"ok": true, "node": node, "checkin_at": checkinAt, "waybill_status": newStatus,
		"photo_saved": photoRel != "",
	}
	if photoFailed {
		resp["photo_error"] = "照片没能存下来，打卡已记录，请稍后在运单里补传。"
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// UploadCredential POST /api/v1/driver/credentials —— 司机自助上传证件（自传）
func (h *Handler) UploadCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	form := parseForm(r)
	d := h.authDriver(w, r, form.str("token"))
	if d == nil {
		return
	}
	credType := form.str("cred_type")
	if !credTypes[credType] {
		httpx.Err(w, http.StatusBadRequest, "INVALID_CRED_TYPE", "证件类型非法。")
		return
	}
	side := form.str("side")
	if side == "" {
		side = "main"
	}
	fileRel := ""
	if raw, name := form.file("file"); raw != nil {
		rel := "credentials/" + uuid.NewString() + filepath.Ext(name)
		if err := h.saveMedia(rel, raw); err == nil {
			fileRel = rel
		}
	}
	id, _ := uuid.NewV7()
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO md_driver_credential (id, created_at, updated_at, driver_id, cred_type, side,
		  file, file_url, ocr_status, ocr_result, holder_name, cert_no, expiry_date, self_uploaded)
		VALUES ($1, now(), now(), $2::uuid, $3, $4, $5, '', 'pending', '{}'::jsonb, '', '', NULL, true)`,
		id.String(), d.ID, credType, side, fileRel); err != nil {
		slog.Error("司机证件落库失败", "driver", d.ID, "cred_type", credType, "err", err)
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "上传失败，请稍后重试。")
		return
	}
	// 建档即触发识别；未配引擎时只落 status=manual，绝不伪造证件号与有效期
	ocrStatus := h.applyCredentialOCR(ctx, id.String(), fileRel, credType)
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"ok": true, "id": id.String(), "ocr_status": ocrStatus,
	})
}

// applyCredentialOCR 与 masterdata 的证件 OCR 同一套语义（未配引擎 → manual，不造数）
func (h *Handler) applyCredentialOCR(ctx context.Context, id, source, credType string) string {
	provider := os.Getenv("OCR_PROVIDER")
	result := map[string]any{
		"provider": "none", "status": "manual", "source": source, "cred_type": credType,
		"fields": map[string]any{"name": "", "cert_no": "", "plate_no": "", "id_no": "", "expiry_date": nil},
		"note":   "未配置证件 OCR 引擎，证件信息待人工录入/核验。",
	}
	if provider != "" {
		result["provider"] = provider
		result["note"] = "OCR 引擎 " + provider + " 尚未接入实现，证件信息待人工录入。"
	}
	rj, _ := json.Marshal(result)
	if _, err := h.DB.Exec(ctx, `
		UPDATE md_driver_credential SET ocr_result=$2::jsonb, ocr_status='manual', updated_at=now()
		WHERE id=$1::uuid`, id, rj); err != nil {
		slog.Warn("司机端写库失败", "err", err)
	}
	return "manual"
}

// saveMedia 存一份司机端上传的证件/打卡照。
// 走 blob.Store 而不是直接落盘：多副本部署时本地盘上的文件，
// 换个副本就读不到了。
func (h *Handler) saveMedia(rel string, data []byte) error {
	return h.store().Put(context.Background(), rel, bytes.NewReader(data),
		int64(len(data)), http.DetectContentType(data))
}

// store 取媒体存放实现。Blob 为 nil 时退回本地盘（老的构造方式）。
func (h *Handler) store() blob.Store {
	if h.Blob != nil {
		return h.Blob
	}
	return blob.NewLocal(h.MediaRoot)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// coord 坐标容错：非法/越界返回 nil，避免脏数据导致 500
func coord(v string) any {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < -180 || f > 180 {
		return nil
	}
	return f
}

func coordText(v any) string {
	if v == nil {
		return "-"
	}
	return strconv.FormatFloat(v.(float64), 'g', -1, 64)
}

// ── 请求体：司机端同时用 JSON 与 multipart ──

type formData struct {
	vals  map[string]string
	files map[string][]byte
	names map[string]string
}

func (f formData) str(k string) string { return f.vals[k] }
func (f formData) file(k string) ([]byte, string) {
	return f.files[k], f.names[k]
}

func parseForm(r *http.Request) formData {
	f := formData{vals: map[string]string{}, files: map[string][]byte{}, names: map[string]string{}}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(16 << 20); err == nil {
			for k, v := range r.MultipartForm.Value {
				if len(v) > 0 {
					f.vals[k] = v[0]
				}
			}
			for k, hs := range r.MultipartForm.File {
				if len(hs) == 0 {
					continue
				}
				fh, err := hs[0].Open()
				if err != nil {
					continue
				}
				raw, _ := io.ReadAll(io.LimitReader(fh, 16<<20))
				_ = fh.Close()
				f.files[k] = raw
				f.names[k] = hs[0].Filename
			}
		}
		return f
	}
	for k, v := range decodeJSON(r) {
		switch t := v.(type) {
		case string:
			f.vals[k] = t
		case float64:
			f.vals[k] = strconv.FormatFloat(t, 'g', -1, 64)
		case bool:
			f.vals[k] = strconv.FormatBool(t)
		}
	}
	return f
}

func decodeJSON(r *http.Request) map[string]any {
	var m map[string]any
	_ = json.NewDecoder(r.Body).Decode(&m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

var _ = pgx.ErrNoRows
