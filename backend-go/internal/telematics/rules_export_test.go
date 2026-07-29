package telematics

import (
	"encoding/json"
	"os"
	"testing"
)

// 规则引擎的报警文案会直接呈现给调度并进入异常工单，必须与 Django 逐字一致。
// 本测试把一组代表性上报的判定结果导出，供与 Django 侧 evaluate_telemetry 对拍。
func TestExportRuleMatrix(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []Report{
		{SpeedKmh: f(95)}, {SpeedKmh: f(111)}, {SpeedKmh: f(90)},
		{TemperatureC: f(-20)}, {TemperatureC: f(25)}, {TemperatureC: f(0)}, {TemperatureC: f(8)},
		{FuelPct: f(14.5)}, {FuelPct: f(15)},
		{Events: []string{"fatigue", "abnormal_stop", "deviation", "unknown"}},
		{SpeedKmh: f(150.7), TemperatureC: f(30.25), FuelPct: f(3.2), Events: []string{"fatigue"}},
	}
	out := make([][]map[string]any, 0, len(cases))
	for _, c := range cases {
		row := []map[string]any{}
		for _, s := range evaluateTelemetry(c) {
			row = append(row, map[string]any{
				"alert_type": s.AlertType, "level": s.Level, "message": s.Message,
				"value": s.Value, "threshold": s.Threshold, "detail": s.Detail,
			})
		}
		out = append(out, row)
	}
	b, _ := json.MarshalIndent(out, "", " ")
	_ = os.WriteFile("/tmp/rules_go.json", b, 0o644)
}
