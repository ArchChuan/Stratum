package wiring

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

// errMemoryEmbeddingNotConfigured 表示租户未显式配置记忆嵌入模型。作为
// fail-closed 信号返回：调用方不得回退到任何全局默认模型，而应让该链路
// 明确失败（记忆对话失败 / 消息进 DLQ / 不建 collection）。
var errMemoryEmbeddingNotConfigured = errors.New(
	"memory embedding: tenant has no embedding model configured, please set it in the tenant settings")

// tenantEmbeddingModelResolver 解析租户显式配置的记忆嵌入模型
// （public.tenants.settings JSONB 键 memory_embedding_model），fail-closed：
// 未配置、配置值为空或目录校验失败一律返回 error，绝不回退全局默认模型。
//
// iam 在 knowledge/memory 之后构建，因此通过 getTenants 延迟绑定运行时才取
// TenantService；nil-safe（db 不可用 / iam 未构建时返回 error，而非 panic）。
type tenantEmbeddingModelResolver struct {
	getTenants func() *iamapp.TenantService
	registry   *llmgateway.ModelRegistry
	logger     *zap.Logger
}

func newTenantEmbeddingModelResolver(
	getTenants func() *iamapp.TenantService,
	registry *llmgateway.ModelRegistry,
	logger *zap.Logger,
) *tenantEmbeddingModelResolver {
	return &tenantEmbeddingModelResolver{
		getTenants: getTenants,
		registry:   registry,
		logger:     logger,
	}
}

// ResolveMemoryEmbeddingModel 返回租户显式配置的记忆嵌入模型名。校验顺序：
// TenantService 可用 → 读取 settings → 键存在且非空 → 目录可解析（须为
// embedding 能力模型）→ 返回模型名。任何一步失败返回 error（fail-closed）。
func (r *tenantEmbeddingModelResolver) ResolveMemoryEmbeddingModel(ctx context.Context, tenantID string) (string, error) {
	if r == nil || r.getTenants == nil || r.registry == nil {
		return "", errMemoryEmbeddingNotConfigured
	}
	tenants := r.getTenants()
	if tenants == nil {
		return "", errMemoryEmbeddingNotConfigured
	}
	_, _, settings, err := tenants.GetSettings(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("memory embedding: read tenant settings: %w", err)
	}
	raw, ok := settings["memory_embedding_model"]
	if !ok {
		return "", errMemoryEmbeddingNotConfigured
	}
	model, _ := raw.(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return "", errMemoryEmbeddingNotConfigured
	}
	if _, _, err := r.registry.ResolveEmbeddingExact(ctx, model); err != nil {
		return "", fmt.Errorf("memory embedding: resolve model %q: %w", model, err)
	}
	return model, nil
}

// IsMemoryEmbeddingModelConfigured 报告租户是否已显式配置记忆嵌入模型键
// （键存在且非空即 true，不校验目录）。供启动 seed 判断「键缺失」以幂等
// 回填——已配置但当前解析失败的租户不得被 seed 覆盖。
func (r *tenantEmbeddingModelResolver) IsMemoryEmbeddingModelConfigured(ctx context.Context, tenantID string) bool {
	if r == nil || r.getTenants == nil {
		return false
	}
	tenants := r.getTenants()
	if tenants == nil {
		return false
	}
	_, _, settings, err := tenants.GetSettings(ctx, tenantID)
	if err != nil {
		return false
	}
	raw, ok := settings["memory_embedding_model"]
	if !ok {
		return false
	}
	model, _ := raw.(string)
	return strings.TrimSpace(model) != ""
}

// seedMemoryEmbeddingModels 启动路径一次性回填存量租户的记忆嵌入模型配置：
// 对 tenants.settings 缺 memory_embedding_model 键（或空值）的租户，把当前
// 全局默认嵌入模型（ResolveDefaultEmbeddingModel）幂等回填。seed 是 P4
// fail-closed 上线前提——避免存量租户上线即全部对话失败（错误信息引导配置）。
// 租户列表失败返回 error 阻断启动（DB 系统性故障）；单租户回填失败记录
// error 并继续（该租户按未配置 fail-closed 处理，已配置租户绝不覆盖）。
func (c *Container) seedMemoryEmbeddingModels(ctx context.Context) error {
	// 无 DB/registry 的装配（test）跳过
	if c.IAM == nil || c.IAM.TenantRepo == nil || c.IAM.TenantService == nil ||
		c.LLMGateway == nil || c.LLMGateway.TenantEmbeddingResolver == nil {
		return nil
	}
	tenantIDs, err := c.IAM.TenantRepo.ListActiveTenantIDs(ctx)
	if err != nil {
		return fmt.Errorf("memory embedding seed: list tenants: %w", err)
	}
	defaultModel, err := c.LLMGateway.Registry.ResolveDefaultEmbeddingModel(ctx)
	if err != nil {
		return fmt.Errorf("memory embedding seed: resolve default model: %w", err)
	}
	if defaultModel == "" {
		c.Logger.Warn("memory embedding seed: no global default embedding model, skip")
		return nil
	}
	seeded := c.seedTenantEmbeddingModels(ctx, tenantIDs, defaultModel)
	c.Logger.Info("memory embedding seed: completed", zap.Int("seeded", seeded))
	return nil
}

// seedTenantEmbeddingModels 为每个未配置的存量租户回填默认嵌入模型，返回回填数。
func (c *Container) seedTenantEmbeddingModels(ctx context.Context, tenantIDs []string, defaultModel string) int {
	seeded := 0
	for _, tid := range tenantIDs {
		if c.LLMGateway.TenantEmbeddingResolver.IsMemoryEmbeddingModelConfigured(ctx, tid) {
			continue // 已配置,幂等跳过
		}
		if err := c.IAM.TenantService.SetSetting(ctx, tid, "memory_embedding_model", defaultModel); err != nil {
			c.Logger.Error("memory embedding seed: backfill failed",
				zap.String("tenant_id", tid), zap.Error(err))
			continue
		}
		seeded++
		c.Logger.Info("memory embedding seed: backfilled",
			zap.String("tenant_id", tid), zap.String("model", defaultModel))
	}
	return seeded
}
