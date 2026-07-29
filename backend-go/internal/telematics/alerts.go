package telematics

// 报警规则引擎 + 上报落库，对齐 apps/telematics/services.py。
//
// 三条不能丢的语义：
//  1. 去重窗口：同车同类型在 ALERT_DEDUP_MINUTES 内已有未处理报警则不再重复生成，
//     否则一辆超速的车会在几分钟内刷出上百条报警，把调度台淹掉。
//  2. 围栏只在「进出跳变」时报警，不是每次上报都报；首次见到该车该围栏只建状态不报警。
//  3. 高危报警关联运单时自动开异常工单（同运单同类型未闭环则跳过），
//     让「报警」真正进入有人负责的工单流，而不是只躺在报警列表里。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 阈值默认值对齐 settings（可用同名环境变量覆盖）
var (
	speedLimitKmh   = envFloat("ALERT_SPEED_LIMIT_KMH", 90.0)
	speedHighMargin = envFloat("ALERT_SPEED_HIGH_MARGIN", 20.0)
	tempMinC        = envFloat("ALERT_TEMP_MIN_C", -18.0)
	tempMaxC        = envFloat("ALERT_TEMP_MAX_C", 8.0)
	fuelLowPct      = envFloat("ALERT_FUEL_LOW_PCT", 15.0)
	alertDedupMin   = int(envFloat("ALERT_DEDUP_MINUTES", 15))
	offlineMinutes  = int(envFloat("DEVICE_OFFLINE_MINUTES", 10))
)

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// eventAlertMap 设备上报事件码 → (报警类型, 等级)
var eventAlertMap = map[string][2]string{
	"fatigue":       {"fatigue", "high"},
	"abnormal_stop": {"abnormal_stop", "medium"},
	"deviation":     {"deviation", "medium"},
}

// alertSpec 待落库的报警
type alertSpec struct {
	AlertType string
	Level     string
	Message   string
	Value     *float64
	Threshold *float64
	Detail    map[string]any
}

// Report 单条设备上报
type Report struct {
	DeviceNo     string   `json:"device_no"`
	VehiclePlate string   `json:"vehicle_plate"`
	WaybillNo    string   `json:"waybill_no"`
	Lng          *float64 `json:"lng"`
	Lat          *float64 `json:"lat"`
	SpeedKmh     *float64 `json:"speed_kmh"`
	Heading      *float64 `json:"heading"`
	MileageKm    *float64 `json:"mileage_km"`
	TemperatureC *float64 `json:"temperature_c"`
	FuelPct      *float64 `json:"fuel_pct"`
	ReportedAt   string   `json:"reported_at"`
	Provider     string   `json:"provider"`
	Events       []string `json:"events"`
}

func fptr(v float64) *float64 { return &v }

// evaluateTelemetry 单条上报 → 应触发的报警（不落库），对齐 evaluate_telemetry
func evaluateTelemetry(r Report) []alertSpec {
	var out []alertSpec
	if r.SpeedKmh != nil && *r.SpeedKmh > speedLimitKmh {
		level := "medium"
		if *r.SpeedKmh > speedLimitKmh+speedHighMargin {
			level = "high"
		}
		out = append(out, alertSpec{
			AlertType: "overspeed", Level: level,
			Message: fmt.Sprintf("超速 %.0f km/h（限速 %.0f）", *r.SpeedKmh, speedLimitKmh),
			Value:   r.SpeedKmh, Threshold: fptr(speedLimitKmh),
		})
	}
	if r.TemperatureC != nil && (*r.TemperatureC < tempMinC || *r.TemperatureC > tempMaxC) {
		out = append(out, alertSpec{
			AlertType: "temperature", Level: "high",
			Message: fmt.Sprintf("温度异常 %.1f℃（允许 %s~%s℃）", *r.TemperatureC, pyFloat(tempMinC), pyFloat(tempMaxC)),
			Value:   r.TemperatureC, Threshold: fptr(tempMaxC),
		})
	}
	if r.FuelPct != nil && *r.FuelPct < fuelLowPct {
		out = append(out, alertSpec{
			AlertType: "fuel", Level: "medium",
			Message: fmt.Sprintf("油量偏低 %.0f%%（阈值 %s%%）", *r.FuelPct, pyFloat(fuelLowPct)),
			Value:   r.FuelPct, Threshold: fptr(fuelLowPct),
		})
	}
	for _, code := range r.Events {
		if m, ok := eventAlertMap[code]; ok {
			out = append(out, alertSpec{
				AlertType: m[0], Level: m[1], Message: "设备事件：" + code,
				Detail: map[string]any{"event": code},
			})
		}
	}
	return out
}

