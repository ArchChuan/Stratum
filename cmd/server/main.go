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

	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"github.com/jackc/pgx/v5/pgxpool"

	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/wiring"
	agentpkg "github.com/byteBuilderX/stratum/internal/agent"
	"github.com/byteBuilderX/stratum/internal/hermes"
	"github.com/byteBuilderX/stratum/internal/platform/config"
	harnesspkg "github.com/byteBuilderX/stratum/internal/platform/harness"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/migration"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"

	_ "github.com/golang-migrate/migrate/v4/database/postgres"
)

func main() {
	const chatCleanupInterval = 24 * time.Hour

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Public schema migration uses its own connection (golang-migrate);
	// must run before BuildContainer opens the shared pool.
	if err := migration.RunPublicSchema(cfg.PostgresURL, "internal/migration/sql", logger); err != nil {
		logger.Fatal("migration failed", zap.Error(err))
	}

	container, err := wiring.BuildContainer(ctx, cfg, logger)
	if err != nil {
		logger.Fatal("BuildContainer failed", zap.Error(err))
	}
	defer func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), constants.HTTPShutdownTimeout)
		defer c()
		if err := container.Shutdown(shutdownCtx); err != nil {
			logger.Error("Container shutdown error", zap.Error(err))
		}
	}()

	// Tenant bootstrap on the container's pool.
	if err := tenantdb.EnsureDefaultTenant(ctx, container.DB(), logger); err != nil {
		logger.Fatal("failed to ensure default tenant", zap.Error(err))
	}
	if err := tenantdb.ProvisionAllTenantSchemas(ctx, container.DB(), logger); err != nil {
		logger.Warn("failed to provision tenant schemas", zap.Error(err))
	}

	appHarness := harnesspkg.New(logger)

	// 1. Hermes event bus — independent NATS connection by design.
	var hermesClient *hermes.Client
	if err := appHarness.Register(harnesspkg.NewSimpleComponent("hermes", logger,
		harnesspkg.WithStartFunc(func(_ context.Context) error {
			c, err := hermes.NewClient(cfg.NatsURL, logger)
			if err != nil {
				logger.Warn("Failed to connect to NATS", zap.Error(err))
				return nil
			}
			hermesClient = c
			logger.Info("Connected to NATS", zap.String("url", cfg.NatsURL))
			return nil
		}),
		harnesspkg.WithStopFunc(func(_ context.Context) error {
			if hermesClient != nil {
				hermesClient.Close()
			}
			return nil
		}),
		harnesspkg.WithHealthCheckFunc(func(_ context.Context) error {
			if cfg.NatsURL == "" {
				return fmt.Errorf("NATS URL not configured")
			}
			return nil
		}),
	)); err != nil {
		logger.Fatal("register hermes", zap.Error(err))
	}

	// 2. LLM gateway health.
	gateway := container.LLMGateway.Gateway
	if err := appHarness.Register(harnesspkg.NewSimpleComponent("llm-gateway", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			if err := gateway.Health(ctx); err != nil {
				logger.Warn("LLM gateway health check failed", zap.Error(err))
			}
			return nil
		}),
		harnesspkg.WithStopFunc(func(_ context.Context) error { return nil }),
		harnesspkg.WithHealthCheckFunc(func(ctx context.Context) error { return gateway.Health(ctx) }),
	)); err != nil {
		logger.Fatal("register llm-gateway", zap.Error(err))
	}

	// 3. Memory pipeline — wiring constructed it; Harness owns lifecycle.
	if err := appHarness.Register(harnesspkg.NewSimpleComponent("memory-pipeline", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			if container.Memory == nil || container.Memory.Pipeline == nil {
				logger.Info("Memory pipeline disabled, skipping")
				return nil
			}
			if err := container.Memory.Pipeline.Start(ctx); err != nil {
				logger.Warn("memory-pipeline: start failed", zap.Error(err))
			}
			return nil
		}),
		harnesspkg.WithStopFunc(func(_ context.Context) error {
			if container.Memory != nil && container.Memory.Pipeline != nil {
				container.Memory.Pipeline.Stop()
			}
			return nil
		}),
		harnesspkg.WithHealthCheckFunc(func(_ context.Context) error { return nil }),
	)); err != nil {
		logger.Fatal("register memory-pipeline", zap.Error(err))
	}

	// 4. Chat cleanup — daily prune of inactive conversations.
	if err := appHarness.Register(harnesspkg.NewSimpleComponent("chat-cleanup", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			db := container.DB()
			if db == nil {
				logger.Warn("chat-cleanup: no DB available, skipping")
				return nil
			}
			go runChatCleanup(ctx, db, chatCleanupInterval, logger)
			return nil
		}),
		harnesspkg.WithStopFunc(func(_ context.Context) error { return nil }),
	)); err != nil {
		logger.Fatal("register chat-cleanup", zap.Error(err))
	}

	// 5. HTTP server — apihttp.NewRouter wires gin from the container.
	router := apihttp.NewRouter(container)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: constants.HTTPReadHeaderTimeout,
	}
	if err := appHarness.Register(harnesspkg.NewSimpleComponent("http-server", logger,
		harnesspkg.WithStartFunc(func(_ context.Context) error {
			logger.Info("Starting HTTP server", zap.String("port", cfg.Port))
			go func() {
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Error("HTTP server error", zap.Error(err))
				}
			}()
			return nil
		}),
		harnesspkg.WithStopFunc(func(ctx context.Context) error {
			logger.Info("Stopping HTTP server")
			shutdownCtx, c := context.WithTimeout(ctx, constants.HTTPShutdownTimeout)
			defer c()
			return srv.Shutdown(shutdownCtx)
		}),
		harnesspkg.WithHealthCheckFunc(func(_ context.Context) error {
			if cfg.Port == "" {
				return fmt.Errorf("server port not configured")
			}
			return nil
		}),
	)); err != nil {
		logger.Fatal("register http-server", zap.Error(err))
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := appHarness.Run(ctx); err != nil {
			logger.Error("Harness run error", zap.Error(err))
		}
	}()

	select {
	case <-sigChan:
		logger.Info("Received shutdown signal")
		cancel()
	case <-ctx.Done():
		logger.Info("Context cancelled")
	}

	logger.Info("Application shutting down")
}

// runChatCleanup periodically prunes expired conversations across all tenants.
// Exits when ctx is cancelled.
func runChatCleanup(ctx context.Context, db *pgxpool.Pool, interval time.Duration, logger *zap.Logger) {
	chatStore := agentpkg.NewPgChatStore(db)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rows, err := db.Query(ctx, `SELECT id::text FROM tenants WHERE deleted_at IS NULL`)
			if err != nil {
				logger.Warn("chat-cleanup: list tenants", zap.Error(err))
				continue
			}
			var tids []string
			for rows.Next() {
				var tid string
				if err := rows.Scan(&tid); err == nil {
					tids = append(tids, tid)
				}
			}
			rows.Close()
			for _, tid := range tids {
				if err := chatStore.CleanupExpired(ctx, tid); err != nil {
					logger.Warn("chat-cleanup: cleanup tenant",
						zap.String("tenant_id", tid), zap.Error(err))
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
