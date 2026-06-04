package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/config"
	harnesspkg "github.com/byteBuilderX/ClawHermes-AI-Go/internal/harness"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/hermes"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/migration"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/postgres"
	pkgredis "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/redis"
	"go.uber.org/zap"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load config", zap.Error(err))
	}

	// Initialize PostgreSQL
	ctx := context.Background()
	pgPool, err := postgres.New(ctx, cfg.PostgresURL, logger)
	if err != nil {
		logger.Fatal("failed to connect postgres", zap.Error(err))
	}
	defer pgPool.Close()

	// Initialize Redis
	redisClient, err := pkgredis.New(ctx, cfg.RedisURL, logger)
	if err != nil {
		logger.Fatal("failed to connect redis", zap.Error(err))
	}
	defer redisClient.Close() //nolint:errcheck

	// Run public schema migration
	if err := migration.RunPublicSchema(cfg.PostgresURL, "internal/migration/sql", logger); err != nil {
		logger.Fatal("migration failed", zap.Error(err))
	}

	// Create Harness for unified component lifecycle management
	appHarness := harnesspkg.New(logger)

	// Register components to Harness
	// 1. Infrastructure services
	services, err := config.InitializeServices(cfg, logger)
	if err != nil {
		logger.Fatal("Failed to initialize services", zap.Error(err))
	}
	defer services.Close() //nolint:errcheck

	// 2. Hermes event bus component
	var hermesClient *hermes.Client
	hermesComponent := harnesspkg.NewSimpleComponent("hermes", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			var err error
			hermesClient, err = hermes.NewClient(cfg.NatsURL, logger)
			if err != nil {
				logger.Warn("Failed to connect to NATS", zap.Error(err))
				// Don't fail startup
			} else {
				logger.Info("Connected to NATS", zap.String("url", cfg.NatsURL))
			}
			return nil
		}),
		harnesspkg.WithStopFunc(func(ctx context.Context) error {
			logger.Info("Disconnecting from NATS")
			if hermesClient != nil {
				hermesClient.Close()
			}
			return nil
		}),
		harnesspkg.WithHealthCheckFunc(func(ctx context.Context) error {
			// Simple health check: verify configured URL format
			if cfg.NatsURL == "" {
				return fmt.Errorf("NATS URL not configured")
			}
			return nil
		}),
	)
	if err := appHarness.Register(hermesComponent); err != nil {
		logger.Fatal("Failed to register Hermes component", zap.Error(err))
	}

	// 3. LLM Gateway component
	llmCfg := llmgateway.LoadConfig()
	gateway := llmgateway.InitializeGateway(llmCfg, logger)
	llmComponent := harnesspkg.NewSimpleComponent("llm-gateway", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			if err := gateway.Health(ctx); err != nil {
				logger.Warn("LLM gateway health check failed", zap.Error(err))
				return nil
			}
			return nil
		}),
		harnesspkg.WithStopFunc(func(ctx context.Context) error {
			logger.Info("LLM gateway stopped")
			return nil
		}),
		harnesspkg.WithHealthCheckFunc(func(ctx context.Context) error {
			return gateway.Health(ctx)
		}),
	)
	if err := appHarness.Register(llmComponent); err != nil {
		logger.Fatal("Failed to register LLM Gateway component", zap.Error(err))
	}

	// 4. Skill Registry component
	registry := orchestrator.NewRegistry(pgPool.DB())
	skillRegistryComponent := harnesspkg.NewSimpleComponent("skill-registry", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			logger.Info("Skill registry initialized")
			return nil
		}),
		harnesspkg.WithStopFunc(func(ctx context.Context) error {
			logger.Info("Skill registry stopped")
			return nil
		}),
		harnesspkg.WithHealthCheckFunc(func(ctx context.Context) error {
			// Simple check: registry is not nil
			if registry == nil {
				return fmt.Errorf("skill registry not initialized")
			}
			return nil
		}),
	)
	if err := appHarness.Register(skillRegistryComponent); err != nil {
		logger.Fatal("Failed to register Skill Registry component", zap.Error(err))
	}

	// 5. HTTP Server component
	router := api.SetupRouter(cfg, logger, registry, gateway, pgPool.DB(), redisClient.Client())
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	httpServer := harnesspkg.NewSimpleComponent("http-server", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			logger.Info("Starting HTTP server", zap.String("port", cfg.Port))

			// Start server (non-blocking)
			go func() {
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Error("HTTP server error", zap.Error(err))
				}
			}()

			return nil
		}),
		harnesspkg.WithStopFunc(func(ctx context.Context) error {
			logger.Info("Stopping HTTP server")

			shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			if err := srv.Shutdown(shutdownCtx); err != nil {
				logger.Error("HTTP server shutdown error", zap.Error(err))
				return err
			}
			return nil
		}),
		harnesspkg.WithHealthCheckFunc(func(ctx context.Context) error {
			// Check if port is configured for listening
			if cfg.Port == "" {
				return fmt.Errorf("server port not configured")
			}
			return nil
		}),
	)
	if err := appHarness.Register(httpServer); err != nil {
		logger.Fatal("Failed to register HTTP Server component", zap.Error(err))
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Capture signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start Harness and wait
	go func() {
		if err := appHarness.Run(ctx); err != nil {
			logger.Error("Harness run error", zap.Error(err))
		}
	}()

	// Wait for signal or context cancellation
	select {
	case <-sigChan:
		logger.Info("Received shutdown signal")
		cancel()
	case <-ctx.Done():
		logger.Info("Context cancelled")
	}

	logger.Info("Application shutting down")
}
