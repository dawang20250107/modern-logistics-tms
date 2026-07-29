// Package telematics 车联网域：设备上报削峰落库、实时车辆位置、轨迹回放、
// 指挥中心 KPI，以及规则报警引擎（超速/温度/油量/围栏/偏航/掉线）。
package telematics

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
	In  *Ingestor
}

// 车联网数据（GPS 轨迹/实时定位/报警/设备）敏感：读需查看权
const permView = "telematics.view"

// requirePerm 与 org 域同一套权限点校验；失败已写响应
func (h *Handler) requirePerm(w http.ResponseWriter, r *http.Request, want string) bool {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return false
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取权限失败")
		return false
	}
	for _, p := range perms {
		if p == "*" || p == want {
			return true
		}
	}
	httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "无权限："+want)
	return false
}

// pyISO 复刻 datetime.isoformat()（这些端点直接输出 isoformat()，不经 DRF 序列化器）
func pyISO(t time.Time) string {
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05-07:00")
	}
	return t.Format("2006-01-02T15:04:05.000000-07:00")
}

// drfISO 经 DRF DateTimeField 输出的时间：+00:00 会被替换成 Z
func drfISO(t time.Time) string {
	return strings.Replace(pyISO(t), "+00:00", "Z", 1)
}

// Live GET /telematics/vehicles/live?online=true —— 实时车辆位置列表
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permView) {
		return
	}
	where := ""
	args := []any{}
	if v := r.URL.Query().Get("online"); v != "" {
		lv := strings.ToLower(v)
		args = append(args, lv == "1" || lv == "true" || lv == "yes")
		where = " WHERE s.online = $1"
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT s.id::text, s.vehicle_id::text, COALESCE(v.plate_no,''), COALESCE(v.vehicle_type,''),
		       s.waybill_id::text, COALESCE(w.waybill_no,''),
		       s.lng::text, s.lat::text, s.speed_kmh::text, s.heading, s.mileage_km::text,
		       s.temperature_c::text, s.fuel_pct::text, s.online, s.reported_at
		FROM tel_vehicle_state s
		LEFT JOIN md_vehicle v ON v.id = s.vehicle_id
		LEFT JOIN ops_waybill w ON w.id = s.waybill_id`+where+`
		ORDER BY s.reported_at DESC NULLS LAST, s.id`, args...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, vehicle, plate, vtype, wbNo, lng, lat, speed, mileage string
		var waybill, temp, fuel *string
		var heading int
		var online bool
		var at *time.Time
		if err := rows.Scan(&id, &vehicle, &plate, &vtype, &waybill, &wbNo,
			&lng, &lat, &speed, &heading, &mileage, &temp, &fuel, &online, &at); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取失败")
			return
		}
		var atOut any
		if at != nil {
			atOut = pyISO(*at)
		}
		items = append(items, map[string]any{
			"id": id, "vehicle": vehicle, "vehicle_plate": plate, "vehicle_type": vtype,
			"waybill": waybill, "waybill_no": wbNo, "lng": lng, "lat": lat,
			"speed_kmh": speed, "heading": heading, "mileage_km": mileage,
			"temperature_c": temp, "fuel_pct": fuel, "online": online, "reported_at": atOut,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"vehicles": items})
}

// Trajectory GET /telematics/waybills/{no}/trajectory —— 轨迹回放 + 停留点 + 超速段
func (h *Handler) Trajectory(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permView) {
		return
	}
	ctx := r.Context()
	no := chi.URLParam(r, "no")
	var exists bool
	_ = h.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM ops_waybill WHERE waybill_no=$1)", no).Scan(&exists)
	if !exists {
		httpx.Err(w, http.StatusNotFound, "WAYBILL_NOT_FOUND", "运单不存在。")
		return
	}
	q := r.URL.Query()
	where := "w.waybill_no = $1"
	args := []any{no}
	if s := q.Get("from"); s != "" {
		args = append(args, s)
		where += " AND p.reported_at >= $2::timestamptz"
	}
	if s := q.Get("to"); s != "" {
		args = append(args, s)
		where += " AND p.reported_at <= $" + strconv.Itoa(len(args)) + "::timestamptz"
	}
	rows, err := h.DB.Query(ctx, `
		SELECT p.lng::float8, p.lat::float8, p.speed_kmh::float8, p.reported_at
		FROM ops_tracking_point p JOIN ops_waybill w ON w.id = p.waybill_id
		WHERE `+where+` ORDER BY p.reported_at, p.id`, args...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	pts := []TrackPoint{}
	for rows.Next() {
		var p TrackPoint
		if rows.Scan(&p.Lng, &p.Lat, &p.SpeedKmh, &p.ReportedAt) != nil {
			break
		}
		pts = append(pts, p)
	}

	speedLimit := speedLimitKmh
	if v := q.Get("speed_limit"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			speedLimit = f
		}
	}
	a := AnalyzeTrajectory(pts, 200.0, 600, speedLimit)

	outPts := make([]map[string]any, 0, len(pts))
	for _, p := range pts {
		outPts = append(outPts, map[string]any{
			"lng": p.Lng, "lat": p.Lat, "speed_kmh": p.SpeedKmh, "reported_at": pyISO(p.ReportedAt),
		})
	}
	stops := make([]map[string]any, 0, len(a.Stops))
	for _, s := range a.Stops {
		stops = append(stops, map[string]any{
			"lng": s.Lng, "lat": s.Lat, "from": pyISO(s.From), "to": pyISO(s.To),
			"duration_seconds": s.DurationSeconds,
		})
	}
	segs := make([]map[string]any, 0, len(a.OverspeedSegments))
	for _, s := range a.OverspeedSegments {
		segs = append(segs, map[string]any{
			"from": pyISO(s.From), "to": pyISO(s.To), "max_speed": s.MaxSpeed,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waybill_no": no, "points": outPts, "stops": stops,
		"overspeed_segments": segs, "total_points": a.TotalPoints,
	})
}

// CommandCenterSummary GET /telematics/command-center/summary —— 一屏 KPI
func (h *Handler) CommandCenterSummary(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permView) {
		return
	}
	var online, offline, pending, inTransit, openAlerts, highAlerts int
	if err := h.DB.QueryRow(r.Context(), `
		SELECT (SELECT count(*) FROM tel_vehicle_state WHERE online),
		       (SELECT count(*) FROM tel_vehicle_state WHERE NOT online),
		       (SELECT count(*) FROM ops_waybill WHERE status='pending_dispatch'),
		       (SELECT count(*) FROM ops_waybill WHERE status='in_transit'),
		       (SELECT count(*) FROM tel_alert WHERE status='open'),
		       (SELECT count(*) FROM tel_alert WHERE status='open' AND level='high')`).
		Scan(&online, &offline, &pending, &inTransit, &openAlerts, &highAlerts); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"online_vehicles": online, "offline_vehicles": offline,
		"pending_dispatch": pending, "in_transit": inTransit,
		"open_alerts": openAlerts, "high_alerts": highAlerts,
	})
}

// Ingest POST /telematics/ingest —— 设备上报批量入口（202 + 异步落库）
func (h *Handler) Ingest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reports json.RawMessage `json:"reports"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var reports []Report
	if len(body.Reports) > 0 && json.Unmarshal(body.Reports, &reports) != nil {
		httpx.Err(w, http.StatusBadRequest, "INVALID_REPORTS", "reports 必须是数组。")
		return
	}
	queued := 0
	for _, rep := range reports {
		if rep.DeviceNo == "" && rep.VehiclePlate == "" {
			continue
		}
		if h.In.enqueueTelemetry(rep) {
			queued++
		}
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"queued": queued, "status": "queued_for_async_persist",
	})
}

