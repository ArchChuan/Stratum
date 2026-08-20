package wiring

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// LLMGateway holds the application's LLM gateway and its tenant-scoped
// caches/resolvers. TenantCache and EmbedResolver are populated in
// later wiring tasks once memory/embedding deps are constructed.
//
// Metrics is the single shared PrometheusMetrics instance reused by
// downstream wiring (skill gateway, HTTP middleware) so all observability
// surfaces register against the same registry.
//
// Registry exposes the ModelRegistry to downstream sub-builders (agent
// resolver, embed resolvers) for tenant-scoped model resolution.
//
// ModelService surfaces the model catalogue to HTTP handlers without
// leaking the infrastructure type across layers.
//
// ProviderService and ModelMgmtService expose tenant-scoped admin CRUD
// operations to HTTP handlers for provider and model management.
type LLMGateway struct {
	Gateway          *llmgateway.Gateway
	Metrics          *observability.PrometheusMetrics
	ModelService     *llmapp.ModelService
	Registry         *llmgateway.ModelRegistry
	ProviderService  *llmapp.ProviderService
	ModelMgmtService *llmapp.ModelMgmtService
	// TenantEmbeddingResolver 解析租户显式配置的记忆嵌入模型
	// （tenants.settings.memory_embedding_model），fail-closed。iam 构建晚于
	// llmgateway，内部延迟绑定 c.IAM.TenantService。
	TenantEmbeddingResolver *tenantEmbeddingModelResolver
}

func (c *Container) buildLLMGateway(ctx context.Context) error {
	db := c.DB()
	if db == nil {
		// c.LLMGateway remains nil when db is unavailable.
		// Downstream consumers (knowledge, memory, agent resolvers) must
		// guard against nil c.LLMGateway and nil c.LLMGateway.Registry.
		return nil
	}
	metrics := observability.NewPrometheusMetrics(c.Logger)
	// cmd/server 是唯一运行 guest reaper 的进程；只有它导出 reaper 指标。
	metrics.RegisterReaperMetrics()

	// 平台级模型健康 registry：协议层调用结果被动同步 + 探活 worker 主动
	// 驱动；ModelRegistry 据此做健康感知解析（unhealthy 模型视为未命中
	// 继续降级，fail-closed 不选中熔断模型）。
	health := llmgateway.NewHealthRegistry(nil).WithMetrics(metrics).WithLogger(c.Logger)

	// Protocol singletons:
	// - OpenAICompatProtocol wraps an OpenAICompatClient, satisfies ChatProtocol + EmbedProtocol.
	// - OllamaProtocol wraps an OllamaClient, satisfies ChatProtocol + EmbedProtocol.
	// - AnthropicProtocol wraps an AnthropicClient, satisfies ChatProtocol.
	openAICompatClient := llmgateway.NewOpenAICompatClient(
		llmgateway.ProviderConfig{Name: "openai-compat"},
		c.Logger,
	)
	openAICompatProto := llmgateway.NewOpenAICompatProtocol(openAICompatClient).WithHealth(health)

	ollamaClient := llmgateway.NewOllamaClient(
		llmgateway.ProviderConfig{Name: "ollama"},
		c.Logger,
	)
	ollamaProto := llmgateway.NewOllamaProtocol(ollamaClient).WithHealth(health)

	anthropicClient := llmgateway.NewAnthropicClient(
		llmgateway.ProviderConfig{Name: "anthropic"},
		c.Logger,
	)
	anthropicProto := llmgateway.NewAnthropicProtocol(anthropicClient).WithHealth(health)

	chatProtos := map[domain.ProviderKind]llmgateway.ChatProtocol{
		domain.ProviderOpenAICompat: openAICompatProto,
		domain.ProviderOllama:       ollamaProto,
		domain.ProviderAnthropic:    anthropicProto,
	}
	embedProtos := map[domain.ProviderKind]llmgateway.EmbedProtocol{
		domain.ProviderOpenAICompat: openAICompatProto,
		domain.ProviderOllama:       ollamaProto,
	}

	modelRepo := llmgateway.NewPgModelRepo(db)
	// at-rest 密钥独立于 JWT 签名密钥；两者皆空时 fail closed，
	// 禁止以 sha256("") 公开常量密钥加密 provider API key。
	aesKey, err := pkgcrypto.ResolveDataKey(c.Config.DataEncryptionKey, c.Config.JWTPrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("build llmgateway: %w", err)
	}
	providerRepo := llmgateway.NewPgProviderRepo(db, aesKey, c.Logger, metrics)
	registry := llmgateway.NewModelRegistry(
		modelRepo, providerRepo,
		chatProtos, embedProtos,
		constants.GatewayCacheTTL,
	).WithHealth(health)
	// 启动期预热全局目录一次；失败仅 WARN 不阻断——解析链内置 ②③④ 兜底
	// 与 ⑤ fail-closed，运行时按需从 DB 惰性解析。
	if err := registry.Warm(ctx); err != nil {
		c.Logger.Warn("llmgateway.registry.warm.failed", zap.Error(err))
	}
	gw := llmgateway.NewGateway(registry, chatProtos, embedProtos).
		WithLogger(c.Logger).WithMetrics(metrics)

	// 探活 worker：后台周期驱动 enabled 模型的主动健康信号，与被动熔断
	// 状态机同步演进。Start 后随 ctx 退出，逆序关闭时 Stop 等待 goroutine。
	prober := llmgateway.NewModelProber(modelRepo, providerRepo, chatProtos, embedProtos, health, c.Logger)
	prober.Start(ctx)
	c.shutdown = append(c.shutdown, func(context.Context) error {
		prober.Stop()
		return nil
	})

	providerSvc := llmapp.NewProviderService(
		providerRepo, modelRepo, llmgateway.NewProviderRuntime(chatProtos), registry,
	)
	// 模型管理目录响应附加运行时健康状态（平台级 HealthRegistry 投影）；
	// health 为 nil（db 不可用的测试装配）时目录不带 health 字段。
	mgmtSvc := llmapp.NewModelMgmtService(modelRepo, registry).WithHealth(health)

	c.LLMGateway = &LLMGateway{
		Gateway:          gw,
		Metrics:          metrics,
		ModelService:     llmapp.NewModelService(registry),
		Registry:         registry,
		ProviderService:  providerSvc,
		ModelMgmtService: mgmtSvc,
		TenantEmbeddingResolver: newTenantEmbeddingModelResolver(func() *iamapp.TenantService {
			if c.IAM == nil {
				return nil
			}
			return c.IAM.TenantService
		}, registry, c.Logger),
	}
	return nil
}
