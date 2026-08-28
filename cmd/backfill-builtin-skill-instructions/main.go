// Command backfill-builtin-skill-instructions 一次性把存量租户的 4 个内置 skill
// 更新为当前种子内容(全量覆盖)。
//
// 背景:内置 skill 指令本轮起做厚并锚定内置工具(stratum_search_official_docs /
// stratum_diagnose_tenant / stratum_propose_resource_change 等);新租户 provision
// 时 tenant_schema.sql 的 seed 已是新内容,存量租户仍保留旧薄指令,需本工具显式回填。
// 产品决策:存量租户也一并全量覆盖——即使租户曾编辑内置 skill(存在更高 revision),
// active revision 也重置回种子 revision(rev-builtin-*-v1);被覆盖的旧 revision 行
// 保留在 skill_revisions(不再 active),不删除。
//
// 内容源直接复用 internal/skill/infrastructure/seeds.BuiltinSkills(),与种子 SQL
// 同源,避免脚本硬编码漂移。默认 dry-run 只列将更新的租户×skill;显式 -execute
// 才写入。任何租户失败必须传播,禁止静默跳过。幂等:active content_hash 与种子
// 一致即跳过,重复执行无副作用。
//
// 用法:DATABASE_URL=postgres://... go run ./cmd/backfill-builtin-skill-instructions [-execute]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/seeds"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

const backfillTimeout = 10 * time.Minute

func main() {
	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		os.Exit(1)
	}
	if err := run(os.Args[1:], os.Getenv, logger); err != nil {
		logger.Error("backfill builtin skill instructions failed", zap.Error(err))
		_ = logger.Sync()
		os.Exit(1)
	}
	_ = logger.Sync()
}

func run(args []string, getenv func(string) string, logger *zap.Logger) error {
	flags := flag.NewFlagSet("backfill-builtin-skill-instructions", flag.ContinueOnError)
	execute := flags.Bool("execute", false, "真写存量租户 skill 内容(默认 dry-run 只列将更新的租户×skill)")
	tenantID := flags.String("tenant", "", "仅回填该 tenant id(schema tenant_<id>);不填则回填全部")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), backfillTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()
	if _, err := backfillAll(ctx, poolDB{pool: pool}, seeds.BuiltinSkills(), *execute, *tenantID, logger); err != nil {
		return err
	}
	return nil
}

// db 抽象回填所需的 DB 原语,便于测试注入 stub。pgxpool.Pool 经 poolDB 适配。
type db interface {
	TenantSchemas(ctx context.Context) ([]string, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// poolDB 把 pgxpool.Pool 适配为 db 接口。
type poolDB struct {
	pool *pgxpool.Pool
}

var _ db = poolDB{}

// TenantSchemas 枚举全部非 deleted 租户的 tenant schema 名(tenant_<uuid>,含连字符)。
func (p poolDB) TenantSchemas(ctx context.Context) ([]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT 'tenant_'||id FROM public.tenants WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()
	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("scan tenant schema: %w", err)
		}
		schemas = append(schemas, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tenants: %w", err)
	}
	return schemas, nil
}

func (p poolDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}

func (p poolDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}

// backfillAll 逐租户回填并汇总;任一租户失败即整体返回,禁止静默跳过。
// tenantFilter 非空时只处理 tenant_<filter> schema(试点/生产灰度用)。返回将更新
// (execute 时为已更新)的 skill 总数。
func backfillAll(ctx context.Context, d db, skills []seeds.BuiltinSkill, execute bool, tenantFilter string, logger *zap.Logger) (int, error) {
	schemas, err := d.TenantSchemas(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, schema := range schemas {
		if tenantFilter != "" && schema != "tenant_"+tenantFilter {
			continue
		}
		n, err := backfillTenant(ctx, d, schema, skills, execute)
		if err != nil {
			return 0, fmt.Errorf("backfill %s: %w", schema, err)
		}
		if n > 0 {
			verb := "backfilled"
			if !execute {
				verb = "would backfill"
			}
			logger.Info(verb, zap.String("schema", schema), zap.Int("skills", n))
			total += n
		}
	}
	logger.Info("backfill done", zap.Int("tenants", len(schemas)), zap.Int("skills_changed", total), zap.Bool("dry_run", !execute))
	return total, nil
}

// backfillTenant 对单个租户回填其内容已过期的内置 skill;返回需要(且 execute 时已)更新的数量。
func backfillTenant(ctx context.Context, d db, schema string, skills []seeds.BuiltinSkill, execute bool) (int, error) {
	changed := 0
	for _, sk := range skills {
		need, err := needsUpdate(ctx, d, schema, sk)
		if err != nil {
			return 0, err
		}
		if !need {
			continue
		}
		if execute {
			if err := applyUpdates(ctx, d, schema, sk); err != nil {
				return 0, err
			}
		}
		changed++
	}
	return changed, nil
}

// needsUpdate 判断该租户当前 active revision 的 content_hash 是否与种子一致。
// 种子 ON CONFLICT DO NOTHING 保证 skill/v1 revision 行存在;行缺失(NoRows)视为
// 数据异常,仍需回填——execute 时 UPDATE 影响行数 0 会进一步暴露。
func needsUpdate(ctx context.Context, d db, schema string, sk seeds.BuiltinSkill) (bool, error) {
	var curHash string
	q := fmt.Sprintf(
		`SELECT sr.content_hash FROM %s sr JOIN %s s ON s.active_revision_id = sr.id WHERE s.id = $1`,
		tableName(schema, "skill_revisions"), tableName(schema, "skills"),
	)
	err := d.QueryRow(ctx, q, sk.ID).Scan(&curHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("query active content_hash for %s: %w", sk.ID, err)
	}
	return curHash != sk.Revision.ContentHash, nil
}

// applyUpdates 全量覆盖该租户的 skills 行与种子 skill_revisions(v1)行。
func applyUpdates(ctx context.Context, d db, schema string, sk seeds.BuiltinSkill) error {
	rev := sk.Revision
	checks, err := compactJSON(rev.PublishChecks)
	if err != nil {
		return fmt.Errorf("marshal publish_checks %s: %w", sk.ID, err)
	}
	tag, err := d.Exec(ctx, fmt.Sprintf(
		`UPDATE %s SET name=$1, description=$2, status='published', active_revision_id=$3, updated_at=NOW() WHERE id=$4`,
		tableName(schema, "skills"),
	), sk.Name, sk.Description, rev.ID, sk.ID)
	if err != nil {
		return fmt.Errorf("update skills %s: %w", sk.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update skills %s: row not found in schema %s", sk.ID, schema)
	}
	tag, err = d.Exec(ctx, fmt.Sprintf(
		`UPDATE %s SET name=$1, description=$2, instructions=$3, content_hash=$4, publish_checks=$5::jsonb, updated_at=NOW() WHERE id=$6 AND skill_id=$7`,
		tableName(schema, "skill_revisions"),
	), rev.Name, rev.Description, rev.Instructions, rev.ContentHash, checks, rev.ID, sk.ID)
	if err != nil {
		return fmt.Errorf("update skill_revisions %s: %w", sk.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update skill_revisions %s: revision row not found in schema %s", sk.ID, schema)
	}
	return nil
}

// tableName 构造 schema-qualified 标识符;tenant schema 名含连字符,必须引号包裹。
func tableName(schema, table string) string {
	return pgx.Identifier{schema, table}.Sanitize()
}

func compactJSON(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
