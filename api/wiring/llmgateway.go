package wiring

import (
	"context"

	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
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
// ModelService surfaces the model catalogue to HTTP handlers without
// leaking the infrastructure type across layers.
type LLMGateway struct {
	Gateway      *llmgateway.Gateway
	Metrics      *observability.PrometheusMetrics
	ModelService *llmapp.ModelService
}

func (c *Container) buildLLMGateway(_ context.Context) error {
	metrics := observability.NewPrometheusMetrics(c.Logger)
	gw := llmgateway.NewGateway().WithLogger(c.Logger).WithMetrics(metrics)
	c.LLMGateway = &LLMGateway{
		Gateway:      gw,
		Metrics:      metrics,
		ModelService: llmapp.NewModelService(llmgateway.StaticModelCatalog{}),
	}
	return nil
}
