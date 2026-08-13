// Command cleanup-platform-bindings 解除普通 agent 对系统内置资源的挂载：
// builtin skill（id 前缀 builtin:）、platform-managed 知识库与 MCP server
// （management_mode = 'platform_managed' 或 system_key 非空）。系统助手
// （system_key = 'stratum.platform_assistant'）的绑定一律保留。
//
// 默认 --dry-run 只预览待解绑的绑定；加 --apply 才执行 DELETE。命令幂等，
// 重复执行第二次无行可删。部署顺序硬前提：必须在写路径校验（A1/A2/B）上线
// 前对存量租户执行本命令，否则存量 platform 绑定会被新校验 409 锁死更新。
// 数据库错误立即失败退出，禁止静默漏清；tenant schema 缺失视为该租户无需
// 清理而跳过（全新环境）。
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

const cleanupTimeout = 5 * time.Minute

// pgxPool 是清理逻辑依赖的最小 SQL 接口（*pgxpool.Pool 与 pgxmock 都满足），
// 便于注入 mock 验证表缺失跳过与失败传播路径。
type pgxPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type cleanupFunc func(ctx context.Context, pool pgxPool, logger *zap.Logger, apply bool) error

func main() {
	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		os.Exit(1)
	}
	exitCode := 0
	if err := run(os.Args[1:], os.Getenv, logger, cleanupPlatformBindings); err != nil {
		logger.Error("cleanup platform bindings failed", zap.Error(err))
		exitCode = 1
	}
	_ = logger.Sync()
	os.Exit(exitCode)
}

func run(args []string, getenv func(string) string, logger *zap.Logger, cleanup cleanupFunc) error {
	flags := flag.NewFlagSet("cleanup-platform-bindings", flag.ContinueOnError)
	apply := flags.Bool("apply", false, "执行 DELETE 解绑；默认 dry-run 只预览")
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

	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := cleanup(ctx, pool, logger, *apply); err != nil {
		return fmt.Errorf("cleanup platform bindings: %w", err)
	}
	return nil
}

// cleanupCounts 汇总单个租户三条绑定清理的受影响行数。
type cleanupCounts struct{ skills, workspaces, mcp int }

// cleanupPlatformBindings 遍历全部存量租户，逐租户解除普通 agent 对平台
// 资源的绑定。public.tenants 缺失视为 0 租户（全新环境）；租户 schema 缺失
// 跳过该租户；其余 DB 错误立即失败退出。
func cleanupPlatformBindings(ctx context.Context, pool pgxPool, logger *zap.Logger, apply bool) error {
	rows, err := pool.Query(ctx, `SELECT id FROM public.tenants WHERE deleted_at IS NULL`)
	if err != nil {
		if isRelationMissing(err) {
			logger.Info("public.tenants does not exist, treat as zero tenants")
			return nil
		}
		return fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()

	var tenantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan tenant: %w", err)
		}
		tenantIDs = append(tenantIDs, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list tenants: %w", err)
	}

	var total cleanupCounts
	skipped := 0
	for _, tenantID := range tenantIDs {
		counts, skip, err := cleanupTenant(ctx, pool, tenantID, logger, apply)
		if err != nil {
			return err // 非表缺失错误必须中止
		}
		if skip > 0 {
			skipped++
			continue // 未 provision 租户，counts 无意义，不累加
		}
		total.skills += counts.skills
		total.workspaces += counts.workspaces
		total.mcp += counts.mcp
	}
	logger.Info("cleanup-platform-bindings summary",
		zap.Int("tenants", len(tenantIDs)),
		zap.Int("skills", total.skills),
		zap.Int("workspaces", total.workspaces),
		zap.Int("mcp", total.mcp),
		zap.Int("skipped_tenants", skipped),
		zap.Bool("apply", apply))
	return nil
}

