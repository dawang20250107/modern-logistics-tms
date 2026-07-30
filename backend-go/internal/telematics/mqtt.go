package telematics

// MQTT 终端接入网关（替代 Django 的 mqtt_gateway 管理命令）。
//
// Django 版是个前台常驻命令，得单独起一个进程守着；Go 版直接跑在网关里的一条
// 协程上——上报最终要进的削峰队列本来就在这个进程内，多一跳进程只是多一个会掉
// 线的东西。broker 地址没配就整个不启用。

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTTOptions 接入参数；Host 为空表示不启用
type MQTTOptions struct {
	Host     string
	Port     int
	Topic    string
	Username string
	Password string
}

// StartMQTTGateway 订阅 broker，把终端上报归一化后压入削峰队列。
// 不阻塞：连不上时 paho 自己退避重连，网关照常提供 HTTP 服务。
func (in *Ingestor) StartMQTTGateway(ctx context.Context, o MQTTOptions) {
	if o.Host == "" {
		return
	}
	if o.Port == 0 {
		o.Port = 1883
	}
	if o.Topic == "" {
		o.Topic = "tms/telemetry/#"
	}
	broker := fmt.Sprintf("tcp://%s:%d", o.Host, o.Port)

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(fmt.Sprintf("tms-gateway-%d", time.Now().UnixNano())).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10 * time.Second).
		SetKeepAlive(60 * time.Second)
	if o.Username != "" {
		opts.SetUsername(o.Username).SetPassword(o.Password)
	}
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		// 订阅放在 OnConnect 里：重连之后订阅关系不会自己回来
		if tok := c.Subscribe(o.Topic, 0, func(_ mqtt.Client, msg mqtt.Message) {
			in.handleTerminalMessage(msg.Payload())
		}); tok.Wait() && tok.Error() != nil {
			slog.Error("MQTT 订阅失败", "topic", o.Topic, "err", tok.Error())
			return
		}
		slog.Info("MQTT 网关已连接", "broker", broker, "topic", o.Topic)
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		slog.Warn("MQTT 连接断开，等待重连", "err", err)
	})

	client := mqtt.NewClient(opts)
	go func() {
		client.Connect() // SetConnectRetry 已开，失败会自己重试，不必在这里等
		<-ctx.Done()
		client.Disconnect(250)
	}()
}

// handleTerminalMessage 单条消息：解析失败只记日志，绝不影响后续消息。
// 终端固件参差不齐，一条脏帧不该让整个订阅停摆。
func (in *Ingestor) handleTerminalMessage(payload []byte) {
	r, err := NormalizeTerminalMessage(payload, "")
	if err != nil {
		slog.Warn("终端上报解析失败", "err", err, "bytes", len(payload))
		return
	}
	if !in.IngestTerminalReport(r) {
		slog.Warn("终端上报入队失败（缺设备号/车牌，或队列已满）", "device_no", r.DeviceNo)
	}
}
