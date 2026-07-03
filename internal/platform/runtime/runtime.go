package runtime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	agentpersistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/internal/iam/infrastructure/hermes"
	harnesspkg "github.com/byteBuilderX/stratum/internal/platform/harness"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

const chatCleanupInterval = 24 * time.Hour

func InitTracingFromEnv(logger *zap.Logger) func(context.Context) error {
	ep := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if ep == "" {
		return nil
	}
	traceCfg := observability.DefaultTraceConfig()
	traceCfg.OTLPEndpoint = ep
	if sn := os.Getenv("OTEL_SERVICE_NAME"); sn != "" {
		traceCfg.ServiceName = sn
	}
	initCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown, err := observability.InitOTelProvider(initCtx, traceCfg)
	if err != nil {
		logger.Warn("OTel init failed, tracing disabled", zap.Error(err))
		return nil
	}
	logger.Info("OTel tracing enabled", zap.String("endpoint", ep))
	return shutdown
}

func BootstrapTenants(ctx context.Context, c *wiring.Container, logger *zap.Logger) error {
	if err := tenantdb.ProvisionPublicSchema(ctx, c.DB(), logger); err != nil {
		return fmt.Errorf("public schema provision: %w", err)
	}
	if err := tenantdb.EnsureDefaultTenant(ctx, c.DB(), logger); err != nil {
		return fmt.Errorf("ensure default tenant: %w", err)
	}
	if err := tenantdb.ProvisionAllTenantSchemas(ctx, c.DB(), logger); err != nil {
		logger.Warn("failed to provision tenant schemas", zap.Error(err))
	}
	return nil
}

func Run(ctx context.Context, cfg *config.Config, c *wiring.Container, logger *zap.Logger) {
	appHarness := harnesspkg.New(logger)
	registerHermes(appHarness, cfg, logger)
	registerMemoryPipeline(appHarness, c, logger)
	registerMemoryWorkers(appHarness, c, logger)
	registerChatCleanup(appHarness, c, logger)
	registerHTTPServer(appHarness, cfg, c, logger)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

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

func registerHermes(appHarness *harnesspkg.Harness, cfg *config.Config, logger *zap.Logger) {
	var hermesClient *hermes.Client
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("hermes", logger,
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
	), logger)
}

func registerMemoryPipeline(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("memory-pipeline", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			if c.Memory == nil || c.Memory.Pipeline == nil {
				logger.Info("Memory pipeline disabled, skipping")
				return nil
			}
			if err := c.Memory.Pipeline.Start(ctx); err != nil {
				logger.Warn("memory-pipeline: start failed", zap.Error(err))
			}
			return nil
		}),
		harnesspkg.WithStopFunc(func(_ context.Context) error {
			if c.Memory != nil && c.Memory.Pipeline != nil {
				c.Memory.Pipeline.Stop()
			}
			return nil
		}),
		harnesspkg.WithHealthCheckFunc(func(context.Context) error { return nil }),
	), logger)
}

func registerMemoryWorkers(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	memWorkers := wiring.BuildMemoryWorkers(c)
	if len(memWorkers) == 0 {
		return
	}
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("memory-workers", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			for _, w := range memWorkers {
				go w.Start(ctx)
			}
			logger.Info("Memory workers started", zap.Int("worker_count", len(memWorkers)))
			return nil
		}),
		harnesspkg.WithStopFunc(func(context.Context) error {
			for _, w := range memWorkers {
				w.Stop()
			}
			logger.Info("Memory workers stopped")
			return nil
		}),
		harnesspkg.WithHealthCheckFunc(func(context.Context) error { return nil }),
	), logger)
}

func registerChatCleanup(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("chat-cleanup", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			db := c.DB()
			if db == nil {
				logger.Warn("chat-cleanup: no DB available, skipping")
				return nil
			}
			go runChatCleanup(ctx, db, chatCleanupInterval, logger)
			return nil
		}),
		harnesspkg.WithStopFunc(func(context.Context) error { return nil }),
	), logger)
}

func registerHTTPServer(appHarness *harnesspkg.Harness, cfg *config.Config, c *wiring.Container, logger *zap.Logger) {
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           apihttp.NewRouter(c),
		ReadHeaderTimeout: constants.HTTPReadHeaderTimeout,
	}
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("http-server", logger,
		harnesspkg.WithStartFunc(func(context.Context) error {
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
			shutdownCtx, cancel := context.WithTimeout(ctx, constants.HTTPShutdownTimeout)
			defer cancel()
			return srv.Shutdown(shutdownCtx)
		}),
		harnesspkg.WithHealthCheckFunc(func(context.Context) error {
			if cfg.Port == "" {
				return fmt.Errorf("server port not configured")
			}
			return nil
		}),
	), logger)
}

func mustRegister(h *harnesspkg.Harness, c harnesspkg.Component, logger *zap.Logger) {
	if err := h.Register(c); err != nil {
		logger.Fatal("register component", zap.String("component", c.Name()), zap.Error(err))
	}
}

func runChatCleanup(ctx context.Context, db *pgxpool.Pool, interval time.Duration, logger *zap.Logger) {
	chatStore := agentpersistence.NewPgChatStore(db, logger)
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
			var tenantIDs []string
			for rows.Next() {
				var tenantID string
				if err := rows.Scan(&tenantID); err == nil {
					tenantIDs = append(tenantIDs, tenantID)
				}
			}
			rows.Close()
			for _, tenantID := range tenantIDs {
				if err := chatStore.CleanupExpired(ctx, tenantID); err != nil {
					logger.Warn("chat-cleanup: cleanup tenant", zap.String("tenant_id", tenantID), zap.Error(err))
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
