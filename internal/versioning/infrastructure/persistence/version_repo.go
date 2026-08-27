package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/versioning/domain"
	"github.com/byteBuilderX/stratum/internal/versioning/domain/port"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	pkgversioning "github.com/byteBuilderX/stratum/pkg/versioning"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolIface 允许 pgxmock 注入（与 skill persistence 同模式）。
type poolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var _ poolIface = (*pgxpool.Pool)(nil)
var _ port.VersionRepo = (*PgVersionRepo)(nil)

// PgVersionRepo 是通用版本历史基座的只读仓储，仅由 wiring 装配（读端口实现）。
type PgVersionRepo struct{ pool poolIface }

func NewPgVersionRepo(pool *pgxpool.Pool) *PgVersionRepo {
	return &PgVersionRepo{pool: pool}
}

// execTenant 以租户事务执行 fn，search_path 由 pgstore 处理。tenantID 显式传入。
func (r *PgVersionRepo) execTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if tenantID == "" {
		return fmt.Errorf("version_repo: missing tenant id")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tenantID, fn)
}

// ListVersions 返回资源版本历史（新→旧），并推导 is_current（产品表 active_version_id）。
func (r *PgVersionRepo) ListVersions(ctx context.Context, tenantID string, kind domain.ResourceKind, resourceID string) ([]domain.Version, error) {
	var result []domain.Version
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		sql, err := listVersionsSQL(kind)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, sql, string(kind), resourceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			value, err := scanVersion(rows)
			if err != nil {
				return err
			}
			result = append(result, value)
		}
		return rows.Err()
	})
	return result, err
}

// GetVersion 返回指定版本；版本不存在或不属于该资源时 ok=false。
func (r *PgVersionRepo) GetVersion(ctx context.Context, tenantID string, kind domain.ResourceKind, resourceID, versionID string) (domain.Version, bool, error) {
	var result domain.Version
	found := false
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		sql, err := listVersionsSQL(kind)
		if err != nil {
			return err
		}
		value, err := scanVersion(tx.QueryRow(ctx, sql+` AND r.id=$3`, string(kind), resourceID, versionID))
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		result, found = value, true
		return nil
	})
	return result, found, err
}

// listVersionsSQL 用产品表的 active_version_id 子查询推导 is_current。
// kind 未注册产品表时 fail-closed（该资源类型尚未接入版本机制）。
func listVersionsSQL(kind domain.ResourceKind) (string, error) {
	ref, ok := pkgversioning.ProductTableRef(string(kind))
	if !ok {
		return "", fmt.Errorf("version_repo: product table not registered for kind %q: %w", kind, domain.ErrVersionKindUnsupported)
	}
	if !safeIdent(ref.Table) || !safeIdent(ref.ActiveColumn) {
		return "", fmt.Errorf("version_repo: unsafe product table identifier %q.%q", ref.Table, ref.ActiveColumn)
	}
	return fmt.Sprintf(`SELECT r.id, r.resource_kind, r.resource_id, COALESCE(r.parent_version_id, ''),
			COALESCE(r.revision_no, 0), r.status, r.source, r.content_hash, r.payload, r.safe_summary,
			r.created_by, r.created_at, r.published_at,
			COALESCE((SELECT p.%s FROM %s p WHERE p.id = r.resource_id) = r.id, false) AS is_current
		FROM resource_versions r
		WHERE r.resource_kind=$1 AND r.resource_id=$2`, ref.ActiveColumn, ref.Table), nil
}

// safeIdent 校验 SQL 标识符仅含小写字母/数字/下划线，防止产品表映射污染 SQL。
func safeIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// versionScanner 兼容 pgx.Row 与 pgx.Rows。
type versionScanner interface{ Scan(dest ...any) error }

func scanVersion(row versionScanner) (domain.Version, error) {
	var v domain.Version
	var kind, status, source string
	var payloadJSON, summaryJSON []byte
	var publishedAt *time.Time
	dest := []any{
		&v.ID, &kind, &v.ResourceID, &v.ParentVersionID, &v.RevisionNo, &status, &source,
		&v.ContentHash, &payloadJSON, &summaryJSON, &v.CreatedBy, &v.CreatedAt, &publishedAt, &v.IsCurrent,
	}
	if err := row.Scan(dest...); err != nil {
		return domain.Version{}, err
	}
	v.ResourceKind = domain.ResourceKind(kind)
	v.Status = domain.VersionStatus(status)
	v.Source = domain.VersionSource(source)
	v.PublishedAt = publishedAt
	_ = json.Unmarshal(payloadJSON, &v.Payload)
	_ = json.Unmarshal(summaryJSON, &v.SafeSummary)
	return v, nil
}