// pyFloat 复刻 Python 对 float 的 str()：整数值也带 .0（文案要逐字一致）
func pyFloat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatFloat(f, 'f', 1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// raiseAlert 落报警；dedup 时同车同类型在窗口内已有 open 报警则跳过。返回是否新建。
func raiseAlert(ctx context.Context, db *pgxpool.Pool, s alertSpec,
	vehicleID, deviceID, waybillID *string, triggeredAt time.Time, dedup bool) bool {
	if dedup && vehicleID != nil {
		var exists bool
		// 这条判定失败会让去重整体失效（一辆超速车能刷爆报警列表），出错必须留痕
		if err := db.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM tel_alert
			  WHERE vehicle_id=$1::uuid AND alert_type=$2 AND status='open'
			    AND triggered_at >= now() - make_interval(mins => $3))`,
			*vehicleID, s.AlertType, alertDedupMin).Scan(&exists); err != nil {
			slog.Warn("alert dedup check", "err", err)
		}
		if exists {
			return false
		}
	}
	level := s.Level
	if level == "" {
		level = "medium"
	}
	detail := s.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	dj, _ := json.Marshal(detail)
	id, _ := uuid.NewV7()
	if _, err := db.Exec(ctx, `
		INSERT INTO tel_alert (id, created_at, updated_at, alert_type, level, status, vehicle_id,
		  device_id, waybill_id, message, value, threshold, detail, triggered_at, handled_at, handled_by_id)
		VALUES ($1, now(), now(), $2, $3, 'open', $4::uuid, $5::uuid, $6::uuid, $7,
		        $8::numeric, $9::numeric, $10::jsonb, $11, NULL, NULL)`,
		id.String(), s.AlertType, level, vehicleID, deviceID, waybillID, s.Message,
		s.Value, s.Threshold, dj, triggeredAt); err != nil {
		return false
	}
	maybeOpenException(ctx, db, s.AlertType, level, s.Message, waybillID)
	return true
}

// exceptionAlertTypes 会自动转异常工单的高危报警类型
var exceptionAlertTypes = map[string]bool{
	"deviation": true, "offline": true, "temperature": true,
	"fatigue": true, "abnormal_stop": true,
}

// maybeOpenException 高危报警挂运单时自动开异常工单（同运单同类型未闭环则跳过）
func maybeOpenException(ctx context.Context, db *pgxpool.Pool, alertType, level, message string, waybillID *string) {
	if waybillID == nil || level != "high" || !exceptionAlertTypes[alertType] {
		return
	}
	var exists bool
	_ = db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM ops_exception
		  WHERE waybill_id=$1::uuid AND exception_type=$2 AND status NOT IN ('closed','rejected'))`,
		*waybillID, alertType).Scan(&exists)
	if exists {
		return
	}
	id, _ := uuid.NewV7()
	_, _ = db.Exec(ctx, `
		INSERT INTO ops_exception (id, created_at, updated_at, waybill_id, exception_type, level,
		  source, description, status, responsibility_party, amount, resolution)
		VALUES ($1, now(), now(), $2::uuid, $3, 'high', 'track', $4, 'open', '', 0, '')`,
		id.String(), *waybillID, alertType, "[自动] "+message)
}

type geofenceRow struct {
	ID, Name, Shape, Purpose string
	CenterLng, CenterLat     *float64
	RadiusM                  float64
	Polygon                  [][]float64
}

