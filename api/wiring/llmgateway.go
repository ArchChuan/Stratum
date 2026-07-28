package wiring

import (
	"context"
	"fmt"

	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// LLMGateway holds the application's LLM gateway and the shared model
// registry. ModelRegistry is created here (before platform wiring) so
// downstream steps that need per-tenant model resolution (tenant resolver,
// knowledge, memory) can reference it from Platform.
//
// Metrics is the single shared PrometheusMetrics instance reused by
// downstream wiring (skill gateway, HTTP middleware) so all observability
// surfaces register against the same registry.
//
// ModelService surfaces the model catalogue to HTTP handlers without
// leaking the infrastructure type across layers.
type LLMGateway struct {
	Gateway       *llmgateway.Gateway
	ModelRegistry *llmgateway.ModelRegistry
	Metrics       *observability.PrometheusMetrics
	ModelService  *llmapp.ModelService
}

func (c *Container) buildLLMGateway(_ context.Context) error {
	if c.Storage == nil || c.Storage.PG == nil {
		return fmt.Errorf("wiring.llmgateway: storage not available")
	}

	metrics := observability.NewPrometheusMetrics(c.Logger)

	// Build tenant settings reader for ModelRegistry.
	db := c.Storage.PG.DB()
	readSettings := func(ctx context.Context, tenantID string) ([]byte, error) {
		var raw []byte
		err := db.QueryRow(ctx,
			"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
			tenantID,
		).Scan(&raw)
		return raw, err
	}
	aesKey := pkgcrypto.DeriveAESKey(c.Config.JWTPrivateKeyPEM)
	reg := llmgateway.NewModelRegistry(readSettings, aesKey, c.Logger)

	gw := llmgateway.NewGateway(reg).WithLogger(c.Logger).WithMetrics(metrics)
	c.LLMGateway = &LLMGateway{
		Gateway:       gw,
		ModelRegistry: reg,
		Metrics:       metrics,
		ModelService:  llmapp.NewModelService(llmgateway.StaticModelCatalog{}),
	}
	return nil
}
