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
		ListenAddr:    env("GO_LISTEN_ADDR", ":8000"),
		DatabaseURL:   env("DATABASE_URL", "postgres://tms:tms_dev_pwd@127.0.0.1:5432/tms"),
		SecretKey:     env("DJANGO_SECRET_KEY", "dev-insecure-secret-change-me-min-32-bytes"),
		PublicBase:    env("PUBLIC_BASE_URL", "http://127.0.0.1:8000"),
		CORSOrigins:   origins,
		AccessMinutes: 30,
		RefreshDays:   7,
		MediaRoot:     env("MEDIA_ROOT", "./media"),
		Debug:         strings.EqualFold(env("DJANGO_DEBUG", "true"), "true"),

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
