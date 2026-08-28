package infrastructure

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditpersistence "github.com/byteBuilderX/stratum/internal/audit/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// pgxPool 是 pgx 连接池的最小接口（public 平台目录数据，不走租户边界封装）。
// Begin 用于需要多语句原子性的操作（UpsertDiscovered）。
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
	sampling, err := encodeSamplingParams(m.SamplingParams)
	if err != nil {
		return fmt.Errorf("create model: %w", err)
	}
	return r.pool.QueryRow(ctx,
		`INSERT INTO public.models (id, provider_id, name, display_name, capabilities,
		 context_window, max_tokens, input_price, output_price, recommended, enabled, provider_managed,
		 sampling_params, max_temperature)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 RETURNING created_at, updated_at`,
		m.ID, m.ProviderID, m.Name, m.DisplayName, caps,
		m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice,
		m.Recommended, m.Enabled, m.ProviderManaged, sampling, m.MaxTemperature,
	).Scan(&m.CreatedAt, &m.UpdatedAt)
}

// Get retrieves a single model by ID.
func (r *PgModelRepo) Get(ctx context.Context, id string) (*domain.Model, error) {
	var m domain.Model
	var caps []string
	var sampling []byte
	err := r.pool.QueryRow(ctx,
		`SELECT id, provider_id, name, display_name, capabilities,
		 context_window, max_tokens, input_price, output_price, recommended, default_embedding,
		 enabled, provider_managed, sampling_params, max_temperature,
		 operator_context_window, operator_max_tokens, default_output_tokens,
		 fallback_candidates,
		 context_window_source, max_tokens_source, context_window_observed_at, max_tokens_observed_at,
		 created_at, updated_at
		 FROM public.models WHERE id=$1`, id,
	).Scan(&m.ID, &m.ProviderID, &m.Name, &m.DisplayName, &caps,
		&m.ContextWindow, &m.MaxTokens, &m.InputPrice, &m.OutputPrice,
		&m.Recommended, &m.DefaultEmbedding, &m.Enabled, &m.ProviderManaged,
		&sampling, &m.MaxTemperature, &m.OperatorContextWindow, &m.OperatorMaxTokens, &m.DefaultOutputTokens,
		&m.FallbackCandidates, &m.ContextWindowSource, &m.MaxTokensSource, &m.ContextWindowObservedAt, &m.MaxTokensObservedAt,
		&m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get model: %w", err)
	}
	m.Capabilities = stringsToModelCaps(caps)
	m.SamplingParams, err = decodeSamplingParams(sampling)
	if err != nil {
		return nil, fmt.Errorf("get model: %w", err)
	}
	return &m, nil
}

// List returns models matching the optional filter, ordered by name.
func (r *PgModelRepo) List(ctx context.Context, filter port.ModelFilter) ([]domain.Model, error) {
	query := `SELECT id, provider_id, name, display_name, capabilities,
	          context_window, max_tokens, input_price, output_price, recommended, default_embedding,
	          enabled, provider_managed, sampling_params, max_temperature,
	          operator_context_window, operator_max_tokens, default_output_tokens,
	          fallback_candidates,
	          context_window_source, max_tokens_source, context_window_observed_at, max_tokens_observed_at,
	          created_at, updated_at FROM public.models`
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
		var sampling []byte
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.Name, &m.DisplayName, &caps,
			&m.ContextWindow, &m.MaxTokens, &m.InputPrice, &m.OutputPrice,
			&m.Recommended, &m.DefaultEmbedding, &m.Enabled, &m.ProviderManaged,
			&sampling, &m.MaxTemperature, &m.OperatorContextWindow, &m.OperatorMaxTokens, &m.DefaultOutputTokens,
			&m.FallbackCandidates, &m.ContextWindowSource, &m.MaxTokensSource, &m.ContextWindowObservedAt, &m.MaxTokensObservedAt,
			&m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list models: scan: %w", err)
		}
		m.Capabilities = stringsToModelCaps(caps)
		m.SamplingParams, err = decodeSamplingParams(sampling)
		if err != nil {
			return nil, fmt.Errorf("list models: %w", err)
		}
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

