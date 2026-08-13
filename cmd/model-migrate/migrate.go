package main

// migrate 一次性把各租户 schema 的 providers/models 迁到 public 平台目录。
// 设计见 docs/superpowers/specs/2026-08-13-model-management-refactor-design.md §10。
// 规则：provider 按 name 归并，同 name 多 key 冲突取 enabled 且 updated_at 最新（告警不静默）；
// model 按 (provider_name, name) 归并，default_embedding 全局唯一冲突保留先创建者（清多余标记 + 告警）；
// API key 密文原样搬，不解密不重加密；对账：迁移后 public 行数 = 归并后预期行数。

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// providerKeySeparator 分隔 provider 名与 model 名，构成 models 归并 key。
const providerKeySeparator = "\x00"

type tenantProvider struct {
	id, name, kind, baseURL, apiKey, defaultModel string
	enabled                                       bool
	createdAt, updatedAt                          time.Time
}

type tenantModel struct {
	providerName, name, displayName   string
	capabilities                      []string
	contextWindow, maxTokens          int
	inputPrice, outputPrice           float64
	recommended, enabled              bool
	providerManaged, defaultEmbedding bool
	createdAt                         time.Time
}

// mergedProvider 携带归并后的 public 新 id 与参与归并的 key 源数（冲突检测用）。
type mergedProvider struct {
	tenantProvider
	newID string
	keys  int
}

type mergedModel struct {
	tenantModel
	newID string
}

// mergeResult 是一次迁移的全部归并产物；warnings 记录冲突，绝不静默选 key。
type mergeResult struct {
	providers map[string]*mergedProvider // key: provider.name
	models    map[string]*mergedModel    // key: providerName+"\x00"+name
	warnings  []string
}

