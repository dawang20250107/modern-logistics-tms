// Package migrate 内嵌式 SQL 迁移器：Go 侧自有 schema 的唯一来源。
//
// Django 退役后，整库 schema 由本包接管：000_baseline.sql 是从并跑期运行库整体
// 快照来的基线，后续变更逐个追加。没有这份基线，删掉 Django 就再也建不出空库。
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
		// 基线约定：*_baseline.sql 只对空库执行。已有业务表的库（并跑期由 Django
		// migrations 建出来的那个）直接记账跳过——重放基线只会撞一堆 already exists，
		// 而它的意义本来就是"让一个空库长出今天这套 schema"。
		if strings.HasSuffix(version, "_baseline") {
			var exists bool
			if err := db.QueryRow(ctx,
				"SELECT to_regclass('public.ops_order') IS NOT NULL").Scan(&exists); err != nil {
				return err
			}
			if exists {
				if _, err := db.Exec(ctx,
					"INSERT INTO go_schema_migration (version) VALUES ($1)", version); err != nil {
					return err
				}
				continue
			}
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
