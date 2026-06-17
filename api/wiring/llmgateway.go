package wiring

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// LLMGateway holds the application's LLM gateway and its tenant-scoped
// caches/resolvers. TenantCache and EmbedResolver are populated in
// later wiring tasks once memory/embedding deps are constructed.
//
// Metrics is the single shared PrometheusMetrics instance reused by
// downstream wiring (skill gateway, HTTP middleware) so all observability
// surfaces register against the same registry.
type LLMGateway struct {
	Gateway *llmgateway.Gateway
	Metrics *observability.PrometheusMetrics
}

func (c *Container) buildLLMGateway(_ context.Context) error {
	metrics := observability.NewPrometheusMetrics(c.Logger)
	gw := llmgateway.NewGateway().
		WithLogger(c.Logger).
		WithMetrics(metrics)
	c.LLMGateway = &LLMGateway{Gateway: gw, Metrics: metrics}
	return nil
}
