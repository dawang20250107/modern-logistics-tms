// 首个超管的开户入口：Django 退役后，createsuperuser 也一并没了，
// 而一个全新部署必须有办法产生第一个能登录的账号。
//
//	go run ./cmd/adminctl -u admin -p 'Admin12345!'
//
// 已存在同名用户时只重置口令并提权，不重复建号（可重复执行）。
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/config"
)

func main() {
	user := flag.String("u", "", "用户名")
	pass := flag.String("p", "", "口令")
	email := flag.String("email", "", "邮箱（可选）")
	flag.Parse()
	if *user == "" || *pass == "" {
		slog.Error("用法：adminctl -u <用户名> -p <口令> [-email <邮箱>]")
		os.Exit(2)
	}

	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("连接数据库失败", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	hash := auth.MakeDjangoPassword(*pass)
	id, _ := uuid.NewV7()
	var out string
	err = pool.QueryRow(ctx, `
		INSERT INTO accounts_user (id, password, is_superuser, username, first_name, last_name,
		  email, is_staff, is_active, date_joined, nickname, phone, preferences)
		VALUES ($1::uuid, $2, true, $3, '', '', $4, true, true, now(), '', '', '{}'::jsonb)
		ON CONFLICT (username) DO UPDATE SET
		  password = EXCLUDED.password, is_superuser = true, is_staff = true, is_active = true
		RETURNING username`, id.String(), hash, *user, *email).Scan(&out)
	if err != nil {
		slog.Error("开户失败", "err", err)
		os.Exit(1)
	}
	slog.Info("超管已就绪", "username", out)
}
