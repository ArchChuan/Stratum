package infrastructure

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

// PgProviderRepo implements port.ProviderRepository backed by PostgreSQL.
type PgProviderRepo struct {
	pool *pgxpool.Pool
}

// NewPgProviderRepo returns a new PgProviderRepo.
func NewPgProviderRepo(pool *pgxpool.Pool) *PgProviderRepo {
	return &PgProviderRepo{pool: pool}
}

func (r *PgProviderRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, fn)
}

// Create inserts a new provider row and populates DB-generated timestamps on p.
func (r *PgProviderRepo) Create(ctx context.Context, tenantID string, p *domain.Provider) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO providers (id, tenant_id, name, kind, base_url, api_key, default_model, enabled)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			 RETURNING created_at, updated_at`,
			p.ID, tenantID, p.Name, string(p.Kind), p.BaseURL, p.APIKey, p.DefaultModel, p.Enabled,
		).Scan(&p.CreatedAt, &p.UpdatedAt)
	})
}

// Get retrieves a single provider by ID.
func (r *PgProviderRepo) Get(ctx context.Context, tenantID, id string) (*domain.Provider, error) {
	var p domain.Provider
	var kind string
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, tenant_id, name, kind, base_url, api_key, default_model, enabled, created_at, updated_at
			 FROM providers WHERE id=$1`, id,
		).Scan(&p.ID, &p.TenantID, &p.Name, &kind, &p.BaseURL, &p.APIKey, &p.DefaultModel, &p.Enabled,
			&p.CreatedAt, &p.UpdatedAt)
	})
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}
	p.Kind = domain.ProviderKind(kind)
	return &p, nil
}

// List returns all providers for a tenant, ordered by creation time.
func (r *PgProviderRepo) List(ctx context.Context, tenantID string) ([]domain.Provider, error) {
	var out []domain.Provider
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, tenant_id, name, kind, base_url, api_key, default_model, enabled, created_at, updated_at
			 FROM providers ORDER BY created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p domain.Provider
			var kind string
			if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &kind, &p.BaseURL, &p.APIKey,
				&p.DefaultModel, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
				return err
			}
			p.Kind = domain.ProviderKind(kind)
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	if out == nil {
		out = []domain.Provider{}
	}
	return out, nil
}

// Update modifies an existing provider. Returns an error if the provider is not found.
func (r *PgProviderRepo) Update(ctx context.Context, tenantID string, p *domain.Provider) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE providers SET name=$1, kind=$2, base_url=$3, api_key=$4, default_model=$5, enabled=$6, updated_at=now()
			 WHERE id=$7 AND tenant_id=$8`,
			p.Name, string(p.Kind), p.BaseURL, p.APIKey, p.DefaultModel, p.Enabled, p.ID, tenantID,
		)
		if err != nil {
			return fmt.Errorf("update provider: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("provider not found: %s", p.ID)
		}
		return nil
	})
}

// Delete removes a provider by ID.
func (r *PgProviderRepo) Delete(ctx context.Context, tenantID, id string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM providers WHERE id=$1 AND tenant_id=$2`, id, tenantID)
		if err != nil {
			return fmt.Errorf("delete provider: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("provider not found: %s", id)
		}
		return nil
	})
}
