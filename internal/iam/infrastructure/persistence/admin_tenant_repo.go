package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditpersistence "github.com/byteBuilderX/stratum/internal/audit/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

// AdminTenantRepo persists public.tenants rows for platform-admin flows.
type AdminTenantRepo struct {
	pool pgxPool
}

// NewAdminTenantRepo wires the pool. The pool may be nil in unit tests; methods
// will return a sentinel when called without a backing DB.
func NewAdminTenantRepo(pool pgxPool) *AdminTenantRepo {
	return &AdminTenantRepo{pool: pool}
}

func (r *AdminTenantRepo) Count(ctx context.Context, filter domain.TenantFilter) (int, error) {
	var total int
	if filter.Status != "" {
		err := r.pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM public.tenants WHERE deleted_at IS NULL AND status=$1",
			filter.Status,
		).Scan(&total)
		return total, err
	}
	err := r.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM public.tenants WHERE deleted_at IS NULL",
	).Scan(&total)
	return total, err
}

func (r *AdminTenantRepo) List(ctx context.Context, filter domain.TenantFilter) ([]domain.Tenant, error) {
	offset := (filter.Page - 1) * filter.PageSize
	var (
		rows pgx.Rows
		err  error
	)
	if filter.Status != "" {
		rows, err = r.pool.Query(ctx,
			`SELECT t.id, t.name, t.slug, t.plan, t.status, t.created_at,
			        COUNT(tm.user_id) AS member_count, t.is_default
			 FROM public.tenants t
			 LEFT JOIN public.tenant_members tm ON tm.tenant_id = t.id
			 WHERE t.deleted_at IS NULL AND t.status=$1
			 GROUP BY t.id
			 ORDER BY t.created_at DESC LIMIT $2 OFFSET $3`,
			filter.Status, filter.PageSize, offset)
	} else {
		rows, err = r.pool.Query(ctx,
			`SELECT t.id, t.name, t.slug, t.plan, t.status, t.created_at,
			        COUNT(tm.user_id) AS member_count, t.is_default
			 FROM public.tenants t
			 LEFT JOIN public.tenant_members tm ON tm.tenant_id = t.id
			 WHERE t.deleted_at IS NULL
			 GROUP BY t.id
			 ORDER BY t.created_at DESC LIMIT $1 OFFSET $2`,
			filter.PageSize, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.Tenant, 0)
	for rows.Next() {
		var t domain.Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status, &t.CreatedAt, &t.MemberCount, &t.IsDefault); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *AdminTenantRepo) Get(ctx context.Context, id string) (*domain.Tenant, error) {
	var t domain.Tenant
	err := r.pool.QueryRow(ctx,
		"SELECT id, name, slug, plan, status, created_at, deleted_at FROM public.tenants WHERE id=$1", id,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status, &t.CreatedAt, &t.DeletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTenantNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (r *AdminTenantRepo) Create(ctx context.Context, t domain.Tenant, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("admin create tenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx,
		"INSERT INTO public.tenants(id, name, slug, plan, status, created_at) VALUES($1,$2,$3,$4,$5,$6)",
		t.ID, t.Name, t.Slug, t.Plan, t.Status, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("admin create tenant: %w", err)
	}
	if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("admin create tenant: commit: %w", err)
	}
	return nil
}

func (r *AdminTenantRepo) UpdatePatch(ctx context.Context, id string, patch domain.TenantPatch, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("admin update tenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		"UPDATE public.tenants SET plan=COALESCE(NULLIF($1,''), plan), status=COALESCE(NULLIF($2,''), status) WHERE id=$3 AND deleted_at IS NULL",
		patch.Plan, patch.Status, id,
	)
	if err != nil {
		return fmt.Errorf("admin update tenant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTenantNotFound
	}
	if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("admin update tenant: commit: %w", err)
	}
	return nil
}

func (r *AdminTenantRepo) HardDelete(ctx context.Context, id string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("admin delete tenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var isDefault bool
	err = tx.QueryRow(ctx,
		"SELECT is_default FROM public.tenants WHERE id=$1", id,
	).Scan(&isDefault)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrTenantNotFound
		}
		return err
	}
	if isDefault {
		return domain.ErrDefaultTenantDelete
	}
	tag, err := tx.Exec(ctx, "DELETE FROM public.tenants WHERE id=$1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTenantNotFound
	}
	if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("admin delete tenant: commit: %w", err)
	}
	return nil
}

func (r *AdminTenantRepo) ProvisionSchema(ctx context.Context, tenantID string) error {
	if r.pool == nil {
		return nil
	}
	pool, ok := r.pool.(*pgxpool.Pool)
	if !ok {
		return errors.New("admin_tenant_repo: provision schema requires a real pgx pool")
	}
	return tenantdb.ProvisionTenantSchema(ctx, pool, tenantID)
}

func (r *AdminTenantRepo) ActivateTenant(ctx context.Context, tenantID string) error {
	_, err := r.pool.Exec(ctx, "UPDATE public.tenants SET status='active', updated_at=NOW() WHERE id=$1", tenantID)
	return err
}

func (r *AdminTenantRepo) MarkProvisioningFailed(ctx context.Context, tenantID string) error {
	_, err := r.pool.Exec(ctx, "UPDATE public.tenants SET status='provision_failed', updated_at=NOW() WHERE id=$1", tenantID)
	return err
}
