package wiring

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// LLMGateway holds the application's LLM gateway and its tenant-scoped
// caches/resolvers. TenantCache and EmbedResolver are populated in
// later wiring tasks once memory/embedding deps are constructed.
type LLMGateway struct {
	Gateway *llmgateway.Gateway
}

func (c *Container) buildLLMGateway(_ context.Context) error {
	gw := llmgateway.NewGateway().
		WithLogger(c.Logger).
		WithMetrics(observability.NewPrometheusMetrics(c.Logger))
	c.LLMGateway = &LLMGateway{Gateway: gw}
	return nil
}
