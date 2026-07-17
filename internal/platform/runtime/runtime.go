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
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	harnesspkg "github.com/byteBuilderX/stratum/internal/platform/harness"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

const chatCleanupInterval = 24 * time.Hour

type tenantBootstrapDeps struct {
	withLock        func(context.Context, *pgxpool.Pool, func(context.Context) error) error
	provisionPublic func(context.Context, *pgxpool.Pool, *zap.Logger) error
	ensureDefault   func(context.Context, *pgxpool.Pool, *zap.Logger) error
	provisionAll    func(context.Context, *pgxpool.Pool, *zap.Logger) error
}

var defaultTenantBootstrapDeps = tenantBootstrapDeps{
	withLock:        tenantdb.WithSchemaProvisionLock,
	provisionPublic: tenantdb.ProvisionPublicSchema,
	ensureDefault:   tenantdb.EnsureDefaultTenant,
	provisionAll:    tenantdb.ProvisionAllTenantSchemas,
}

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
	return bootstrapTenantSchemas(ctx, c.DB(), logger, defaultTenantBootstrapDeps)
}

func bootstrapTenantSchemas(
	ctx context.Context,
	pool *pgxpool.Pool,
	logger *zap.Logger,
	deps tenantBootstrapDeps,
) error {
	return deps.withLock(ctx, pool, func(lockCtx context.Context) error {
		if err := deps.provisionPublic(lockCtx, pool, logger); err != nil {
			return fmt.Errorf("public schema provision: %w", err)
		}
		if err := deps.ensureDefault(lockCtx, pool, logger); err != nil {
			return fmt.Errorf("ensure default tenant: %w", err)
		}
		if err := deps.provisionAll(lockCtx, pool, logger); err != nil {
			return fmt.Errorf("provision tenant schemas: %w", err)
		}
		return nil
	})
}

func Run(ctx context.Context, cfg *config.Config, c *wiring.Container, logger *zap.Logger) {
	appHarness := harnesspkg.New(logger)
	registerHermes(appHarness, cfg, logger)
	registerMemoryPipeline(appHarness, c, logger)
	registerMemoryWorkers(appHarness, c, logger)
	registerChatCleanup(appHarness, c, logger)
	registerGuestReaper(appHarness, c, logger)
	registerWorkflowWorker(appHarness, c, logger)
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

func registerWorkflowWorker(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	if c.Workflow == nil || c.Workflow.Worker == nil {
		return
	}
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("workflow-worker", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error { go c.Workflow.Worker.Run(ctx, 250*time.Millisecond); return nil }),
		harnesspkg.WithStopFunc(func(context.Context) error { return nil }),
		harnesspkg.WithHealthCheckFunc(func(context.Context) error { return nil }),
	), logger)
}

func registerHermes(appHarness *harnesspkg.Harness, cfg *config.Config, logger *zap.Logger) {
	start, stop, healthCheck := wiring.BuildHermesFuncs(cfg, logger)
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("hermes", logger,
		harnesspkg.WithStartFunc(start),
		harnesspkg.WithStopFunc(stop),
		harnesspkg.WithHealthCheckFunc(healthCheck),
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

// chatCleaner is a local duck-typed seam so platform/runtime avoids
// importing agent/infrastructure directly.
type chatCleaner interface {
	CleanupExpired(ctx context.Context, tenantID string) error
}

func registerChatCleanup(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("chat-cleanup", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			db := c.DB()
			if db == nil || c.Agent == nil || c.Agent.ChatStore == nil {
				logger.Warn("chat-cleanup: no DB or ChatStore available, skipping")
				return nil
			}
			go runChatCleanup(ctx, db, c.Agent.ChatStore, chatCleanupInterval, logger)
			return nil
		}),
		harnesspkg.WithStopFunc(func(context.Context) error { return nil }),
	), logger)
}

// registerGuestReaper installs the background component that reaps expired
// guest accounts: for each expired guest it deletes every non-default tenant
// the guest owns, then hard-deletes the user (FK cascades clear membership +
// refresh tokens). Removing a guest is thus equivalent to evicting the member
// from the default tenant plus dropping tenants the guest created.
func registerGuestReaper(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("guest-reaper", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			if c.Platform == nil || c.Platform.OnboardSvc == nil || c.IAM == nil || c.IAM.AdminService == nil {
				logger.Warn("guest-reaper: OnboardSvc or AdminService unavailable, skipping")
				return nil
			}
			go runGuestReaper(ctx, c.Platform.OnboardSvc, c.IAM.AdminService, constants.GuestReaperInterval, logger)
			return nil
		}),
		harnesspkg.WithStopFunc(func(context.Context) error { return nil }),
	), logger)
}

func runGuestReaper(ctx context.Context, onboard *iamapp.OnboardService, admin *iamapp.AdminService, interval time.Duration, logger *zap.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			guestIDs, err := onboard.ListExpiredGuests(ctx, time.Now())
			if err != nil {
				logger.Warn("guest-reaper: list expired guests", zap.Error(err))
				continue
			}
			for _, userID := range guestIDs {
				tenantIDs, err := onboard.ListOwnedNonDefaultTenants(ctx, userID)
				if err != nil {
					logger.Warn("guest-reaper: list owned tenants", zap.String("user_id", userID), zap.Error(err))
					continue
				}
				for _, tenantID := range tenantIDs {
					if err := admin.DeleteTenant(ctx, tenantID); err != nil {
						logger.Warn("guest-reaper: delete tenant", zap.String("tenant_id", tenantID), zap.Error(err))
					}
				}
				if err := onboard.DeleteUser(ctx, userID); err != nil {
					logger.Warn("guest-reaper: delete user", zap.String("user_id", userID), zap.Error(err))
					continue
				}
				logger.Info("guest-reaper: reaped expired guest", zap.String("user_id", userID), zap.Int("tenants_deleted", len(tenantIDs)))
			}
		case <-ctx.Done():
			return
		}
	}
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

func runChatCleanup(ctx context.Context, db *pgxpool.Pool, store chatCleaner, interval time.Duration, logger *zap.Logger) {
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
				if err := store.CleanupExpired(ctx, tenantID); err != nil {
					logger.Warn("chat-cleanup: cleanup tenant", zap.String("tenant_id", tenantID), zap.Error(err))
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
