// Package persistence provides PostgreSQL adapters for the prompt context.
// Prompt templates and bindings live in the public schema because they are
// a platform-level concern (shared across tenants, with optional tenant scope).
package persistence

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/prompt/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgPromptRepo persists prompt templates in public.prompt_templates.
type PgPromptRepo struct {
	pool *pgxpool.Pool
}

// NewPgPromptRepo constructs a PostgreSQL-backed prompt repository.
func NewPgPromptRepo(pool *pgxpool.Pool) *PgPromptRepo {
	return &PgPromptRepo{pool: pool}
}

// Insert stores a new prompt template version.
func (r *PgPromptRepo) Insert(ctx context.Context, tmpl domain.PromptTemplate) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO public.prompt_templates (key, tenant_id, version, content, status, content_hash, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		tmpl.Key, tmpl.TenantID, tmpl.Version, tmpl.Content,
		string(tmpl.Status), tmpl.ContentHash, tmpl.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("prompt: insert: %w", err)
	}
	return nil
}

// GetByKey returns all versions for a key+tenant pair, newest first.
func (r *PgPromptRepo) GetByKey(ctx context.Context, key string, tenantID *string) ([]domain.PromptTemplate, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, tenant_id, version, content, status, content_hash, created_by, created_at
		 FROM public.prompt_templates
		 WHERE key = $1 AND tenant_id IS NOT DISTINCT FROM $2
		 ORDER BY version DESC`, key, tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("prompt: get by key: %w", err)
	}
	defer rows.Close()
	return scanPrompts(rows)
}

// GetVersion returns a specific version of a prompt template.
func (r *PgPromptRepo) GetVersion(ctx context.Context, key string, version int, tenantID *string) (*domain.PromptTemplate, error) {
	var tmpl domain.PromptTemplate
	var status string
	err := r.pool.QueryRow(ctx,
		`SELECT key, tenant_id, version, content, status, content_hash, created_by, created_at
		 FROM public.prompt_templates
		 WHERE key = $1 AND version = $2 AND tenant_id IS NOT DISTINCT FROM $3`,
		key, version, tenantID,
	).Scan(&tmpl.Key, &tmpl.TenantID, &tmpl.Version, &tmpl.Content,
		&status, &tmpl.ContentHash, &tmpl.CreatedBy, &tmpl.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("prompt: get version: %w", err)
	}
	tmpl.Status = domain.PromptStatus(status)
	return &tmpl, nil
}

// GetLatestPublished returns the most recent published version for a key+tenant pair.
func (r *PgPromptRepo) GetLatestPublished(ctx context.Context, key string, tenantID *string) (*domain.PromptTemplate, error) {
	var tmpl domain.PromptTemplate
	var status string
	err := r.pool.QueryRow(ctx,
		`SELECT key, tenant_id, version, content, status, content_hash, created_by, created_at
		 FROM public.prompt_templates
		 WHERE key = $1 AND tenant_id IS NOT DISTINCT FROM $2 AND status = 'published'
		 ORDER BY version DESC LIMIT 1`,
		key, tenantID,
	).Scan(&tmpl.Key, &tmpl.TenantID, &tmpl.Version, &tmpl.Content,
		&status, &tmpl.ContentHash, &tmpl.CreatedBy, &tmpl.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("prompt: get latest published: %w", err)
	}
	tmpl.Status = domain.PromptStatus(status)
	return &tmpl, nil
}

// UpdateStatus changes the lifecycle status of a specific version.
func (r *PgPromptRepo) UpdateStatus(ctx context.Context, key string, version int, tenantID *string, status domain.PromptStatus) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE public.prompt_templates SET status = $1
		 WHERE key = $2 AND version = $3 AND tenant_id IS NOT DISTINCT FROM $4`,
		string(status), key, version, tenantID,
	)
	if err != nil {
		return fmt.Errorf("prompt: update status: %w", err)
	}
	return nil
}

