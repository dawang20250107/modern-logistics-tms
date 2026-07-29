// Package migrate 内嵌式 SQL 迁移器：Go 侧自有 schema 的唯一来源。
//
// 绞杀期两栈共库、schema 归 Django 的 migrations 管；从这里开始，Go 新增的表与列
// 由本包接管，收官时 Django 表的所有权也一并移交过来（见 PORTING.md 第 5 阶段）。
//
// 刻意做得很小：单表记录已应用版本、单事务逐个执行、只前进不回滚。
// 迁移文件按 NNN_name.sql 命名，内容是纯 SQL，必须可重复执行前的幂等前置检查自理。
package migrate

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var files embed.FS

// Run 应用所有尚未执行的迁移（按文件名字典序）
func Run(ctx context.Context, db *pgxpool.Pool) error {
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS go_schema_migration (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	applied := map[string]bool{}
	rows, err := db.Query(ctx, "SELECT version FROM go_schema_migration")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()

	entries, err := files.ReadDir("sql")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		body, err := files.ReadFile("sql/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO go_schema_migration (version) VALUES ($1)", version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migration %s commit: %w", version, err)
		}
	}
	return nil
}