// TrackingIngest POST /tracking/points —— 轨迹批量上报（202 + 异步落库）
func (h *Handler) TrackingIngest(w http.ResponseWriter, r *http.Request) {
	const maxPoints = 1000
	var body struct {
		Points json.RawMessage `json:"points"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var points []TrackPointReport
	if len(body.Points) > 0 && json.Unmarshal(body.Points, &points) != nil {
		httpx.Err(w, http.StatusBadRequest, "TRACK_POINTS_INVALID", "points 必须为数组。")
		return
	}
	if len(points) > maxPoints {
		httpx.Err(w, http.StatusRequestEntityTooLarge, "TRACK_POINTS_TOO_MANY",
			"单次最多上报 "+strconv.Itoa(maxPoints)+" 个轨迹点。")
		return
	}
	queued := 0
	for _, p := range points {
		// 丢弃非法坐标，避免脏数据落库
		if p.WaybillNo == "" || !validCoord(p.Lat) || !validCoord(p.Lng) {
			continue
		}
		if h.In.enqueueTracking(p) {
			queued++
		}
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"queued": queued, "received": len(points), "status": "queued_for_async_persist",
	})
}

func validCoord(v *float64) bool {
	return v != nil && *v >= -180 && *v <= 180
}

// WaybillTracking GET /waybills/{no}/tracking —— 最近 200 个轨迹点（倒序）
func (h *Handler) WaybillTracking(w http.ResponseWriter, r *http.Request) {
	no := chi.URLParam(r, "no")
	var exists bool
	_ = h.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM ops_waybill WHERE waybill_no=$1)", no).Scan(&exists)
	if !exists {
		httpx.Err(w, http.StatusNotFound, "error", "未找到。")
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT p.id::text, p.lng::text, p.lat::text, p.speed_kmh::text, p.reported_at, p.provider
		FROM ops_tracking_point p JOIN ops_waybill w ON w.id = p.waybill_id
		WHERE w.waybill_no=$1 ORDER BY p.reported_at DESC, p.id LIMIT 200`, no)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, lng, lat, speed, provider string
		var at time.Time
		if rows.Scan(&id, &lng, &lat, &speed, &at, &provider) != nil {
			break
		}
		items = append(items, map[string]any{
			"id": id, "lng": lng, "lat": lat, "speed_kmh": speed,
			"reported_at": drfISO(at), "provider": provider,
		})
	}
	httpx.JSON(w, http.StatusOK, items)
}
