package wiring

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

func TestBuildPlatformRequiresAuthConfigInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	c := &Container{
		Config: &config.Config{},
		Logger: zap.NewNop(),
		LLMGateway: &LLMGateway{
			Metrics: observability.NewPrometheusMetrics(zap.NewNop()),
		},
	}

	err := c.buildPlatform(context.Background())
	if err == nil {
		t.Fatal("expected production auth config error, got nil")
	}
}
