// Package config 集中环境配置。变量名沿用并跑期的 DJANGO_* 前缀：
// 部署脚本与密钥管理都按这套名字配好了，为了改名去动运维不划算。
package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr    string // Go 网关监听地址（对外入口）
	DatabaseURL   string // PostgreSQL 连接串（与 Django 共库）
	SecretKey     string // 必须与 DJANGO_SECRET_KEY 一致：签发/校验 simplejwt 兼容 token
	PublicBase    string // 对外可访问的基址（拼 avatar_url 等绝对地址）
	CORSOrigins   []string
	AccessMinutes int    // 与 SIMPLE_JWT.ACCESS_TOKEN_LIFETIME 对齐
	RefreshDays   int    // 与 SIMPLE_JWT.REFRESH_TOKEN_LIFETIME 对齐
	MediaRoot     string // 媒体文件落盘根目录（对齐 Django MEDIA_ROOT）
	Debug         bool   // 对齐 settings.DEBUG（仅影响调试期的额外响应字段）

	// DBMaxConns 连接池上限。
	//
	// 之前这里什么都没设，直接 pgxpool.New(ctx, url)——pgx 的默认值是
	// max(4, NumCPU)。演示库只有十几条单时每个查询不到 1ms，4 条连接够用，
	// 一切正常；灌到 5 万单之后每个查询要 20ms 上下，4 条连接就成了
	// 整个网关的吞吐上限：4 / 0.02s ≈ 200 QPS，压出来实测 221 req/s，对得上。
	// 症状很有迷惑性——连不碰订单表的 /auth/me 也从 3.5ms 涨到 35ms，
	// 因为它同样要排队等连接。看起来像"整机变慢"，其实是池子太小。
	//
	// 上限不能无脑调大：每个连接在 PG 侧是一个进程，池大小 × 副本数
	// 不能超过 PG 的 max_connections（默认 100），否则新副本上线时
	// 老副本会开始报 "too many clients"。默认 20，按副本数留了余量。
	DBMaxConns int32
	DBMinConns int32 // 常驻连接，避免空闲后第一批请求现场握手

	// AllowSelfRegistration 是否开放自助注册（POST /auth/register）。
	//
	// 默认**关闭**。这是一套 B2B 内部系统：账号该由管理员在「组织与权限 →
	// 员工名录」里开，开出来就带组织归属与角色。自助注册出来的账号
	// organization_id 为 NULL、无角色无权限点——在数据范围语义里"看不见任何东西"，
	// 业务上没有任何用处，却是一个任何人都能自助获得的、已认证的身份。
	// 已认证本身就是攻击面：所有只判「登录了没有」的端点对它敞开。
	//
	// 想开放（对外客户门户自助开户之类）就置 TMS_ALLOW_SELF_REGISTRATION=1。
	AllowSelfRegistration bool

	// MQTT 终端接入。Host 为空即不启用——绝大多数部署没有 broker，
	// 默认连一个不存在的地址只会让日志里滚重连错误。
	MQTTHost     string
	MQTTPort     int
	MQTTTopic    string
	MQTTUsername string
	MQTTPassword string
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() Config {
	origins := strings.Split(env("DJANGO_CORS_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173,http://localhost:4173,http://127.0.0.1:4173"), ",")
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}
	return Config{
		ListenAddr:  env("GO_LISTEN_ADDR", ":8000"),
		DatabaseURL: env("DATABASE_URL", "postgres://tms:tms_dev_pwd@127.0.0.1:5432/tms"),
		SecretKey:   env("DJANGO_SECRET_KEY", "dev-insecure-secret-change-me-min-32-bytes"),
		PublicBase:  env("PUBLIC_BASE_URL", "http://127.0.0.1:8000"),
		CORSOrigins: origins,
		// 令牌有效期是个正经的安全旋钮，.env 模板里也一直写着这两项——
		// 但代码原先写死 30/7，改了不生效。要求更短会话的客户会以为自己配上了。
		AccessMinutes: atoi(env("JWT_ACCESS_MIN", "30"), 30),
		RefreshDays:   atoi(env("JWT_REFRESH_DAYS", "7"), 7),
		MediaRoot:     env("MEDIA_ROOT", "./media"),
		DBMaxConns:    int32(atoi(env("DB_MAX_CONNS", "20"), 20)),
		DBMinConns:    int32(atoi(env("DB_MIN_CONNS", "4"), 4)),
		Debug:         strings.EqualFold(env("DJANGO_DEBUG", "true"), "true"),
		// 默认 "0"：不配就是关。开放注册必须是一个显式动作。
		AllowSelfRegistration: env("TMS_ALLOW_SELF_REGISTRATION", "0") == "1",

		MQTTHost:     env("MQTT_HOST", ""),
		MQTTPort:     atoi(env("MQTT_PORT", "1883"), 1883),
		MQTTTopic:    env("MQTT_TOPIC", "tms/telemetry/#"),
		MQTTUsername: env("MQTT_USERNAME", ""),
		MQTTPassword: env("MQTT_PASSWORD", ""),
	}
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