// cleanupTenant 清理单个租户 schema 内普通 agent 对 builtin skill /
// platform workspace / platform MCP server 的绑定，返回三类受影响行数与
// 是否因 schema 未 provision 而整体跳过。
func cleanupTenant(ctx context.Context, pool pgxPool, tenantID string, logger *zap.Logger, apply bool) (cleanupCounts, int, error) {
	schema := `"tenant_` + tenantID + `"`
	var total cleanupCounts
	var err error
	// 任一绑定表缺失（42P01）即判定该租户 schema 未 provision，跳过剩余清理。
	if total.skills, err = cleanupBinding(ctx, pool, tenantID, logger, apply, "skill",
		`SELECT l.agent_id, l.skill_id AS resource_id FROM `+schema+`.agent_skill_links AS l WHERE `+skillBindWhere(schema),
		`DELETE FROM `+schema+`.agent_skill_links AS l WHERE `+skillBindWhere(schema)); err != nil {
		return total, 0, err
	}
	if total.skills < 0 {
		return total, 1, nil
	}
	if total.workspaces, err = cleanupBinding(ctx, pool, tenantID, logger, apply, "workspace",
		`SELECT l.agent_id, l.workspace_id::text AS resource_id FROM `+schema+`.agent_workspaces AS l WHERE `+workspaceBindWhere(schema),
		`DELETE FROM `+schema+`.agent_workspaces AS l WHERE `+workspaceBindWhere(schema)); err != nil {
		return total, 0, err
	}
	if total.workspaces < 0 {
		return total, 1, nil
	}
	if total.mcp, err = cleanupBinding(ctx, pool, tenantID, logger, apply, "mcp",
		`SELECT l.agent_id, l.server_id AS resource_id FROM `+schema+`.agent_mcp_tool_links AS l WHERE `+mcpBindWhere(schema),
		`DELETE FROM `+schema+`.agent_mcp_tool_links AS l WHERE `+mcpBindWhere(schema)); err != nil {
		return total, 0, err
	}
	if total.mcp < 0 {
		return total, 1, nil
	}
	return total, 0, nil
}

// cleanupBinding 对单条绑定清理执行 dry-run（SELECT 预览）或 apply（DELETE），
// 返回受影响/待删行数。表缺失（未 provision）时返回 -1 表示跳过该租户。
func cleanupBinding(ctx context.Context, pool pgxPool, tenantID string, logger *zap.Logger, apply bool, kind, previewSQL, deleteSQL string) (int, error) {
	if !apply {
		return previewBindings(ctx, pool, tenantID, logger, kind, previewSQL)
	}
	return deleteBindings(ctx, pool, tenantID, logger, kind, deleteSQL)
}

// previewBindings dry-run 预览待解绑的绑定并返回行数；表缺失视为未 provision。
func previewBindings(ctx context.Context, pool pgxPool, tenantID string, logger *zap.Logger, kind, previewSQL string) (int, error) {
	rows, err := pool.Query(ctx, previewSQL)
	if err != nil {
		if isRelationMissing(err) {
			logger.Info("tenant schema not provisioned, skip", zap.String("tenant_id", tenantID))
			return -1, nil
		}
		return 0, fmt.Errorf("preview %s bindings: %w", kind, err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var agentID, resourceID string
		if err := rows.Scan(&agentID, &resourceID); err != nil {
			return 0, fmt.Errorf("scan %s binding: %w", kind, err)
		}
		logger.Info("binding would be unbound",
			zap.String("kind", kind), zap.String("tenant_id", tenantID),
			zap.String("agent_id", agentID), zap.String("resource_id", resourceID))
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("preview %s bindings: %w", kind, err)
	}
	return n, nil
}

// deleteBindings apply 模式执行 DELETE 并返回受影响行数；表缺失视为未 provision。
func deleteBindings(ctx context.Context, pool pgxPool, tenantID string, logger *zap.Logger, kind, deleteSQL string) (int, error) {
	ct, err := pool.Exec(ctx, deleteSQL)
	if err != nil {
		if isRelationMissing(err) {
			logger.Info("tenant schema not provisioned, skip", zap.String("tenant_id", tenantID))
			return -1, nil
		}
		return 0, fmt.Errorf("delete %s bindings: %w", kind, err)
	}
	n := int(ct.RowsAffected())
	logger.Info("bindings unbound",
		zap.String("kind", kind), zap.String("tenant_id", tenantID), zap.Int("count", n))
	return n, nil
}

// 三条绑定清理各自的平台资源过滤谓词。NOT EXISTS 保护系统助手
// （system_key='stratum.platform_assistant'）的绑定不被清理。
func skillBindWhere(schema string) string {
	return `l.skill_id LIKE 'builtin:%'` +
		` AND NOT EXISTS (SELECT 1 FROM ` + schema + `.agents sa WHERE sa.id = l.agent_id AND sa.system_key = 'stratum.platform_assistant')`
}

func workspaceBindWhere(schema string) string {
	return `l.workspace_id IN (SELECT id FROM ` + schema + `.rag_workspaces WHERE management_mode = 'platform_managed')` +
		` AND NOT EXISTS (SELECT 1 FROM ` + schema + `.agents sa WHERE sa.id = l.agent_id AND sa.system_key = 'stratum.platform_assistant')`
}

func mcpBindWhere(schema string) string {
	// 谓词与 mcp_service.isPlatformManaged 对齐：system_key 非空或
	// management_mode = 'platform_managed' 即为平台 MCP。
	return `l.server_id IN (SELECT id FROM ` + schema + `.mcp_configs WHERE system_key IS NOT NULL OR management_mode = 'platform_managed')` +
		` AND NOT EXISTS (SELECT 1 FROM ` + schema + `.agents sa WHERE sa.id = l.agent_id AND sa.system_key = 'stratum.platform_assistant')`
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
