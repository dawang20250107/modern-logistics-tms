// Package config 集中环境配置：与 Django 侧同一套环境变量语义，便于并跑期共用部署。
package config

import (
	"os"
	"strings"
)

type Config struct {
	ListenAddr     string // Go 网关监听地址（对外入口）
	DatabaseURL    string // PostgreSQL 连接串（与 Django 共库）
	SecretKey      string // 必须与 DJANGO_SECRET_KEY 一致：签发/校验 simplejwt 兼容 token
	DjangoUpstream string // 未移植路由的反向代理上游（绞杀者模式）
	CORSOrigins    []string
	AccessMinutes  int // 与 SIMPLE_JWT.ACCESS_TOKEN_LIFETIME 对齐
	RefreshDays    int // 与 SIMPLE_JWT.REFRESH_TOKEN_LIFETIME 对齐
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
		ListenAddr:     env("GO_LISTEN_ADDR", ":8000"),
		DatabaseURL:    env("DATABASE_URL", "postgres://tms:tms_dev_pwd@127.0.0.1:5432/tms"),
		SecretKey:      env("DJANGO_SECRET_KEY", "dev-insecure-secret-change-me-min-32-bytes"),
		DjangoUpstream: env("DJANGO_UPSTREAM", "http://127.0.0.1:8001"),
		CORSOrigins:    origins,
		AccessMinutes:  30,
		RefreshDays:    7,
	}
}
