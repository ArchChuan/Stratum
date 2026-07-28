package wiring

import (
	"context"

	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/constants"
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
type LLMGateway struct {
	Gateway      *llmgateway.Gateway
	Metrics      *observability.PrometheusMetrics
	ModelService *llmapp.ModelService
	Registry     *llmgateway.ModelRegistry
}

func (c *Container) buildLLMGateway(_ context.Context) error {
	db := c.DB()
	if db == nil {
		return nil // no database — skip gateway building
	}
	metrics := observability.NewPrometheusMetrics(c.Logger)

	// Protocol singletons — OpenAICompatProtocol wraps an OpenAICompatClient
	// and satisfies both ChatProtocol and EmbedProtocol.
	openAICompatClient := llmgateway.NewOpenAICompatClient(
		llmgateway.ProviderConfig{Name: "openai-compat"},
		c.Logger,
	)
	openAICompatProto := llmgateway.NewOpenAICompatProtocol(openAICompatClient)

	chatProtos := map[domain.ProviderKind]llmgateway.ChatProtocol{
		domain.ProviderOpenAICompat: openAICompatProto,
	}
	embedProtos := map[domain.ProviderKind]llmgateway.EmbedProtocol{
		domain.ProviderOpenAICompat: openAICompatProto,
	}

	modelRepo := llmgateway.NewPgModelRepo(db)
	providerRepo := llmgateway.NewPgProviderRepo(db)
	registry := llmgateway.NewModelRegistry(
		modelRepo, providerRepo,
		chatProtos, embedProtos,
		constants.GatewayCacheTTL,
	)
	gw := llmgateway.NewGateway(registry, chatProtos, embedProtos).
		WithLogger(c.Logger).WithMetrics(metrics)

	c.LLMGateway = &LLMGateway{
		Gateway:      gw,
		Metrics:      metrics,
		ModelService: llmapp.NewModelService(registry),
		Registry:     registry,
	}
	return nil
}
