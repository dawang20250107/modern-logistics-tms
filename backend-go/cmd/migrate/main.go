// 独立迁移入口：只建 schema、不起服务。
// 部署时可以先跑它再滚动更新网关，也让 CI 能在空库上验证「从零建库」这条路没断。
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/config"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/migrate"
)

func main() {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("连接数据库失败", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := migrate.Run(ctx, pool); err != nil {
		slog.Error("迁移失败", "err", err)
		os.Exit(1)
	}
	slog.Info("迁移完成")
}
