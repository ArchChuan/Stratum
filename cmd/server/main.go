package main

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
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

	// 配置中心（Nacos）：档位 A 业务/功能配置，Nacos 优先、env 兜底。
	// fail-closed：连接失败 WARN 后按 env/默认值启动，不阻断。
	// 必须位于 BuildContainer 之前——MemoryPipeline.Enabled 影响装配。
	// ConnectNacos 非幂等，main 只调一次，defer 中关闭。
	if err := cfg.ConnectNacos(logger); err != nil {
		logger.Warn("config: nacos unavailable, using env/fallback config", zap.Error(err))
	} else {
		defer func() { _ = cfg.CloseNacos() }()
	}

	if shutdown := InitTracingFromEnv(logger); shutdown != nil {
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

	if err := BootstrapTenants(ctx, container, logger); err != nil {
		logger.Fatal("tenant bootstrap failed", zap.Error(err))
	}
	container.RecoverStuckKnowledgeIngests(ctx)
	container.SyncBuiltinKnowledgeDocs(ctx)
	if err := Run(ctx, cfg, container, logger); err != nil {
		logger.Fatal("Application runtime failed", zap.Error(err))
	}
}