// GetByHash looks up a prompt template by its content hash.
func (r *PgPromptRepo) GetByHash(ctx context.Context, hash string) (*domain.PromptTemplate, error) {
	var tmpl domain.PromptTemplate
	var status string
	err := r.pool.QueryRow(ctx,
		`SELECT key, tenant_id, version, content, status, content_hash, created_by, created_at
		 FROM public.prompt_templates
		 WHERE content_hash = $1
		 ORDER BY version DESC LIMIT 1`,
		hash,
	).Scan(&tmpl.Key, &tmpl.TenantID, &tmpl.Version, &tmpl.Content,
		&status, &tmpl.ContentHash, &tmpl.CreatedBy, &tmpl.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("prompt: get by hash: %w", err)
	}
	tmpl.Status = domain.PromptStatus(status)
	return &tmpl, nil
}

// PgBindingRepo persists prompt bindings in public.prompt_bindings.
type PgBindingRepo struct {
	pool *pgxpool.Pool
}

// NewPgBindingRepo constructs a PostgreSQL-backed binding repository.
func NewPgBindingRepo(pool *pgxpool.Pool) *PgBindingRepo {
	return &PgBindingRepo{pool: pool}
}

// UpsertBinding inserts or updates a prompt binding.
func (r *PgBindingRepo) UpsertBinding(ctx context.Context, b domain.PromptBinding) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO public.prompt_bindings (key, scope, stable_version_id, canary_version_id, traffic_percent)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (key, scope) DO UPDATE SET
		   stable_version_id = EXCLUDED.stable_version_id,
		   canary_version_id = EXCLUDED.canary_version_id,
		   traffic_percent  = EXCLUDED.traffic_percent`,
		b.Key, b.Scope, b.StableVersionID, b.CanaryVersionID, b.TrafficPercent,
	)
	if err != nil {
		return fmt.Errorf("prompt: upsert binding: %w", err)
	}
	return nil
}

// GetBinding returns a single binding for key+scope.
func (r *PgBindingRepo) GetBinding(ctx context.Context, key, scope string) (*domain.PromptBinding, error) {
	var b domain.PromptBinding
	err := r.pool.QueryRow(ctx,
		`SELECT key, scope, stable_version_id, canary_version_id, traffic_percent
		 FROM public.prompt_bindings WHERE key = $1 AND scope = $2`,
		key, scope,
	).Scan(&b.Key, &b.Scope, &b.StableVersionID, &b.CanaryVersionID, &b.TrafficPercent)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("prompt: get binding: %w", err)
	}
	return &b, nil
}

// ListBindings returns all bindings for a scope prefix.
func (r *PgBindingRepo) ListBindings(ctx context.Context, scope string) ([]domain.PromptBinding, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, scope, stable_version_id, canary_version_id, traffic_percent
		 FROM public.prompt_bindings WHERE scope LIKE $1`, scope+"%",
	)
	if err != nil {
		return nil, fmt.Errorf("prompt: list bindings: %w", err)
	}
	defer rows.Close()
	var bindings []domain.PromptBinding
	for rows.Next() {
		var b domain.PromptBinding
		if err := rows.Scan(&b.Key, &b.Scope, &b.StableVersionID, &b.CanaryVersionID, &b.TrafficPercent); err != nil {
			return nil, fmt.Errorf("prompt: scan binding: %w", err)
		}
		bindings = append(bindings, b)
	}
	return bindings, rows.Err()
}

// DeleteBinding removes a binding for key+scope.
func (r *PgBindingRepo) DeleteBinding(ctx context.Context, key, scope string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM public.prompt_bindings WHERE key = $1 AND scope = $2`,
		key, scope,
	)
	if err != nil {
		return fmt.Errorf("prompt: delete binding: %w", err)
	}
	return nil
}

func scanPrompts(rows pgx.Rows) ([]domain.PromptTemplate, error) {
	var tmpls []domain.PromptTemplate
	for rows.Next() {
		var tmpl domain.PromptTemplate
		var status string
		if err := rows.Scan(&tmpl.Key, &tmpl.TenantID, &tmpl.Version, &tmpl.Content,
			&status, &tmpl.ContentHash, &tmpl.CreatedBy, &tmpl.CreatedAt); err != nil {
			return nil, fmt.Errorf("prompt: scan: %w", err)
		}
		tmpl.Status = domain.PromptStatus(status)
		tmpls = append(tmpls, tmpl)
	}
	return tmpls, rows.Err()
}
