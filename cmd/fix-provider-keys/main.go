// Command fix-provider-keys 一次性回填历史明文 provider API key：
// 加密功能（91e87c9f）上线前落库的 providers.api_key 是无前缀明文，
// 读取侧双读兼容放行后，由本命令在部署期（Helm pre-upgrade hook）加密回写，
// 使存量数据收敛为与写路径一致的 enc:v1: 密文。幂等：仅处理非空且无前缀的行。
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

	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

const fixTimeout = 5 * time.Minute

// pgxPool 是回填逻辑依赖的最小 SQL 接口（*pgxpool.Pool 与 pgxmock 都满足），
// 便于注入 mock 验证表缺失跳过与失败传播路径。
type pgxPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type fixFunc func(ctx context.Context, pool pgxPool, key [32]byte, logger *zap.Logger, dryRun bool) error

func main() {
	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		os.Exit(1)
	}
	exitCode := 0
	if err := run(os.Args[1:], os.Getenv, logger, fixProviderKeys); err != nil {
		logger.Error("fix provider keys failed", zap.Error(err))
		exitCode = 1
	}
	_ = logger.Sync()
	os.Exit(exitCode)
}

func run(args []string, getenv func(string) string, logger *zap.Logger, fix fixFunc) error {
	flags := flag.NewFlagSet("fix-provider-keys", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "只预览待回填的明文 key，不执行写入")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	databaseURL := getenv("POSTGRES_URL")
	if databaseURL == "" {
		return errors.New("POSTGRES_URL is required")
	}
	key, err := pkgcrypto.ResolveDataKey(getenv("DATA_ENCRYPTION_KEY"), getenv("JWT_PRIVATE_KEY_PEM"))
	if err != nil {
		return fmt.Errorf("resolve data encryption key: %w", err)
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		// 禁止打印 DSN：pgconn 解析错误会带完整 URL（含密码）。
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), fixTimeout)
	defer cancel()
	if err := fix(ctx, pool, key, logger, *dryRun); err != nil {
		return fmt.Errorf("fix provider keys: %w", err)
	}
	return nil
}

// fixProviderKeys 将 public.providers 表中无前缀的历史明文 api key 加密回写。
// providers 已提升为 public 平台全局目录（035 迁移），单次扫描即可覆盖全部行，
// 不再按租户 schema 遍历。public.providers 缺失视为全新环境（正常返回）；
// 其余 DB 错误立即失败退出，防止静默漏修。日志只含 provider id，绝不输出 key 明文。
func fixProviderKeys(ctx context.Context, pool pgxPool, key [32]byte, logger *zap.Logger, dryRun bool) error {
	rows, err := pool.Query(ctx,
		`SELECT id, api_key FROM public.providers WHERE api_key <> '' AND api_key NOT LIKE 'enc:v1:%'`)
	if err != nil {
		if isRelationMissing(err) {
			logger.Info("public.providers does not exist, treat as nothing to fix")
			return nil
		}
		return fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	type pending struct{ id, plain string }
	var pendings []pending
	for rows.Next() {
		var id, plain string
		if err := rows.Scan(&id, &plain); err != nil {
			return fmt.Errorf("scan provider: %w", err)
		}
		pendings = append(pendings, pending{id: id, plain: plain})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list providers: %w", err)
	}

	fixed := 0
	for _, p := range pendings {
		if dryRun {
			logger.Info("provider key would be fixed", zap.String("provider_id", p.id))
			fixed++
			continue
		}
		ct, err := pkgcrypto.EncryptSecret(key, p.plain)
		if err != nil {
			return fmt.Errorf("encrypt provider %s: %w", p.id, err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE public.providers SET api_key=$1, updated_at=now() WHERE id=$2`,
			ct, p.id); err != nil {
			return fmt.Errorf("update provider %s: %w", p.id, err)
		}
		logger.Info("provider key fixed", zap.String("provider_id", p.id))
		fixed++
	}
	logger.Info("fix-provider-keys summary",
		zap.Int("fixed", fixed),
		zap.Bool("dry_run", dryRun))
	return nil
}

// pgCodeUndefinedTable 是 PostgreSQL 的 SQLSTATE 42P01（relation does not exist）。
// pgconn 未导出对应常量，这里显式声明供 isRelationMissing 使用。
const pgCodeUndefinedTable = "42P01"

// isRelationMissing 报告 err 是否为 relation does not exist（SQLSTATE 42P01），
// 用于区分"schema/表未 provision"（合法跳过）与真实 DB 故障（必须中止）。
func isRelationMissing(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeUndefinedTable
}