func loadGeofences(ctx context.Context, db *pgxpool.Pool) []geofenceRow {
	rows, err := db.Query(ctx, `
		SELECT id::text, name, shape, purpose, center_lng::float8, center_lat::float8,
		       radius_m::float8, polygon
		FROM tel_geofence WHERE is_active`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []geofenceRow{}
	for rows.Next() {
		var g geofenceRow
		var poly []byte
		if rows.Scan(&g.ID, &g.Name, &g.Shape, &g.Purpose, &g.CenterLng, &g.CenterLat, &g.RadiusM, &poly) != nil {
			break
		}
		_ = json.Unmarshal(poly, &g.Polygon)
		out = append(out, g)
	}
	return out
}

func pointInGeofence(g geofenceRow, lng, lat float64) bool {
	switch g.Shape {
	case "circle":
		if g.CenterLng == nil || g.CenterLat == nil {
			return false
		}
		return pointInCircle(lng, lat, *g.CenterLng, *g.CenterLat, g.RadiusM)
	case "polygon":
		return pointInPolygon(lng, lat, g.Polygon)
	}
	return false
}

// evaluateGeofences 只在进出跳变时报警；首见该车该围栏仅建状态不报警
func evaluateGeofences(ctx context.Context, db *pgxpool.Pool, vehicleID *string, lng, lat float64,
	waybillID *string, reportedAt time.Time, fences []geofenceRow) int {
	if vehicleID == nil {
		return 0
	}
	raised := 0
	for _, f := range fences {
		insideNow := pointInGeofence(f, lng, lat)
		var stateID string
		var wasInside bool
		var since *time.Time
		err := db.QueryRow(ctx, `
			SELECT id::text, inside, since FROM tel_geofence_state
			WHERE vehicle_id=$1::uuid AND geofence_id=$2::uuid`, *vehicleID, f.ID).
			Scan(&stateID, &wasInside, &since)
		if err != nil { // get_or_create 的 create 分支
			sid, _ := uuid.NewV7()
			_, _ = db.Exec(ctx, `
				INSERT INTO tel_geofence_state (id, created_at, updated_at, vehicle_id, geofence_id, inside, since)
				VALUES ($1, now(), now(), $2::uuid, $3::uuid, false, NULL)`,
				sid.String(), *vehicleID, f.ID)
			stateID, wasInside, since = sid.String(), false, nil
		}
		if insideNow == wasInside && since != nil {
			continue // 状态未变化
		}
		transitioned := since != nil && insideNow != wasInside
		_, _ = db.Exec(ctx, `
			UPDATE tel_geofence_state SET inside=$2, since=$3, updated_at=now() WHERE id=$1::uuid`,
			stateID, insideNow, reportedAt)
		if !transitioned {
			continue
		}
		action, act := "离开", "exit"
		if insideNow {
			action, act = "进入", "enter"
		}
		level := "info"
		if f.Purpose == "restricted" {
			level = "high"
		}
		if raiseAlert(ctx, db, alertSpec{
			AlertType: "geofence", Level: level,
			Message: fmt.Sprintf("%s围栏「%s」", action, f.Name),
			Detail:  map[string]any{"geofence": f.Name, "action": act},
		}, vehicleID, nil, waybillID, reportedAt, false) {
			raised++
		}
	}
	return raised
}

// evaluateDeviation 运单绑定规划线路时检测是否偏离走廊
func evaluateDeviation(ctx context.Context, db *pgxpool.Pool, vehicleID *string, lng, lat float64,
	waybillID *string, reportedAt time.Time) int {
	if waybillID == nil {
		return 0
	}
	var code, name string
	var corridor float64
	var wp []byte
	if err := db.QueryRow(ctx, `
		SELECT r.code, r.name, r.corridor_m::float8, r.waypoints
		FROM ops_waybill w JOIN md_route r ON r.id = w.planned_route_id
		WHERE w.id=$1::uuid`, *waybillID).Scan(&code, &name, &corridor, &wp); err != nil {
		return 0
	}
	var line [][]float64
	if json.Unmarshal(wp, &line) != nil || len(line) == 0 {
		return 0
	}
	dist := distanceToPolylineM(lng, lat, line)
	if dist <= corridor {
		return 0
	}
	if raiseAlert(ctx, db, alertSpec{
		AlertType: "deviation", Level: "high",
		Message: fmt.Sprintf("偏离规划线路「%s」约 %.1f km", name, dist/1000),
		Value:   fptr(roundTo(dist, 2)), Threshold: fptr(corridor),
		Detail: map[string]any{"route": code},
	}, vehicleID, nil, waybillID, reportedAt, true) {
		return 1
	}
	return 0
}

func roundTo(v float64, places int) float64 {
	p := 1.0
	for i := 0; i < places; i++ {
		p *= 10
	}
	return float64(int64(v*p+0.5)) / p
}
