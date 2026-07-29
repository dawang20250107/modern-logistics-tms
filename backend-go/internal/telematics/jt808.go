package telematics

// JT/T 808-2013 终端接入：位置汇报(0x0200)的解析与构帧。
//
// 对齐 apps/telematics/gateway.py 的 parse_jt808 / build_jt808_location /
// normalize_terminal_message。全是纯函数——协议解析不该碰数据库，这样才好做
// 模拟器和单测；落库那一步交给已有的削峰队列。

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// JT808Message 一帧的解析结果；0x0200 位置汇报额外带位置字段
type JT808Message struct {
	MsgID         int
	TerminalPhone string // BCD 编码的终端手机号（12 位十六进制串）

	// 以下仅 0x0200 有效
	HasLocation bool
	Alarm       uint32
	Status      uint32
	Lat, Lng    float64
	Altitude    int
	SpeedKmh    float64
	Direction   int
	TimeBCD     string
}

// jt808Unescape 反转义：7D 01 → 7D，7D 02 → 7E
func jt808Unescape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == 0x7D && i+1 < len(data) {
			switch data[i+1] {
			case 0x01:
				out = append(out, 0x7D)
				i++
				continue
			case 0x02:
				out = append(out, 0x7E)
				i++
				continue
			}
		}
		out = append(out, data[i])
	}
	return out
}

// jt808Escape 转义：7E → 7D 02，7D → 7D 01
func jt808Escape(data []byte) []byte {
	out := make([]byte, 0, len(data)+8)
	for _, b := range data {
		switch b {
		case 0x7E:
			out = append(out, 0x7D, 0x02)
		case 0x7D:
			out = append(out, 0x7D, 0x01)
		default:
			out = append(out, b)
		}
	}
	return out
}

// jt808Checksum 逐字节异或
func jt808Checksum(data []byte) byte {
	var c byte
	for _, b := range data {
		c ^= b
	}
	return c
}

func be(b []byte) uint32 {
	var v uint32
	for _, x := range b {
		v = v<<8 | uint32(x)
	}
	return v
}

// ParseJT808 解析一帧 JT/T 808 报文
func ParseJT808(frame []byte) (JT808Message, error) {
	var m JT808Message
	if len(frame) < 4 || frame[0] != 0x7E || frame[len(frame)-1] != 0x7E {
		return m, errors.New("JT808 帧定界符非法")
	}
	inner := jt808Unescape(frame[1 : len(frame)-1])
	if len(inner) < 13 {
		return m, errors.New("JT808 帧过短")
	}
	payload, checksum := inner[:len(inner)-1], inner[len(inner)-1]
	if jt808Checksum(payload) != checksum {
		return m, errors.New("JT808 校验和不匹配")
	}
	m.MsgID = int(be(payload[0:2]))
	props := be(payload[2:4])
	bodyLen := int(props & 0x03FF)
	m.TerminalPhone = hex.EncodeToString(payload[4:10])
	end := 12 + bodyLen
	if end > len(payload) {
		end = len(payload)
	}
	body := payload[12:end]
	if m.MsgID == 0x0200 && len(body) >= 28 {
		m.HasLocation = true
		m.Alarm = be(body[0:4])
		m.Status = be(body[4:8])
		m.Lat = float64(be(body[8:12])) / 1_000_000
		m.Lng = float64(be(body[12:16])) / 1_000_000
		m.Altitude = int(be(body[16:18]))
		m.SpeedKmh = float64(be(body[18:20])) / 10.0
		m.Direction = int(be(body[20:22]))
		m.TimeBCD = hex.EncodeToString(body[22:28])
	}
	return m, nil
}

func putBE(dst []byte, v uint64) {
	for i := len(dst) - 1; i >= 0; i-- {
		dst[i] = byte(v)
		v >>= 8
	}
}

