package main

import (
	"context"
	"os"
	"time"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
)

const (
	platformMCPServiceName = "stratum-platform-mcp"
	tracingInitTimeout     = 5 * time.Second
)

func initTracing(logger *zap.Logger) func(context.Context) error {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return nil
	}
	cfg := observability.DefaultTraceConfig()
	cfg.OTLPEndpoint = endpoint
	cfg.ServiceName = platformMCPServiceName
	cfg.Environment = os.Getenv("APP_ENV")
	ctx, cancel := context.WithTimeout(context.Background(), tracingInitTimeout)
	defer cancel()
	shutdown, err := observability.InitOTelProvider(ctx, cfg)
	if err != nil {
		logger.Warn("platform_mcp.tracing.disabled", zap.Error(err))
		return nil
	}
	logger.Info("platform_mcp.tracing.enabled")
	return shutdown
}
