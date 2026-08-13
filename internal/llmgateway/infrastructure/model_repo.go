package infrastructure

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// pgxPool 是 pgx 连接池的最小接口（public 平台目录数据，不走租户边界封装）。
// Begin 用于需要多语句原子性的操作（UpsertDiscovered、SetDefaultEmbedding）。
type pgxPool interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PgModelRepo implements port.ModelRepository backed by PostgreSQL.
// models 已提升为 public 平台全局目录（035 迁移），SQL 显式限定 public 前缀，
// 不依赖连接残留 search_path。
type PgModelRepo struct {
	pool pgxPool
}

// NewPgModelRepo returns a new PgModelRepo.
func NewPgModelRepo(pool *pgxpool.Pool) *PgModelRepo {
	return &PgModelRepo{pool: pool}
}

// Create inserts a new model row and populates DB-generated timestamps on m.
func (r *PgModelRepo) Create(ctx context.Context, m *domain.Model) error {
	caps := modelCapsToStrings(m.Capabilities)
	return r.pool.QueryRow(ctx,
		`INSERT INTO public.models (id, provider_id, name, display_name, capabilities,
		 context_window, max_tokens, input_price, output_price, recommended, enabled, provider_managed)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 RETURNING created_at, updated_at`,
		m.ID, m.ProviderID, m.Name, m.DisplayName, caps,
		m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice,
		m.Recommended, m.Enabled, m.ProviderManaged,
	).Scan(&m.CreatedAt, &m.UpdatedAt)
}

// Get retrieves a single model by ID.
func (r *PgModelRepo) Get(ctx context.Context, id string) (*domain.Model, error) {
	var m domain.Model
	var caps []string
	err := r.pool.QueryRow(ctx,
		`SELECT id, provider_id, name, display_name, capabilities,
		 context_window, max_tokens, input_price, output_price, recommended, default_embedding,
		 enabled, provider_managed, created_at, updated_at FROM public.models WHERE id=$1`, id,
	).Scan(&m.ID, &m.ProviderID, &m.Name, &m.DisplayName, &caps,
		&m.ContextWindow, &m.MaxTokens, &m.InputPrice, &m.OutputPrice,
		&m.Recommended, &m.DefaultEmbedding, &m.Enabled, &m.ProviderManaged, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get model: %w", err)
	}
	m.Capabilities = stringsToModelCaps(caps)
	return &m, nil
}

// List returns models matching the optional filter, ordered by name.
func (r *PgModelRepo) List(ctx context.Context, filter port.ModelFilter) ([]domain.Model, error) {
	query := `SELECT id, provider_id, name, display_name, capabilities,
	          context_window, max_tokens, input_price, output_price, recommended, default_embedding,
	          enabled, provider_managed, created_at, updated_at FROM public.models`
	var args []any
	argIdx := 1
	var conds []string
	if filter.ProviderID != "" {
		conds = append(conds, fmt.Sprintf("provider_id=$%d", argIdx))
		args = append(args, filter.ProviderID)
		argIdx++
	}
	if filter.Enabled != nil {
		conds = append(conds, fmt.Sprintf("enabled=$%d", argIdx))
		args = append(args, *filter.Enabled)
		argIdx++
	}
	if filter.Capability != "" {
		conds = append(conds, fmt.Sprintf("$%d = ANY(capabilities)", argIdx))
		args = append(args, string(filter.Capability))
	}
	if len(conds) > 0 {
		query += " WHERE " + joinConditions(conds)
	}
	query += " ORDER BY name"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Model, 0, 8)
	for rows.Next() {
		var m domain.Model
		var caps []string
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.Name, &m.DisplayName, &caps,
			&m.ContextWindow, &m.MaxTokens, &m.InputPrice, &m.OutputPrice,
			&m.Recommended, &m.DefaultEmbedding, &m.Enabled, &m.ProviderManaged, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list models: scan: %w", err)
		}
		m.Capabilities = stringsToModelCaps(caps)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list models: iterate: %w", err)
	}
	return out, nil
}

// joinConditions 拼接 WHERE 条件片段（AND 分隔），保持 List 动态查询可读。
func joinConditions(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}

// Update modifies an editable model. Returns an error if not found.
func (r *PgModelRepo) Update(ctx context.Context, m *domain.Model) error {
	caps := modelCapsToStrings(m.Capabilities)
	tag, err := r.pool.Exec(ctx,
		`UPDATE public.models SET display_name=$1, capabilities=$2, context_window=$3, max_tokens=$4,
		 input_price=$5, output_price=$6, recommended=$7, enabled=$8, updated_at=now(),
		 default_embedding = default_embedding AND $8 AND 'embedding' = ANY($2)
		 WHERE id=$9`,
		m.DisplayName, caps, m.ContextWindow, m.MaxTokens,
		m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled, m.ID,
	)
	if err != nil {
		return fmt.Errorf("update model: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model not found: %s", m.ID)
	}
	return nil
}

// UpsertDiscovered syncs provider-managed models: disables stale entries,
// inserts new ones, and re-enables existing ones while preserving user edits.
// 全部在单事务内执行，保证 disable 阶段与写入/读回原子。
func (r *PgModelRepo) UpsertDiscovered(ctx context.Context, providerID string, models []domain.Model) ([]domain.Model, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("upsert models: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.upsertDisablePhase(ctx, tx, providerID); err != nil {
		return nil, err
	}
	for _, m := range models {
		if err := r.upsertSyncModel(ctx, tx, providerID, m); err != nil {
			return nil, err
		}
	}
	result, err := r.upsertReadBack(ctx, tx, providerID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("upsert models: commit: %w", err)
	}
	return result, nil
}