// Update modifies an editable model and writes the resource change audit in the
// same transaction (audit 表位于租户 schema，事务内 SET LOCAL search_path 切换；
// tenantID 供审计归属)。Returns an error if not found。
func (r *PgModelRepo) Update(ctx context.Context, m *domain.Model, tenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
	tx, err := beginTenantTx(ctx, r.pool, tenantID)
	if err != nil {
		return fmt.Errorf("update model: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	caps := modelCapsToStrings(m.Capabilities)
	sampling, err := encodeSamplingParams(m.SamplingParams)
	if err != nil {
		return fmt.Errorf("update model: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE public.models SET display_name=$1, capabilities=$2, context_window=$3, max_tokens=$4,
		 input_price=$5, output_price=$6, recommended=$7, enabled=$8, updated_at=now(),
		 sampling_params=$9, max_temperature=$10, fallback_candidates=$11,
		 default_embedding = default_embedding AND $8 AND 'embedding' = ANY($2)
		 WHERE id=$12`,
		m.DisplayName, caps, m.ContextWindow, m.MaxTokens,
		m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled,
		sampling, m.MaxTemperature, m.FallbackCandidates, m.ID,
	)
	if err != nil {
		return fmt.Errorf("update model: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model not found: %s", m.ID)
	}
	if err := insertAuditTx(ctx, tx, tenantID, audit); err != nil {
		return fmt.Errorf("update model: audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("update model: commit: %w", err)
	}
	return nil
}

// UpdatePlatform performs a public-catalog mutation and writes its audit in
// the same public transaction. actorTenantID is audit attribution only.
func (r *PgModelRepo) UpdatePlatform(
	ctx context.Context,
	m *domain.Model,
	actorTenantID string,
	audit *auditdomain.ResourceChangeAuditEvent,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("update platform model: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	caps := modelCapsToStrings(m.Capabilities)
	sampling, err := encodeSamplingParams(m.SamplingParams)
	if err != nil {
		return fmt.Errorf("update platform model: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE public.models SET display_name=$1, capabilities=$2, context_window=$3,
		 max_tokens=$4, input_price=$5, output_price=$6, recommended=$7, enabled=$8,
		 sampling_params=$9, max_temperature=$10, operator_context_window=$11,
		 operator_max_tokens=$12, default_output_tokens=$13, fallback_candidates=$14,
		 updated_at=now(),
		 default_embedding = default_embedding AND $8 AND 'embedding' = ANY($2)
		 WHERE id=$15`,
		m.DisplayName, caps, m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice,
		m.Recommended, m.Enabled, sampling, m.MaxTemperature, m.OperatorContextWindow,
		m.OperatorMaxTokens, m.DefaultOutputTokens, m.FallbackCandidates, m.ID)
	if err != nil {
		return fmt.Errorf("update platform model: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model not found: %s", m.ID)
	}
	if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("update platform model commit: %w", err)
	}
	return nil
}

// CreatePlatform inserts a manually-added model into the public catalog and
// writes its audit in the same public transaction. Column list is locked to
// the 035/039/040 migrations (16 columns incl. context_window_source and
// max_tokens_source).
func (r *PgModelRepo) CreatePlatform(
	ctx context.Context,
	m *domain.Model,
	actorTenantID string,
	audit *auditdomain.ResourceChangeAuditEvent,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("create platform model: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	caps := modelCapsToStrings(m.Capabilities)
	sampling, err := encodeSamplingParams(m.SamplingParams)
	if err != nil {
		return fmt.Errorf("create platform model: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO public.models (id, provider_id, name, display_name, capabilities,
		 context_window, max_tokens, context_window_source, max_tokens_source,
		 input_price, output_price, recommended, enabled, provider_managed,
		 sampling_params, max_temperature)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		m.ID, m.ProviderID, m.Name, m.DisplayName, caps,
		m.ContextWindow, m.MaxTokens, m.ContextWindowSource, m.MaxTokensSource,
		m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled, m.ProviderManaged,
		sampling, m.MaxTemperature)
	if err != nil {
		return fmt.Errorf("create platform model: %w", err)
	}
	if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("create platform model commit: %w", err)
	}
	return nil
}

// UpdatePolicy updates only operator/runtime policy columns. Discovery facts
// and catalog metadata remain untouched.
func (r *PgModelRepo) UpdatePolicy(
	ctx context.Context,
	m *domain.Model,
	tenantID string,
	audit *auditdomain.ResourceChangeAuditEvent,
) error {
	tx, err := beginTenantTx(ctx, r.pool, tenantID)
	if err != nil {
		return fmt.Errorf("update model policy: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	sampling, err := encodeSamplingParams(m.SamplingParams)
	if err != nil {
		return fmt.Errorf("update model policy: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE public.models SET operator_context_window=$1, operator_max_tokens=$2,
		 default_output_tokens=$3, sampling_params=$4, max_temperature=$5,
		 fallback_candidates=$6, updated_at=now()
		 WHERE id=$7`,
		m.OperatorContextWindow, m.OperatorMaxTokens, m.DefaultOutputTokens,
		sampling, m.MaxTemperature, m.FallbackCandidates, m.ID)
	if err != nil {
		return fmt.Errorf("update model policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model not found: %s", m.ID)
	}
	if err := insertAuditTx(ctx, tx, tenantID, audit); err != nil {
		return fmt.Errorf("update model policy audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("update model policy commit: %w", err)
	}
	return nil
}

// UpsertDiscovered syncs provider-managed models: disables stale entries that
// are no longer reported, and inserts new ones (enabled by default). Existing
// models keep their current enabled state — including user manual toggles —
// so a re-discovery never silently re-opens a switch the user turned off.
// 全部在单事务内执行，保证 disable 阶段与写入/读回原子。
func (r *PgModelRepo) UpsertDiscovered(ctx context.Context, providerID string, models []domain.Model) ([]domain.Model, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("upsert models: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name)
	}
	if err := r.upsertDisablePhase(ctx, tx, providerID, names); err != nil {
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

// upsertDisablePhase 只关闭"不在本次上报名集合内"的 stale provider-managed 模型；
// 集合内的存量模型完全不碰 enabled（含用户手动关闭的状态），避免发现流程
// 静默重置用户开关。default_embedding 标记刻意不在此清空：re-enable 无 restore
// 通道，清掉会丢用户对仍在列表内模型的标记；只清理 capabilities 已不含
// embedding 的失效标记（disabled 的标记无害，解析按 enabled 过滤）。
func (r *PgModelRepo) upsertDisablePhase(ctx context.Context, tx pgx.Tx, providerID string, names []string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE public.models SET enabled=false, updated_at=now(),
		 default_embedding = default_embedding AND 'embedding' = ANY(capabilities)
		 WHERE provider_id=$1 AND provider_managed=true AND name != ALL($2)`,
		providerID, names); err != nil {
		return fmt.Errorf("upsert models: disable phase: %w", err)
	}
	return nil
}

// upsertSyncModel 同步单个 provider 发现模型：不存在则插入（provider_managed，
// enabled 默认开启），已存在则只同步 provider 上报的上下文元数据——enabled 保留
// 现状（含用户手动关闭），不静默重新打开；用户可编辑字段（display_name、
// capabilities、定价、recommended）也不覆盖。
func (r *PgModelRepo) upsertSyncModel(ctx context.Context, tx pgx.Tx, providerID string, m domain.Model) error {
	caps := modelCapsToStrings(m.Capabilities)
	var existingID string
	err := tx.QueryRow(ctx,
		`SELECT id FROM public.models WHERE provider_id=$1 AND name=$2`,
		providerID, m.Name,
	).Scan(&existingID)
	if err == nil {
		_, err = tx.Exec(ctx,
			`UPDATE public.models SET context_window=$1, max_tokens=$2,
			 context_window_source=$3, max_tokens_source=$4,
			 context_window_observed_at=now(), max_tokens_observed_at=now(), updated_at=now()
			 WHERE id=$5`,
			m.ContextWindow, m.MaxTokens, m.ContextWindowSource, m.MaxTokensSource, existingID)
		if err != nil {
			return fmt.Errorf("upsert models: update %s: %w", m.Name, err)
		}
		return nil
	}
	// New model -- insert with defaults
	_, err = tx.Exec(ctx,
		`INSERT INTO public.models (id, provider_id, name, display_name, capabilities,
		 context_window, max_tokens, input_price, output_price, recommended, enabled, provider_managed,
		 context_window_source, max_tokens_source, context_window_observed_at, max_tokens_observed_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,true,true,$11,$12,now(),now())`,
		uuid.Must(uuid.NewV7()).String(),
		providerID, m.Name, m.DisplayName, caps,
		m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice, m.Recommended,
		m.ContextWindowSource, m.MaxTokensSource)
	if err != nil {
		return fmt.Errorf("upsert models: insert %s: %w", m.Name, err)
	}
	return nil
}

// upsertReadBack 在事务内读回该 provider 全部模型（按 name 排序）并转为 domain.Model。
// 新列和运营策略一并读回（默认 '{}'/NULL 时解码为 nil）。
func (r *PgModelRepo) upsertReadBack(ctx context.Context, tx pgx.Tx, providerID string) ([]domain.Model, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, provider_id, name, display_name, capabilities,
		 context_window, max_tokens, input_price, output_price, recommended, default_embedding,
		 enabled, provider_managed, sampling_params, max_temperature,
		 operator_context_window, operator_max_tokens, default_output_tokens,
		 fallback_candidates,
		 context_window_source, max_tokens_source, context_window_observed_at, max_tokens_observed_at,
		 created_at, updated_at
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
		var sampling []byte
		if err := rows.Scan(&model.ID, &model.ProviderID, &model.Name,
			&model.DisplayName, &caps, &model.ContextWindow, &model.MaxTokens,
			&model.InputPrice, &model.OutputPrice, &model.Recommended, &model.DefaultEmbedding,
			&model.Enabled, &model.ProviderManaged, &sampling, &model.MaxTemperature,
			&model.OperatorContextWindow, &model.OperatorMaxTokens, &model.DefaultOutputTokens,
			&model.FallbackCandidates, &model.ContextWindowSource, &model.MaxTokensSource,
			&model.ContextWindowObservedAt, &model.MaxTokensObservedAt, &model.CreatedAt, &model.UpdatedAt); err != nil {
			return nil, fmt.Errorf("upsert models: scan: %w", err)
		}
		model.Capabilities = stringsToModelCaps(caps)
		model.SamplingParams, err = decodeSamplingParams(sampling)
		if err != nil {
			return nil, fmt.Errorf("upsert models: %w", err)
		}
		result = append(result, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("upsert models: iterate: %w", err)
	}
	return result, nil
}

// Delete removes a model by ID regardless of provider-managed flag.
// 删除后该厂商再次"发现模型"会按 upsertSyncModel 重新插入（删除语义 =
// "直到下次发现前不出现"）。
func (r *PgModelRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM public.models WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model not found: %s", id)
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
