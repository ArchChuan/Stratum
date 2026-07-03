package main

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	platformruntime "github.com/byteBuilderX/stratum/internal/platform/runtime"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: could not load .env file: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

	if shutdown := platformruntime.InitTracingFromEnv(logger); shutdown != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), constants.HTTPShutdownTimeout)
			defer cancel()
			_ = shutdown(ctx)
		}()
	}

	ctx := context.Background()
	container, err := wiring.BuildContainer(ctx, cfg, logger)
	if err != nil {
		logger.Fatal("BuildContainer failed", zap.Error(err))
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), constants.HTTPShutdownTimeout)
		defer cancel()
		if err := container.Shutdown(ctx); err != nil {
			logger.Error("Container shutdown error", zap.Error(err))
		}
	}()

	if err := platformruntime.BootstrapTenants(ctx, container, logger); err != nil {
		logger.Fatal("tenant bootstrap failed", zap.Error(err))
	}
	platformruntime.Run(ctx, cfg, container, logger)
}
