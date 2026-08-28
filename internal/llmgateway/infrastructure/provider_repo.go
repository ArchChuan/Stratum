package infrastructure

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditpersistence "github.com/byteBuilderX/stratum/internal/audit/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// PgProviderRepo implements port.ProviderRepository backed by PostgreSQL.
// providers 已提升为 public 平台全局目录（035 迁移），SQL 显式限定 public
// 前缀，不依赖连接残留 search_path。
//
// API keys are encrypted at rest via crypto.EncryptSecret: the providers
// table stores "enc:v1:"-prefixed AES-256-GCM ciphertext, never plaintext.
// 存量兼容策略（双读）：加密功能上线前落库的历史明文没有前缀（crypto.IsEncrypted
// 为 false），读取时按明文放行（恢复可见/可用）；有前缀的必须是可解密的密文，
// 解不开（密文损坏或 key 不匹配）视为"配置无效，请重新保存"错误（fail closed），
// 禁止把存储值当作明文使用。写路径一律加密（Create/Update），明文存量由
// cmd/fix-provider-keys 一次性回填。
type PgProviderRepo struct {
	pool    pgxPool
	key     [32]byte
	logger  *zap.Logger
	metrics observability.MetricsProvider
}

// NewPgProviderRepo returns a new PgProviderRepo with the at-rest encryption key.
func NewPgProviderRepo(pool *pgxpool.Pool, key [32]byte, logger *zap.Logger, metrics observability.MetricsProvider) *PgProviderRepo {
	if logger == nil {
		logger = zap.NewNop()
	}
	if metrics == nil {
		metrics = observability.NoopMetrics{}
	}
	return &PgProviderRepo{pool: pool, key: key, logger: logger, metrics: metrics}
}

// Create inserts a new provider row and populates DB-generated timestamps on p.
func (r *PgProviderRepo) Create(ctx context.Context, p *domain.Provider) error {
	apiKey, err := crypto.EncryptSecret(r.key, p.APIKey)
	if err != nil {
		return fmt.Errorf("create provider: encrypt api key: %w", err)
	}
	extra, err := encodeStringMap(p.ExtraHeaders)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	sampling, err := encodeJSONMap(p.DefaultSampling)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	return r.pool.QueryRow(ctx,
		`INSERT INTO public.providers (id, name, kind, base_url, api_key, default_model, enabled,
		 extra_headers, default_sampling)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING created_at, updated_at`,
		p.ID, p.Name, string(p.Kind), p.BaseURL, apiKey, p.DefaultModel, p.Enabled,
		extra, sampling,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
}

// Get retrieves a single provider by ID.
func (r *PgProviderRepo) Get(ctx context.Context, id string) (*domain.Provider, error) {
	var p domain.Provider
	var kind string
	var extra, sampling []byte
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, kind, base_url, api_key, default_model, enabled,
		 extra_headers, default_sampling, created_at, updated_at
		 FROM public.providers WHERE id=$1`, id,
	).Scan(&p.ID, &p.Name, &kind, &p.BaseURL, &p.APIKey, &p.DefaultModel, &p.Enabled,
		&extra, &sampling, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}
	if err := r.decryptProviderKey(&p); err != nil {
		return nil, err
	}
	p.Kind = domain.ProviderKind(kind)
	p.ExtraHeaders, err = decodeStringMap(extra)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}
	p.DefaultSampling, err = decodeJSONMap(sampling)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}
	return &p, nil
}

// List returns all providers, ordered by creation time.
func (r *PgProviderRepo) List(ctx context.Context) ([]domain.Provider, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, kind, base_url, api_key, default_model, enabled,
		 extra_headers, default_sampling, created_at, updated_at
		 FROM public.providers ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	var out []domain.Provider
	for rows.Next() {
		var p domain.Provider
		var kind string
		var extra, sampling []byte
		if err := rows.Scan(&p.ID, &p.Name, &kind, &p.BaseURL, &p.APIKey,
			&p.DefaultModel, &p.Enabled, &extra, &sampling, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list providers: scan: %w", err)
		}
		p.Kind = domain.ProviderKind(kind)
		p.ExtraHeaders, err = decodeStringMap(extra)
		if err != nil {
			return nil, fmt.Errorf("list providers: %w", err)
		}
		p.DefaultSampling, err = decodeJSONMap(sampling)
		if err != nil {
			return nil, fmt.Errorf("list providers: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list providers: iterate: %w", err)
	}
	// 单条 key 无效（有前缀但解不开 = 密文损坏/key 不匹配）→ 跳过该条并告警，
	// 其余正常返回：一条损坏的密文不应让整个管理页（编辑/删除入口）永久不可用。
	// 历史明文按放行处理（decryptProviderKey 直接返回）。Get 仍保持 fail closed——
	// 只读列表以可用性优先，单条访问以正确性优先。
	kept := out[:0]
	for i := range out {
		if err := r.decryptProviderKey(&out[i]); err != nil {
			r.logger.Warn("provider list: skip provider with invalid api key",
				zap.String("provider_id", out[i].ID), zap.Error(err))
			r.metrics.IncComponentError("llmgateway-provider", "list-decrypt")
			continue
		}
		kept = append(kept, out[i])
	}
	if kept == nil {
		kept = []domain.Provider{}
	}
	return kept, nil
}

// GetMeta retrieves a single provider's metadata without decrypting the API
// key (APIKey is left empty). Update 用它读取元数据：存量明文/损坏密文的
// provider 必须仍能带新 key 重新保存，先解密旧 key 会把该 provider 永久锁死。
func (r *PgProviderRepo) GetMeta(ctx context.Context, id string) (*domain.Provider, error) {
	var p domain.Provider
	var kind string
	var extra, sampling []byte
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, kind, base_url, default_model, enabled,
		 extra_headers, default_sampling, created_at, updated_at
		 FROM public.providers WHERE id=$1`, id,
	).Scan(&p.ID, &p.Name, &kind, &p.BaseURL, &p.DefaultModel, &p.Enabled,
		&extra, &sampling, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get provider meta: %w", err)
	}
	p.Kind = domain.ProviderKind(kind)
	p.ExtraHeaders, err = decodeStringMap(extra)
	if err != nil {
		return nil, fmt.Errorf("get provider meta: %w", err)
	}
	p.DefaultSampling, err = decodeJSONMap(sampling)
	if err != nil {
		return nil, fmt.Errorf("get provider meta: %w", err)
	}
	return &p, nil
}

