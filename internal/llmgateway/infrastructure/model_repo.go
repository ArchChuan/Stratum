package infrastructure

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// tenantPool 是 pgxmock 可替换的最小池接口，满足 postgres.ExecTenantWith。
type tenantPool interface {
	Begin(context.Context) (pgx.Tx, error)
}

// PgModelRepo implements port.ModelRepository backed by PostgreSQL.
type PgModelRepo struct {
	pool tenantPool
}

// NewPgModelRepo returns a new PgModelRepo.
func NewPgModelRepo(pool *pgxpool.Pool) *PgModelRepo {
	return &PgModelRepo{pool: pool}
}

func (r *PgModelRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return postgres.ExecTenantWith(ctx, r.pool, tenantID, fn)
}

// Create inserts a new model row and populates DB-generated timestamps on m.
func (r *PgModelRepo) Create(ctx context.Context, tenantID string, m *domain.Model) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		caps := modelCapsToStrings(m.Capabilities)
		return tx.QueryRow(ctx,
			`INSERT INTO models (id, tenant_id, provider_id, name, display_name, capabilities,
			 context_window, max_tokens, input_price, output_price, recommended, enabled, provider_managed)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			 RETURNING created_at, updated_at`,
			m.ID, tenantID, m.ProviderID, m.Name, m.DisplayName, caps,
			m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice,
			m.Recommended, m.Enabled, m.ProviderManaged,
		).Scan(&m.CreatedAt, &m.UpdatedAt)
	})
}

// Get retrieves a single model by ID.
func (r *PgModelRepo) Get(ctx context.Context, tenantID, id string) (*domain.Model, error) {
	var m domain.Model
	var caps []string
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, tenant_id, provider_id, name, display_name, capabilities,
			 context_window, max_tokens, input_price, output_price, recommended, default_embedding,
			 enabled, provider_managed, created_at, updated_at FROM models WHERE id=$1`, id,
		).Scan(&m.ID, &m.TenantID, &m.ProviderID, &m.Name, &m.DisplayName, &caps,
			&m.ContextWindow, &m.MaxTokens, &m.InputPrice, &m.OutputPrice,
			&m.Recommended, &m.DefaultEmbedding, &m.Enabled, &m.ProviderManaged, &m.CreatedAt, &m.UpdatedAt)
	})
	if err != nil {
		return nil, fmt.Errorf("get model: %w", err)
	}
	m.Capabilities = stringsToModelCaps(caps)
	return &m, nil
}

// List returns models matching the optional filter, ordered by name.
func (r *PgModelRepo) List(ctx context.Context, tenantID string, filter port.ModelFilter) ([]domain.Model, error) {
	var out []domain.Model
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		query := `SELECT id, tenant_id, provider_id, name, display_name, capabilities,
		          context_window, max_tokens, input_price, output_price, recommended, default_embedding,
		          enabled, provider_managed, created_at, updated_at FROM models WHERE tenant_id=$1`
		args := []any{tenantID}
		argIdx := 2
		if filter.ProviderID != "" {
			query += fmt.Sprintf(" AND provider_id=$%d", argIdx)
			args = append(args, filter.ProviderID)
			argIdx++
		}
		if filter.Enabled != nil {
			query += fmt.Sprintf(" AND enabled=$%d", argIdx)
			args = append(args, *filter.Enabled)
			argIdx++
		}
		if filter.Capability != "" {
			query += fmt.Sprintf(" AND $%d = ANY(capabilities)", argIdx)
			args = append(args, string(filter.Capability))
		}
		query += " ORDER BY name"
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m domain.Model
			var caps []string
			if err := rows.Scan(&m.ID, &m.TenantID, &m.ProviderID, &m.Name, &m.DisplayName, &caps,
				&m.ContextWindow, &m.MaxTokens, &m.InputPrice, &m.OutputPrice,
				&m.Recommended, &m.DefaultEmbedding, &m.Enabled, &m.ProviderManaged, &m.CreatedAt, &m.UpdatedAt); err != nil {
				return err
			}
			m.Capabilities = stringsToModelCaps(caps)
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	if out == nil {
		out = []domain.Model{}
	}
	return out, nil
}

// Update modifies an editable model. Returns an error if not found.
func (r *PgModelRepo) Update(ctx context.Context, tenantID string, m *domain.Model) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		caps := modelCapsToStrings(m.Capabilities)
		tag, err := tx.Exec(ctx,
			`UPDATE models SET display_name=$1, capabilities=$2, context_window=$3, max_tokens=$4,
			 input_price=$5, output_price=$6, recommended=$7, enabled=$8, updated_at=now(),
			 default_embedding = default_embedding AND enabled AND 'embedding' = ANY(capabilities)
			 WHERE id=$9 AND tenant_id=$10`,
			m.DisplayName, caps, m.ContextWindow, m.MaxTokens,
			m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled, m.ID, tenantID,
		)
		if err != nil {
			return fmt.Errorf("update model: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("model not found: %s", m.ID)
		}
		return nil
	})
}

// UpsertDiscovered syncs provider-managed models: disables stale entries,
// inserts new ones, and re-enables existing ones while preserving user edits.
func (r *PgModelRepo) UpsertDiscovered(ctx context.Context, tenantID, providerID string, models []domain.Model) ([]domain.Model, error) {
	var result []domain.Model
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Mark all provider-managed models for this provider as disabled,
		// then re-enable those still present.
		if _, err := tx.Exec(ctx,
			`UPDATE models SET enabled=false, updated_at=now(),
			 default_embedding = default_embedding AND enabled AND 'embedding' = ANY(capabilities)
			 WHERE tenant_id=$1 AND provider_id=$2 AND provider_managed=true`,
			tenantID, providerID); err != nil {
			return fmt.Errorf("upsert models: disable phase: %w", err)
		}
		for _, m := range models {
			caps := modelCapsToStrings(m.Capabilities)
			var existingID string
			err := tx.QueryRow(ctx,
				`SELECT id FROM models WHERE tenant_id=$1 AND provider_id=$2 AND name=$3`,
				tenantID, providerID, m.Name,
			).Scan(&existingID)
			if err != nil {
				// New model -- insert with defaults
				_, err = tx.Exec(ctx,
					`INSERT INTO models (id, tenant_id, provider_id, name, display_name, capabilities,
					 context_window, max_tokens, input_price, output_price, recommended, enabled, provider_managed)
					 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,true)`,
					uuid.Must(uuid.NewV7()).String(),
					tenantID, providerID, m.Name, m.DisplayName, caps,
					m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice, m.Recommended,
				)
				if err != nil {
					return fmt.Errorf("upsert models: insert %s: %w", m.Name, err)
				}
			} else {
				// Existing -- re-enable, sync provider-reported context metadata
				// while preserving user-editable fields (display_name, capabilities,
				// pricing, recommended).
				_, err = tx.Exec(ctx,
					`UPDATE models SET enabled=true, context_window=$1, max_tokens=$2, updated_at=now()
					 WHERE id=$3`,
					m.ContextWindow, m.MaxTokens, existingID)
				if err != nil {
					return fmt.Errorf("upsert models: update %s: %w", m.Name, err)
				}
			}
		}
		// Read back
		rows, err := tx.Query(ctx,
			`SELECT id, tenant_id, provider_id, name, display_name, capabilities,
			 context_window, max_tokens, input_price, output_price, recommended, default_embedding,
			 enabled, provider_managed, created_at, updated_at
			 FROM models WHERE tenant_id=$1 AND provider_id=$2 ORDER BY name`,
			tenantID, providerID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var model domain.Model
			var caps []string
			if err := rows.Scan(&model.ID, &model.TenantID, &model.ProviderID, &model.Name,
				&model.DisplayName, &caps, &model.ContextWindow, &model.MaxTokens,
				&model.InputPrice, &model.OutputPrice, &model.Recommended, &model.DefaultEmbedding,
				&model.Enabled, &model.ProviderManaged, &model.CreatedAt, &model.UpdatedAt); err != nil {
				return err
			}
			model.Capabilities = stringsToModelCaps(caps)
			result = append(result, model)
		}
		return rows.Err()
	})
	if result == nil {
		result = []domain.Model{}
	}
	return result, err
}

// Delete removes a non-provider-managed model by ID.
func (r *PgModelRepo) Delete(ctx context.Context, tenantID, id string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM models WHERE id=$1 AND tenant_id=$2 AND provider_managed=false`, id, tenantID)
		if err != nil {
			return fmt.Errorf("delete model: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("model not found or is provider-managed: %s", id)
		}
		return nil
	})
}

