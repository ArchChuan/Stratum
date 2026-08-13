// Command model-migrate 一次性把各租户 schema 的 providers/models 迁移到 public 平台目录
// （设计见 docs/superpowers/specs/2026-08-13-model-management-refactor-design.md §10）。
// 只在部署迁移 035 后、tenant_schema 清理落地前运行一次，用完即弃，不留启动路径。
// 默认 dry-run 只打印归并计划与冲突告警；显式 -dry-run=false 才写 public 表。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/observability"
)

const migrateTimeout = 10 * time.Minute

// pgxPool 是迁移逻辑依赖的最小 SQL 接口（*pgxpool.Pool 与 pgxmock 都满足），
// 便于注入 mock 验证 schema 缺失跳过、冲突归并与失败传播路径。
type pgxPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type migrateFunc func(ctx context.Context, pool pgxPool, logger *zap.Logger, dryRun bool) error

func main() {
	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		os.Exit(1)
	}
	exitCode := 0
	if err := run(os.Args[1:], os.Getenv, logger, migrate); err != nil {
		logger.Error("model migrate failed", zap.Error(err))
		exitCode = 1
	}
	_ = logger.Sync()
	os.Exit(exitCode)
}

func run(args []string, getenv func(string) string, logger *zap.Logger, do migrateFunc) error {
	flags := flag.NewFlagSet("model-migrate", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", true, "只预览归并计划，不写 public 表（默认开启）")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	databaseURL := getenv("POSTGRES_URL")
	if databaseURL == "" {
		return errors.New("POSTGRES_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		// 禁止打印 DSN：pgconn 解析错误会带完整 URL（含密码）。
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
	defer cancel()
	if err := do(ctx, pool, logger, *dryRun); err != nil {
		return fmt.Errorf("model migrate: %w", err)
	}
	return nil
}