// discoverTenantSchemas 返回全部非 public / 非 pg_* 的 schema（即各租户 schema）。
func discoverTenantSchemas(ctx context.Context, pool pgxPool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT schema_name FROM information_schema.schemata
		WHERE schema_name <> 'public' AND schema_name NOT LIKE 'pg\_%'
		ORDER BY schema_name`)
	if err != nil {
		return nil, fmt.Errorf("discover schemas: %w", err)
	}
	defer rows.Close()
	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("scan schema: %w", err)
		}
		schemas = append(schemas, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discover schemas: %w", err)
	}
	return schemas, nil
}

// collectTenantData 遍历所有租户 schema 读取存量 providers/models。
// 表缺失（42P01，租户未 provision 或已被清理）跳过该 schema，其余错误必须中止。
func collectTenantData(ctx context.Context, pool pgxPool, schemas []string) ([]tenantProvider, []tenantModel, error) {
	var providers []tenantProvider
	var models []tenantModel
	for _, schema := range schemas {
		ps, err := queryTenantProviders(ctx, pool, schema)
		if err != nil {
			if isRelationMissing(err) {
				continue
			}
			return nil, nil, fmt.Errorf("read providers (schema %s): %w", schema, err)
		}
		ms, err := queryTenantModels(ctx, pool, schema)
		if err != nil {
			if isRelationMissing(err) {
				continue
			}
			return nil, nil, fmt.Errorf("read models (schema %s): %w", schema, err)
		}
		providers = append(providers, ps...)
		models = append(models, ms...)
	}
	return providers, models, nil
}

func queryTenantProviders(ctx context.Context, pool pgxPool, schema string) ([]tenantProvider, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, name, kind, base_url, api_key, default_model, enabled, created_at, updated_at
		 FROM "`+schema+`".providers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tenantProvider
	for rows.Next() {
		var p tenantProvider
		if err := rows.Scan(&p.id, &p.name, &p.kind, &p.baseURL, &p.apiKey,
			&p.defaultModel, &p.enabled, &p.createdAt, &p.updatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func queryTenantModels(ctx context.Context, pool pgxPool, schema string) ([]tenantModel, error) {
	rows, err := pool.Query(ctx,
		`SELECT p.name AS provider_name, m.name, m.display_name, m.capabilities,
		        m.context_window, m.max_tokens, m.input_price, m.output_price,
		        m.recommended, m.enabled, m.provider_managed, m.default_embedding, m.created_at
		 FROM "`+schema+`".models m
		 JOIN "`+schema+`".providers p ON p.id = m.provider_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tenantModel
	for rows.Next() {
		var m tenantModel
		if err := rows.Scan(&m.providerName, &m.name, &m.displayName, &m.capabilities,
			&m.contextWindow, &m.maxTokens, &m.inputPrice, &m.outputPrice,
			&m.recommended, &m.enabled, &m.providerManaged, &m.defaultEmbedding,
			&m.createdAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// mergePlan 执行 provider 按 name、model 按 (provider_name, name) 的归并。
// 纯函数，不触碰 DB，便于表驱动测试。
func mergePlan(providers []tenantProvider, models []tenantModel) *mergeResult {
	res := &mergeResult{
		providers: make(map[string]*mergedProvider, len(providers)),
		models:    make(map[string]*mergedModel, len(models)),
	}
	mergeProviders(res, providers)
	mergeModels(res, models)
	return res
}

// mergeProviders 按 name 归并 provider：同 name 多 key 冲突取 enabled 且 updated_at 最新，告警不静默。
func mergeProviders(res *mergeResult, providers []tenantProvider) {
	for _, p := range providers {
		merged, ok := res.providers[p.name]
		if !ok {
			res.providers[p.name] = &mergedProvider{tenantProvider: p, newID: uuid.NewString(), keys: 1}
			continue
		}
		merged.keys++
		if merged.apiKey != p.apiKey {
			merged.tenantProvider = pickProviderWinner(merged.tenantProvider, p)
			res.warnings = append(res.warnings,
				fmt.Sprintf("provider %q 多 key 冲突，取 enabled 且 updated_at 最新（共 %d 个源）",
					p.name, merged.keys))
		}
	}
}

// mergeModels 按 (provider_name, name) 归并 model：default_embedding 冲突与普通重复分别处理。
func mergeModels(res *mergeResult, models []tenantModel) {
	for _, m := range models {
		key := m.providerName + providerKeySeparator + m.name
		existing, ok := res.models[key]
		if !ok {
			res.models[key] = &mergedModel{tenantModel: m, newID: uuid.NewString()}
			continue
		}
		if existing.defaultEmbedding && m.defaultEmbedding {
			res.models[key] = resolveEmbeddingConflict(key, existing, m, res)
			continue
		}
		res.models[key] = resolveDuplicateModel(existing, m)
	}
}

// resolveEmbeddingConflict 双 default_embedding 同键冲突：保留先创建者，清后创建者标记并告警。
func resolveEmbeddingConflict(key string, existing *mergedModel, m tenantModel, res *mergeResult) *mergedModel {
	keep, cleared := existing.tenantModel, m
	if m.createdAt.Before(existing.createdAt) {
		keep, cleared = m, existing.tenantModel
	}
	cleared.defaultEmbedding = false
	res.warnings = append(res.warnings,
		fmt.Sprintf("model %q default_embedding 冲突，保留先创建者（%s），已清 %s 标记",
			key, keep.createdAt.Format(time.RFC3339), cleared.name))
	return &mergedModel{tenantModel: keep, newID: existing.newID}
}

// resolveDuplicateModel 无 default_embedding 冲突的同键重复：取启用者，双启用取后更新。
func resolveDuplicateModel(existing *mergedModel, m tenantModel) *mergedModel {
	if existing.enabled != m.enabled {
		if m.enabled {
			return &mergedModel{tenantModel: m, newID: existing.newID}
		}
		return existing
	}
	if m.createdAt.After(existing.createdAt) {
		return &mergedModel{tenantModel: m, newID: existing.newID}
	}
	return existing
}

// pickProviderWinner 冲突时选 enabled 优先，同 enabled 状态取 updated_at 最新。
func pickProviderWinner(a, b tenantProvider) tenantProvider {
	if a.enabled != b.enabled {
		if a.enabled {
			return a
		}
		return b
	}
	if b.updatedAt.After(a.updatedAt) {
		return b
	}
	return a
}

// apply 按归并结果写 public 平台目录。幂等：ON CONFLICT 跳过已存在行，重复运行不产生重复数据。
// 返回写入的 provider/model 行数（不含冲突跳过的已有行）。
func apply(ctx context.Context, pool pgxPool, res *mergeResult) (int, int, error) {
	providerCount, modelCount := 0, 0
	for name, p := range res.providers {
		ct, err := pool.Exec(ctx,
			`INSERT INTO public.providers
			    (id, name, kind, base_url, api_key, default_model, enabled, created_at, updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			 ON CONFLICT (name) DO NOTHING`,
			p.newID, name, p.kind, p.baseURL, p.apiKey, p.defaultModel,
			p.enabled, p.createdAt, p.updatedAt)
		if err != nil {
			return providerCount, modelCount, fmt.Errorf("insert public provider %q: %w", name, err)
		}
		providerCount += int(ct.RowsAffected())
		// provider 可能已存在（ON CONFLICT 跳过）：model 的 provider_id 必须指向 public 真实 id，
		// 而非归并期生成的新 UUID，否则 FK 违反。统一回读真实 id 写回，供 model 循环使用。
		var pid string
		if err := pool.QueryRow(ctx,
			`SELECT id FROM public.providers WHERE name = $1`, name).Scan(&pid); err != nil {
			return providerCount, modelCount, fmt.Errorf("resolve public provider %q id: %w", name, err)
		}
		res.providers[name].newID = pid
	}
	for key, m := range res.models {
		providerName, _, found := strings.Cut(key, providerKeySeparator)
		if !found {
			return providerCount, modelCount,
				fmt.Errorf("invalid model key %q: missing provider separator", key)
		}
		provider, ok := res.providers[providerName]
		if !ok {
			return providerCount, modelCount,
				fmt.Errorf("model %q references missing provider %q", key, providerName)
		}
		ct, err := pool.Exec(ctx,
			`INSERT INTO public.models
			    (id, provider_id, name, display_name, capabilities, context_window, max_tokens,
			     input_price, output_price, recommended, enabled, provider_managed, default_embedding,
			     created_at, updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			 ON CONFLICT (provider_id, name) DO NOTHING`,
			m.newID, provider.newID, m.name, m.displayName, m.capabilities,
			m.contextWindow, m.maxTokens, m.inputPrice, m.outputPrice,
			m.recommended, m.enabled, m.providerManaged, m.defaultEmbedding,
			m.createdAt, m.createdAt)
		if err != nil {
			return providerCount, modelCount, fmt.Errorf("insert public model %q: %w", key, err)
		}
		modelCount += int(ct.RowsAffected())
	}
	return providerCount, modelCount, nil
}

// reconcile 对账迁移结果与 public 实际行数；差异即失败（防静默漏迁）。
func reconcile(ctx context.Context, pool pgxPool, res *mergeResult, logger *zap.Logger, dryRun bool) error {
	if dryRun {
		logger.Info("dry-run reconcile",
			zap.Int("expected_providers", len(res.providers)),
			zap.Int("expected_models", len(res.models)))
		return nil
	}
	actualProviders, err := countRows(ctx, pool, "public.providers")
	if err != nil {
		return err
	}
	actualModels, err := countRows(ctx, pool, "public.models")
	if err != nil {
		return err
	}
	logger.Info("reconcile",
		zap.Int("expected_providers", len(res.providers)),
		zap.Int("actual_providers", actualProviders),
		zap.Int("expected_models", len(res.models)),
		zap.Int("actual_models", actualModels))
	if actualProviders < len(res.providers) || actualModels < len(res.models) {
		return fmt.Errorf("reconcile mismatch: providers %d<%d or models %d<%d",
			actualProviders, len(res.providers), actualModels, len(res.models))
	}
	return nil
}

func countRows(ctx context.Context, pool pgxPool, table string) (int, error) {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		return 0, fmt.Errorf("count %s: %w", table, err)
	}
	return n, nil
}

// migrate 编排一次迁移：发现 schema → 收集 → 归并 → dry-run 打印或 apply → 对账。
func migrate(ctx context.Context, pool pgxPool, logger *zap.Logger, dryRun bool) error {
	schemas, err := discoverTenantSchemas(ctx, pool)
	if err != nil {
		return err
	}
	providers, models, err := collectTenantData(ctx, pool, schemas)
	if err != nil {
		return err
	}
	res := mergePlan(providers, models)
	logger.Info("merge plan",
		zap.Int("source_providers", len(providers)),
		zap.Int("source_models", len(models)),
		zap.Int("merged_providers", len(res.providers)),
		zap.Int("merged_models", len(res.models)),
		zap.Int("warnings", len(res.warnings)),
		zap.Bool("dry_run", dryRun))
	for _, w := range res.warnings {
		logger.Warn("merge warning", zap.String("detail", w))
	}
	names := make([]string, 0, len(res.providers))
	for name := range res.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := res.providers[name]
		logger.Info("provider",
			zap.String("name", name), zap.String("kind", p.kind),
			zap.Bool("enabled", p.enabled), zap.Int("sources", p.keys))
	}
	modelKeys := make([]string, 0, len(res.models))
	for k := range res.models {
		modelKeys = append(modelKeys, k)
	}
	sort.Strings(modelKeys)
	for _, k := range modelKeys {
		m := res.models[k]
		logger.Info("model",
			zap.String("provider", m.providerName), zap.String("name", m.name),
			zap.Bool("default_embedding", m.defaultEmbedding))
	}
	if dryRun {
		return nil
	}
	if _, _, err := apply(ctx, pool, res); err != nil {
		return err
	}
	return reconcile(ctx, pool, res, logger, false)
}

// pgCodeUndefinedTable 是 SQLSTATE 42P01；pgconn 未导出常量，显式声明供 isRelationMissing 用。
const pgCodeUndefinedTable = "42P01"

func isRelationMissing(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeUndefinedTable
}