// decryptProviderKey 将 p.APIKey 从密文解密为明文。按前缀判定（crypto.IsEncrypted）
// 而非错误类型分支：DecryptSecret 对"无前缀明文"与"有前缀但 payload 非法 base64"
// 返回同一个 ErrLegacyPlaintext，若按错误放行会把损坏的密文当成明文凭据使用。
// 无前缀 = 历史明文 → 放行（读兼容，写路径一律加密）；有前缀 → 必须解密成功，
// 失败返回配置无效错误（fail closed），禁止降级为把存储值当明文使用。
func (r *PgProviderRepo) decryptProviderKey(p *domain.Provider) error {
	if !crypto.IsEncrypted(p.APIKey) {
		// 历史明文：放行（恢复可见/可用），由 fix-provider-keys 回填为密文。
		return nil
	}
	plain, err := crypto.DecryptSecret(r.key, p.APIKey)
	if err != nil {
		return fmt.Errorf(
			"provider %s: api key 解密失败（密文损坏或 key 不匹配），请重新保存该 provider 的 api key: %w",
			p.ID, err)
	}
	p.APIKey = plain
	return nil
}

// Update modifies an existing provider and writes the resource change audit in
// the same transaction (audit 表位于租户 schema，事务内 SET LOCAL search_path
// 切换；tenantID 供审计归属)。An empty APIKey keeps the stored ciphertext
// unchanged (CASE WHEN); a non-empty APIKey is encrypted at rest before being
// written. 与 GetMeta 配合：调用方不需要解密旧 key 也能重存。
func (r *PgProviderRepo) Update(ctx context.Context, p *domain.Provider, tenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
	apiKey := ""
	if p.APIKey != "" {
		enc, err := crypto.EncryptSecret(r.key, p.APIKey)
		if err != nil {
			return fmt.Errorf("update provider: encrypt api key: %w", err)
		}
		apiKey = enc
	}
	extra, err := encodeStringMap(p.ExtraHeaders)
	if err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	sampling, err := encodeJSONMap(p.DefaultSampling)
	if err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	tx, err := beginTenantTx(ctx, r.pool, tenantID)
	if err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE public.providers SET name=$1, kind=$2, base_url=$3,
		 api_key=CASE WHEN $4='' THEN api_key ELSE $4 END,
		 default_model=$5, enabled=$6, updated_at=now(),
		 extra_headers=$7, default_sampling=$8
		 WHERE id=$9`,
		p.Name, string(p.Kind), p.BaseURL, apiKey, p.DefaultModel, p.Enabled,
		extra, sampling, p.ID,
	)
	if err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("provider not found: %s", p.ID)
	}
	if err := insertAuditTx(ctx, tx, tenantID, audit); err != nil {
		return fmt.Errorf("update provider: audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("update provider: commit: %w", err)
	}
	return nil
}

// UpdatePlatform performs a public provider mutation and writes its audit in
// the same public transaction. actorTenantID is audit attribution only.
func (r *PgProviderRepo) UpdatePlatform(
	ctx context.Context,
	p *domain.Provider,
	actorTenantID string,
	audit *auditdomain.ResourceChangeAuditEvent,
) error {
	apiKey := ""
	if p.APIKey != "" {
		enc, err := crypto.EncryptSecret(r.key, p.APIKey)
		if err != nil {
			return fmt.Errorf("update platform provider: encrypt api key: %w", err)
		}
		apiKey = enc
	}
	sampling, err := encodeJSONMap(p.DefaultSampling)
	if err != nil {
		return fmt.Errorf("update platform provider: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("update platform provider begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`UPDATE public.providers SET name=$1, kind=$2, base_url=$3,
		 api_key=CASE WHEN $4='' THEN api_key ELSE $4 END,
		 default_model=$5, enabled=$6, default_sampling=$7, updated_at=now()
		 WHERE id=$8`,
		p.Name, string(p.Kind), p.BaseURL, apiKey, p.DefaultModel, p.Enabled, sampling, p.ID)
	if err != nil {
		return fmt.Errorf("update platform provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("provider not found: %s", p.ID)
	}
	if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("update platform provider commit: %w", err)
	}
	return nil
}

// Delete removes a provider by ID.
func (r *PgProviderRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM public.providers WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("provider not found: %s", id)
	}
	return nil
}
