package wiring

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	parametersapp "github.com/byteBuilderX/stratum/internal/parameters/application"
)

// errMemoryEmbeddingNotConfigured 表示平台参数 memory.embedding_model 未配置。
// 作为 fail-closed 信号返回：调用方不得回退到任何全局默认模型，而应让该链路
// 明确失败（记忆对话失败 / 消息进 DLQ / 不建 collection）并走告警。
var errMemoryEmbeddingNotConfigured = errors.New(
	"memory embedding: platform parameter memory.embedding_model not configured; please set it in the platform settings page")

// tenantEmbeddingModelResolver 解析平台参数 memory.embedding_model（全局，
// 存于 public.platform_settings，热更新），fail-closed：未配置、配置值为空或
// 目录校验失败一律返回 error，绝不回退全局默认模型。
//
// llmgateway 在 parameters 之前构建，因此通过 params 延迟绑定运行时才取
// parameters.Service；nil-safe（参数服务未装配时返回 error，而非 panic）。
type tenantEmbeddingModelResolver struct {
	params   func() *parametersapp.Service
	registry *llmgateway.ModelRegistry
	logger   *zap.Logger
}

func newTenantEmbeddingModelResolver(
	params func() *parametersapp.Service,
	registry *llmgateway.ModelRegistry,
	logger *zap.Logger,
) *tenantEmbeddingModelResolver {
	return &tenantEmbeddingModelResolver{
		params:   params,
		registry: registry,
		logger:   logger,
	}
}

// ResolveMemoryEmbeddingModel 返回平台参数配置的记忆嵌入模型名。校验顺序：
// parameters 服务可用 → 平台参数存在且非空 → 目录可解析（须为 embedding 能力
// 模型）→ 返回模型名。任何一步失败返回 error（fail-closed）。tenantID 保留在
// 签名中维持消费方接口不变，全局参数下不参与解析。
func (r *tenantEmbeddingModelResolver) ResolveMemoryEmbeddingModel(ctx context.Context, _ string) (string, error) {
	if r == nil || r.params == nil || r.registry == nil {
		return "", errMemoryEmbeddingNotConfigured
	}
	svc := r.params()
	if svc == nil || svc.Resolver() == nil {
		return "", errMemoryEmbeddingNotConfigured
	}
	raw, ok, err := svc.Resolver().Resolve(ctx, "memory.embedding_model", nil)
	if err != nil {
		return "", fmt.Errorf("memory embedding: resolve platform parameter: %w", err)
	}
	model, _ := raw.(string)
	model = strings.TrimSpace(model)
	if !ok || model == "" {
		return "", errMemoryEmbeddingNotConfigured
	}
	if _, _, err := r.registry.ResolveEmbeddingExact(ctx, model); err != nil {
		return "", fmt.Errorf("memory embedding: resolve model %q: %w", model, err)
	}
	return model, nil
}
