package telematics

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"
)

// 构帧 → 解析必须原样回来。协议代码最怕的是"自己跟自己一致但跟标准不一致"，
// 所以除了回环，下面还钉了几个字面值。
func TestJT808RoundTrip(t *testing.T) {
	frame, err := BuildJT808Location("013800138000", 121.473701, 31.230416, 68.4, 275, 7, "260601080000")
	if err != nil {
		t.Fatalf("构帧失败：%v", err)
	}
	if frame[0] != 0x7E || frame[len(frame)-1] != 0x7E {
		t.Fatalf("帧定界符不对：%x…%x", frame[0], frame[len(frame)-1])
	}
	m, err := ParseJT808(frame)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if m.MsgID != 0x0200 || !m.HasLocation {
		t.Fatalf("消息类型不对：msg_id=%#x has_location=%v", m.MsgID, m.HasLocation)
	}
	if m.TerminalPhone != "013800138000" {
		t.Errorf("终端号：得到 %s", m.TerminalPhone)
	}
	// 经纬度按 1e-6 度存整数，回环误差应在半个最低位以内
	if math.Abs(m.Lng-121.473701) > 5e-7 || math.Abs(m.Lat-31.230416) > 5e-7 {
		t.Errorf("经纬度：得到 %f,%f", m.Lng, m.Lat)
	}
	if math.Abs(m.SpeedKmh-68.4) > 0.05 {
		t.Errorf("速度：得到 %f", m.SpeedKmh)
	}
	if m.Direction != 275 {
		t.Errorf("方向：得到 %d", m.Direction)
	}
	if m.TimeBCD != "260601080000" {
		t.Errorf("时间 BCD：得到 %s", m.TimeBCD)
	}
}

// 与 Django 版 build_jt808_location 的输出逐字节对拍。参考帧由原 Python 实现生成
// （移植时从 git 历史取回 gateway.py 跑的），钉在这里防止将来改动悄悄改变线上格式
// ——终端固件不会跟着我们改，帧格式一变就是整个车队失联。
func TestJT808MatchesPythonReference(t *testing.T) {
	const want = "7e0200001c0138001380000007000000000000000001dc89d0073d8aa5000002ac0113260601080000b77e"
	got, err := BuildJT808Location("013800138000", 121.473701, 31.230416, 68.4, 275, 7, "260601080000")
	if err != nil {
		t.Fatalf("构帧失败：%v", err)
	}
	if hex.EncodeToString(got) != want {
		t.Fatalf("与 Python 参考帧不一致：\n got %s\nwant %s", hex.EncodeToString(got), want)
	}
}

// 0x7E / 0x7D 出现在正文里时必须转义，否则接收端会把帧从中间截断。
func TestJT808EscapeUnescape(t *testing.T) {
	raw := []byte{0x30, 0x7E, 0x08, 0x7D, 0x55}
	got := jt808Unescape(jt808Escape(raw))
	if !bytes.Equal(got, raw) {
		t.Fatalf("转义回环不一致：%x → %x", raw, got)
	}
	if esc := jt808Escape([]byte{0x7E}); !bytes.Equal(esc, []byte{0x7D, 0x02}) {
		t.Errorf("0x7E 应转义为 7D 02，得到 %x", esc)
	}
	if esc := jt808Escape([]byte{0x7D}); !bytes.Equal(esc, []byte{0x7D, 0x01}) {
		t.Errorf("0x7D 应转义为 7D 01，得到 %x", esc)
	}
}

// 坏帧必须报错而不是返回半个结果——静默接受脏数据会把假位置写进轨迹。
func TestJT808RejectsBadFrames(t *testing.T) {
	good, _ := BuildJT808Location("013800138000", 120, 30, 0, 0, 1, "260601080000")
	bad := append([]byte(nil), good...)
	bad[len(bad)-2] ^= 0xFF // 破坏校验和

	cases := map[string][]byte{
		"空帧":     {},
		"无定界符":   {0x01, 0x02, 0x03, 0x04},
		"过短":     {0x7E, 0x00, 0x7E},
		"校验和不匹配": bad,
	}
	for name, frame := range cases {
		if _, err := ParseJT808(frame); err == nil {
			t.Errorf("%s：应报错但通过了", name)
		}
	}
}

func TestNormalizeTerminalMessage(t *testing.T) {
	// JT808 分支：设备号取帧里的终端号
	frame, _ := BuildJT808Location("013800138000", 121.5, 31.2, 60, 90, 1, "260601080000")
	r, err := NormalizeTerminalMessage(frame, "")
	if err != nil {
		t.Fatalf("JT808 归一化失败：%v", err)
	}
	if r.DeviceNo != "013800138000" || r.Provider != "jt808" {
		t.Errorf("JT808 归一化：device_no=%s provider=%s", r.DeviceNo, r.Provider)
	}
	if r.ReportedAt != "2026-06-01T08:00:00+08:00" {
		t.Errorf("BCD 时间转换：得到 %s", r.ReportedAt)
	}

	// 显式给定的 device_no 覆盖帧内终端号（网关按 topic 约定设备时用）
	r2, _ := NormalizeTerminalMessage(frame, "DEV-001")
	if r2.DeviceNo != "DEV-001" {
		t.Errorf("显式 device_no 未生效：%s", r2.DeviceNo)
	}

	// JSON 分支
	r3, err := NormalizeTerminalMessage([]byte(`{"device_no":"DEV-9","lng":120.1,"lat":30.2,"speed_kmh":88}`), "")
	if err != nil {
		t.Fatalf("JSON 归一化失败：%v", err)
	}
	if r3.DeviceNo != "DEV-9" || r3.Provider != "mqtt" || r3.SpeedKmh == nil || *r3.SpeedKmh != 88 {
		t.Errorf("JSON 归一化结果不对：%+v", r3)
	}

	// 既不是帧也不是 JSON
	if _, err := NormalizeTerminalMessage([]byte("not json"), ""); err == nil {
		t.Error("非法负载应报错")
	}
	// 非位置汇报的 JT808 报文不该被当成上报吞下去
	heartbeat := buildRawJT808(0x0002, "013800138000", nil)
	if _, err := NormalizeTerminalMessage(heartbeat, ""); err == nil {
		t.Error("非 0x0200 报文应被拒绝")
	}
}

// buildRawJT808 拼一帧任意消息 ID 的报文（测试用）
func buildRawJT808(msgID int, phone string, body []byte) []byte {
	pb, _ := hex.DecodeString(phone)
	header := []byte{byte(msgID >> 8), byte(msgID), byte(len(body) >> 8), byte(len(body))}
	header = append(header, pb...)
	header = append(header, 0x00, 0x01)
	payload := append(header, body...)
	framed := append(payload, jt808Checksum(payload))
	return append(append([]byte{0x7E}, jt808Escape(framed)...), 0x7E)
}