// upsertDisablePhase 将 provider 的全部 provider-managed 模型标记 disabled。
// default_embedding 标记刻意不在此清空：re-enable 无 restore 通道，清掉会丢
// 用户对仍在列表内模型的标记；只清理 capabilities 已不含 embedding 的失效标记
// （disabled 的标记无害，解析按 enabled 过滤）。
func (r *PgModelRepo) upsertDisablePhase(ctx context.Context, tx pgx.Tx, providerID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE public.models SET enabled=false, updated_at=now(),
		 default_embedding = default_embedding AND 'embedding' = ANY(capabilities)
		 WHERE provider_id=$1 AND provider_managed=true`,
		providerID); err != nil {
		return fmt.Errorf("upsert models: disable phase: %w", err)
	}
	return nil
}

// upsertSyncModel 同步单个 provider 发现模型：不存在则插入（provider_managed），
// 已存在则 re-enable 并同步 provider 上报的上下文元数据，保留用户可编辑字段
// （display_name、capabilities、定价、recommended）。
func (r *PgModelRepo) upsertSyncModel(ctx context.Context, tx pgx.Tx, providerID string, m domain.Model) error {
	caps := modelCapsToStrings(m.Capabilities)
	var existingID string
	err := tx.QueryRow(ctx,
		`SELECT id FROM public.models WHERE provider_id=$1 AND name=$2`,
		providerID, m.Name,
	).Scan(&existingID)
	if err == nil {
		_, err = tx.Exec(ctx,
			`UPDATE public.models SET enabled=true, context_window=$1, max_tokens=$2, updated_at=now()
			 WHERE id=$3`,
			m.ContextWindow, m.MaxTokens, existingID)
		if err != nil {
			return fmt.Errorf("upsert models: update %s: %w", m.Name, err)
		}
		return nil
	}
	// New model -- insert with defaults
	_, err = tx.Exec(ctx,
		`INSERT INTO public.models (id, provider_id, name, display_name, capabilities,
		 context_window, max_tokens, input_price, output_price, recommended, enabled, provider_managed)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,true,true)`,
		uuid.Must(uuid.NewV7()).String(),
		providerID, m.Name, m.DisplayName, caps,
		m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice, m.Recommended)
	if err != nil {
		return fmt.Errorf("upsert models: insert %s: %w", m.Name, err)
	}
	return nil
}

// upsertReadBack 在事务内读回该 provider 全部模型（按 name 排序）并转为 domain.Model。
func (r *PgModelRepo) upsertReadBack(ctx context.Context, tx pgx.Tx, providerID string) ([]domain.Model, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, provider_id, name, display_name, capabilities,
		 context_window, max_tokens, input_price, output_price, recommended, default_embedding,
		 enabled, provider_managed, created_at, updated_at
		 FROM public.models WHERE provider_id=$1 ORDER BY name`,
		providerID)
	if err != nil {
		return nil, fmt.Errorf("upsert models: read back: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Model, 0, 16)
	for rows.Next() {
		var model domain.Model
		var caps []string
		if err := rows.Scan(&model.ID, &model.ProviderID, &model.Name,
			&model.DisplayName, &caps, &model.ContextWindow, &model.MaxTokens,
			&model.InputPrice, &model.OutputPrice, &model.Recommended, &model.DefaultEmbedding,
			&model.Enabled, &model.ProviderManaged, &model.CreatedAt, &model.UpdatedAt); err != nil {
			return nil, fmt.Errorf("upsert models: scan: %w", err)
		}
		model.Capabilities = stringsToModelCaps(caps)
		result = append(result, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("upsert models: iterate: %w", err)
	}
	return result, nil
}

// Delete removes a non-provider-managed model by ID.
func (r *PgModelRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM public.models WHERE id=$1 AND provider_managed=false`, id)
	if err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model not found or is provider-managed: %s", id)
	}
	return nil
}

// Toggle enables or disables a model by ID.
func (r *PgModelRepo) Toggle(ctx context.Context, id string, enabled bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE public.models SET enabled=$1, updated_at=now(),
		 default_embedding = default_embedding AND $1 AND 'embedding' = ANY(capabilities)
		 WHERE id=$2`,
		enabled, id)
	if err != nil {
		return fmt.Errorf("toggle model: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model not found: %s", id)
	}
	return nil
}

// SetDefaultEmbedding sets or clears the default-embedding marker for a model.
// enabled=true clears all other markers globally first, then sets the target
// in the same transaction (atomic clear-then-set). default_embedding 是全局
// 唯一标记（035 partial unique index 兜底）。The target must be enabled and
// carry the embedding capability; otherwise the call fails closed.
func (r *PgModelRepo) SetDefaultEmbedding(ctx context.Context, id string, enabled bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set default embedding: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if !enabled {
		tag, err := tx.Exec(ctx,
			`UPDATE public.models SET default_embedding=false WHERE id=$1`, id)
		if err != nil {
			return fmt.Errorf("clear default embedding: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("model not found: %s: %w", id, domain.ErrModelNotFound)
		}
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE public.models SET default_embedding=false WHERE id<>$1`,
		id); err != nil {
		return fmt.Errorf("clear other default embeddings: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE public.models SET default_embedding=true WHERE id=$1 AND enabled AND 'embedding' = ANY(capabilities)`,
		id)
	if err != nil {
		return fmt.Errorf("set default embedding: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model not found or not an enabled embedding model: %s: %w", id, domain.ErrModelNotFound)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("set default embedding: commit: %w", err)
	}
	return nil
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