// Toggle enables or disables a model by ID.
func (r *PgModelRepo) Toggle(ctx context.Context, tenantID, id string, enabled bool) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE models SET enabled=$1, updated_at=now(),
			 default_embedding = default_embedding AND enabled AND 'embedding' = ANY(capabilities)
			 WHERE id=$2 AND tenant_id=$3`,
			enabled, id, tenantID)
		if err != nil {
			return fmt.Errorf("toggle model: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("model not found: %s", id)
		}
		return nil
	})
}

// SetDefaultEmbedding sets or clears the default-embedding marker for a model.
// enabled=true clears all other markers for the tenant first, then sets the
// target in the same transaction (atomic clear-then-set). The target must be
// enabled and carry the embedding capability; otherwise the call fails closed.
func (r *PgModelRepo) SetDefaultEmbedding(ctx context.Context, tenantID, id string, enabled bool) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if !enabled {
			tag, err := tx.Exec(ctx,
				`UPDATE models SET default_embedding=false WHERE id=$1 AND tenant_id=$2`, id, tenantID)
			if err != nil {
				return fmt.Errorf("clear default embedding: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("model not found: %s: %w", id, domain.ErrModelNotFound)
			}
			return nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE models SET default_embedding=false WHERE tenant_id=$1 AND id<>$2`,
			tenantID, id); err != nil {
			return fmt.Errorf("clear other default embeddings: %w", err)
		}
		tag, err := tx.Exec(ctx,
			`UPDATE models SET default_embedding=true WHERE id=$1 AND tenant_id=$2 AND enabled AND 'embedding' = ANY(capabilities)`,
			id, tenantID)
		if err != nil {
			return fmt.Errorf("set default embedding: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("model not found or not an enabled embedding model: %s: %w", id, domain.ErrModelNotFound)
		}
		return nil
	})
}

// modelCapsToStrings converts domain capability constants to string slices for storage.
func modelCapsToStrings(caps []domain.ModelCapability) []string {
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return out
}

// stringsToModelCaps converts stored string slices back to domain capability constants.
func stringsToModelCaps(ss []string) []domain.ModelCapability {
	out := make([]domain.ModelCapability, len(ss))
	for i, s := range ss {
		out[i] = domain.ModelCapability(s)
	}
	return out
}