// BuildJT808Location 构造一帧 0x0200 位置汇报（模拟器与自测用）
func BuildJT808Location(phone string, lng, lat, speedKmh float64, direction, serial int, timeBCD string) ([]byte, error) {
	if timeBCD == "" {
		timeBCD = "260601080000"
	}
	tb, err := hex.DecodeString(timeBCD)
	if err != nil || len(tb) != 6 {
		return nil, fmt.Errorf("时间 BCD 非法：%s", timeBCD)
	}
	body := make([]byte, 0, 28)
	body = append(body, make([]byte, 8)...) // alarm + status 全 0
	four := make([]byte, 4)
	putBE(four, uint64(int64(lat*1_000_000+0.5)))
	body = append(body, four...)
	putBE(four, uint64(int64(lng*1_000_000+0.5)))
	body = append(body, four...)
	two := make([]byte, 2)
	body = append(body, 0, 0) // altitude
	putBE(two, uint64(int64(speedKmh*10+0.5)))
	body = append(body, two...)
	putBE(two, uint64(direction))
	body = append(body, two...)
	body = append(body, tb...)

	pb, err := hex.DecodeString(leftPad(phone, 12, '0'))
	if err != nil || len(pb) != 6 {
		return nil, fmt.Errorf("终端号 BCD 非法：%s", phone)
	}
	header := make([]byte, 0, 12)
	putBE(two, 0x0200)
	header = append(header, two...)
	putBE(two, uint64(len(body)&0x03FF))
	header = append(header, two...)
	header = append(header, pb...)
	putBE(two, uint64(serial))
	header = append(header, two...)

	payload := append(header, body...)
	framed := append(payload, jt808Checksum(payload))
	out := append([]byte{0x7E}, jt808Escape(framed)...)
	return append(out, 0x7E), nil
}

func leftPad(s string, n int, c byte) string {
	if len(s) >= n {
		return s[len(s)-n:]
	}
	return strings.Repeat(string(c), n-len(s)) + s
}

// bcdTimeToISO JT808 的 BCD 时间(YYMMDDhhmmss，北京时间) → ISO8601(+08:00)
func bcdTimeToISO(t string) string {
	if len(t) < 12 {
		return ""
	}
	return fmt.Sprintf("20%s-%s-%sT%s:%s:%s+08:00",
		t[0:2], t[2:4], t[4:6], t[6:8], t[8:10], t[10:12])
}

// NormalizeTerminalMessage 把一条终端消息归一化成 Report。
//
// JT808 帧以 0x7E 开头，其余按 JSON 文本处理——这个分派规则跟 Django 的 MQTT
// 网关一致，终端厂商各发各的，网关这层要都吃得下。
func NormalizeTerminalMessage(raw []byte, deviceNo string) (Report, error) {
	var r Report
	if len(raw) > 0 && raw[0] == 0x7E {
		m, err := ParseJT808(raw)
		if err != nil {
			return r, err
		}
		if !m.HasLocation {
			return r, fmt.Errorf("非位置汇报报文（msg_id=0x%04X），暂不处理", m.MsgID)
		}
		no := deviceNo
		if no == "" {
			no = m.TerminalPhone
		}
		lng, lat, spd, hd := m.Lng, m.Lat, m.SpeedKmh, float64(m.Direction)
		return Report{
			DeviceNo: no, Lng: &lng, Lat: &lat, SpeedKmh: &spd, Heading: &hd,
			ReportedAt: bcdTimeToISO(m.TimeBCD), Provider: "jt808",
		}, nil
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("上报既不是 JT808 帧也不是合法 JSON：%w", err)
	}
	if r.Provider == "" {
		r.Provider = "mqtt"
	}
	if deviceNo != "" {
		r.DeviceNo = deviceNo
	}
	return r, nil
}

// IngestTerminalReport 归一化后的上报入削峰队列；没有设备号也没有车牌的直接丢弃
// （落不到车上的点是噪声，留着只会污染轨迹）。
func (in *Ingestor) IngestTerminalReport(r Report) bool {
	if r.DeviceNo == "" && r.VehiclePlate == "" {
		return false
	}
	return in.enqueueTelemetry(r)
}
