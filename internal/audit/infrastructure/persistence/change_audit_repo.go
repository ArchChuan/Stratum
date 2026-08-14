package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// changeAuditPoolIface 是事务起点抽象，生产用 *pgxpool.Pool，测试注入
// pgxmock。注意与 audit_repo.go 的 poolIface（Query/Exec 直连版）不同名。
type changeAuditPoolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var _ changeAuditPoolIface = (*pgxpool.Pool)(nil)

// PgResourceChangeAuditRepo 在 tenant schema 内读取 resource_change_audits。
// 每个方法都要求非空 tenantID：为空时在触碰连接池之前 fail closed
// （跨租户泄露面防御；禁止复制旧 buildAuditFilter 的「空租户省略谓词」模式）。
type PgResourceChangeAuditRepo struct {
	pool changeAuditPoolIface
}

func NewPgResourceChangeAuditRepo(pool *pgxpool.Pool) *PgResourceChangeAuditRepo {
	return &PgResourceChangeAuditRepo{pool: pool}
}

func (r *PgResourceChangeAuditRepo) List(
	ctx context.Context,
	tenantID string,
	f port.ResourceChangeAuditFilter,
) ([]port.ResourceChangeAuditRow, int, error) {
	if tenantID == "" {
		return nil, 0, fmt.Errorf("audit: list resource change audits: tenant id required")
	}
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	rows := make([]port.ResourceChangeAuditRow, 0)
	var total int
	err := postgres.ExecTenantWith(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		where, args := buildChangeAuditWhere(tenantID, f)
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM resource_change_audits r `+where, args...).Scan(&total); err != nil {
			return fmt.Errorf("audit: count resource change audits: %w", err)
		}
		if total == 0 {
			return nil
		}
		paging := make([]any, 0, len(args)+2)
		paging = append(paging, args...)
		paging = append(paging, f.Limit, f.Offset)
		rowsQuery := `SELECT id, resource_kind, resource_id, operation, actor_id, created_at,
			before_projection, after_projection
			FROM resource_change_audits r ` + where + fmt.Sprintf(` ORDER BY created_at DESC, id DESC
			LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
		got, err := tx.Query(ctx, rowsQuery, paging...)
		if err != nil {
			return fmt.Errorf("audit: query resource change audits: %w", err)
		}
		defer got.Close()
		actorIDs := make([]string, 0, 8)
		for got.Next() {
			var row port.ResourceChangeAuditRow
			var before, after []byte
			if err := got.Scan(&row.ID, &row.ResourceKind, &row.ResourceID, &row.Operation,
				&row.ActorID, &row.CreatedAt, &before, &after); err != nil {
				return fmt.Errorf("audit: scan resource change audit: %w", err)
			}
			row.Before = json.RawMessage(before)
			row.After = json.RawMessage(after)
			rows = append(rows, row)
			actorIDs = append(actorIDs, row.ActorID)
		}
		if err := got.Err(); err != nil {
			return fmt.Errorf("audit: iterate resource change audits: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		names, err := loadActorNames(ctx, tx, actorIDs)
		if err != nil {
			return err
		}
		for i := range rows {
			rows[i].ActorName = actorDisplayName(rows[i].ActorID, names)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *PgResourceChangeAuditRepo) GetByID(
	ctx context.Context,
	tenantID, id string,
) (*port.ResourceChangeAuditRow, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("audit: get resource change audit: tenant id required")
	}
	var row port.ResourceChangeAuditRow
	err := postgres.ExecTenantWith(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before, after []byte
		if err := tx.QueryRow(ctx, `SELECT id, resource_kind, resource_id, operation, actor_id,
			created_at, before_projection, after_projection
			FROM resource_change_audits WHERE tenant_id = $1 AND id = $2`, tenantID, id).
			Scan(&row.ID, &row.ResourceKind, &row.ResourceID, &row.Operation, &row.ActorID,
				&row.CreatedAt, &before, &after); err != nil {
			return err
		}
		row.Before = json.RawMessage(before)
		row.After = json.RawMessage(after)
		names, err := loadActorNames(ctx, tx, []string{row.ActorID})
		if err != nil {
			return err
		}
		row.ActorName = actorDisplayName(row.ActorID, names)
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("audit: get resource change audit: %w", err)
	}
	return &row, nil
}

// buildChangeAuditWhere 构造带占位符的 WHERE。tenant_id 谓词恒存在（$1）。
// actor_name 子串匹配：命中 public.users.display_name / github_login，或
// actor_id 原文（覆盖 system actor 如 evaluation-worker）。返回的 where 以
// "WHERE " 开头，可直接拼接在表名后；表别名恒为 r（子查询引用 r.actor_id）。
func buildChangeAuditWhere(tenantID string, f port.ResourceChangeAuditFilter) (string, []any) {
	conds := []string{`tenant_id = $1`}
	args := []any{tenantID}
	if f.ResourceKind != "" {
		args = append(args, f.ResourceKind)
		conds = append(conds, fmt.Sprintf(`resource_kind = $%d`, len(args)))
	}
	if f.ActorName != "" {
		args = append(args, `%`+f.ActorName+`%`)
		idx := len(args)
		conds = append(conds, fmt.Sprintf(
			`(EXISTS (SELECT 1 FROM public.users u WHERE u.id::text = r.actor_id AND (u.display_name ILIKE $%[1]d OR u.github_login ILIKE $%[1]d)) OR r.actor_id ILIKE $%[1]d)`, idx))
	}
	if f.From != nil {
		args = append(args, *f.From)
		conds = append(conds, fmt.Sprintf(`created_at >= $%d`, len(args)))
	}
	if f.To != nil {
		args = append(args, *f.To)
		conds = append(conds, fmt.Sprintf(`created_at <= $%d`, len(args)))
	}
	return `WHERE ` + strings.Join(conds, ` AND `), args
}

// actorNameRow 是 public.users 批量映射的中间载体。
type actorNameRow struct {
	DisplayName string
	GitHubLogin string
}

// loadActorNames 批量取分页行 actor 的 display_name/github_login。
// schema-qualified public.users（execTenant 内 search_path 含 public，显式限定
// 防未来 shadow）；只取两列，不返回 email。
func loadActorNames(
	ctx context.Context,
	tx pgx.Tx,
	actorIDs []string,
) (map[string]actorNameRow, error) {
	names := make(map[string]actorNameRow, len(actorIDs))
	rows, err := tx.Query(ctx, `SELECT id, COALESCE(display_name,''), COALESCE(github_login,'')
		FROM public.users WHERE id = ANY($1)`, actorIDs)
	if err != nil {
		return nil, fmt.Errorf("audit: load actor names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n actorNameRow
		if err := rows.Scan(&id, &n.DisplayName, &n.GitHubLogin); err != nil {
			return nil, fmt.Errorf("audit: scan actor name: %w", err)
		}
		names[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: iterate actor names: %w", err)
	}
	return names, nil
}

// actorDisplayName 按 display_name > github_login > actor_id 原文兜底。
// system actor（evaluation-worker 等）无对应 users 行，直接展示 actor_id。
func actorDisplayName(actorID string, names map[string]actorNameRow) string {
	n, ok := names[actorID]
	if !ok {
		return actorID
	}
	if n.DisplayName != "" {
		return n.DisplayName
	}
	if n.GitHubLogin != "" {
		return n.GitHubLogin
	}
	return actorID
}
