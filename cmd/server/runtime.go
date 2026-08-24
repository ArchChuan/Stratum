package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	harnesspkg "github.com/byteBuilderX/stratum/internal/platform/harness"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	postgresstorage "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

func withPostgresReadiness(
	base func(context.Context) map[string]error,
	checkDatabase func(context.Context) error,
) func(context.Context) map[string]error {
	return func(ctx context.Context) map[string]error {
		results := base(ctx)
		results["postgres"] = checkDatabase(ctx)
		return results
	}
}

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
	// OTEL_SAMPLING_RATIO 此前从不被读取（helm 注入 0.1 却从未生效）。
	// 修复后非 agent 根 span 按此值头采样；agent.execute 由 agentSampler
	// 恒保 100%（见 pkg/observability）。非法/越界值 warn 并回退 1.0，
	// 防止误配置导致全量丢弃。此为已确认的生产行为变更（默认降至 helm 0.1）。
	if r := os.Getenv("OTEL_SAMPLING_RATIO"); r != "" {
		if f, ok := parseSamplingRatio(r); ok {
			traceCfg.SamplingRatio = f
		} else {
			logger.Warn("invalid OTEL_SAMPLING_RATIO, falling back to 1.0", zap.String("value", r))
		}
	}
	initCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown, err := observability.InitOTelProvider(initCtx, traceCfg)
	if err != nil {
		logger.Warn("OTel init failed, tracing disabled", zap.Error(err))
		return nil
	}
	logger.Info("OTel tracing enabled")
	return shutdown
}

// parseSamplingRatio parses the OTEL_SAMPLING_RATIO value. Empty or invalid
// values return (1.0, false) so callers fall back to full sampling rather than
// risk a misconfigured value silently dropping all non-agent traces.
func parseSamplingRatio(raw string) (float64, bool) {
	if raw == "" {
		return 1.0, false
	}
	f, err := strconv.ParseFloat(raw, 64)
	// math.IsNaN 单独拦截：NaN 对 <=0/>1 两个比较都恒 false，直接通过会
	// 让 TraceIDRatioBased(NaN) 静默变成全量采样（防误配置的 fail-safe 被绕过）。
	if err != nil || math.IsNaN(f) || f <= 0 || f > 1 {
		return 1.0, false
	}
	return f, true
}

func BootstrapTenants(ctx context.Context, c *wiring.Container, logger *zap.Logger) error {
	if err := bootstrapTenantSchemas(ctx, c.DB(), logger, defaultTenantBootstrapDeps); err != nil {
		return err
	}
	// 管理面门禁（RequireDefaultTenant）与系统角色推导（DeriveSystemRole）
	// 比较的是默认租户的真实 id，而非字面 "tenant_default"。bootstrap 在
	// 路由装配（BuildContainer）之后运行，因此只能在这里解析并写入。
	// 解析失败 → 启动失败（fail closed），禁止带未知默认租户服务请求。
	defaultTenantID, err := tenantdb.ResolveDefaultTenantID(ctx, c.DB())
	if err != nil {
		return fmt.Errorf("resolve default tenant id: %w", err)
	}
	constants.SetResolvedDefaultTenantID(defaultTenantID)
	logger.Info("default tenant resolved", zap.String("tenant_id", defaultTenantID))
	return iampersistence.EnsureAdminUser(ctx, c.DB(), c.Config.AdminUsername, c.Config.AdminPassword, logger)
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

func Run(ctx context.Context, cfg *config.Config, c *wiring.Container, logger *zap.Logger) error {
	appHarness := harnesspkg.New(logger)
	registerHermes(appHarness, c, logger)
	registerMemoryPipeline(appHarness, c, logger)
	registerMemoryWorkers(appHarness, c, logger)
	registerChatCleanup(appHarness, c, logger)
	registerGuestReaper(appHarness, c, logger)
	registerWorkflowWorker(appHarness, c, logger)
	registerCollabWorker(appHarness, c, logger)
	registerSchedulerWorker(appHarness, c, logger)
	c.ReadinessCheck = withPostgresReadiness(appHarness.HealthCheck, func(ctx context.Context) error {
		db := c.DB()
		if db == nil {
			return fmt.Errorf("postgres not configured")
		}
		if err := db.Ping(ctx); err != nil {
			return fmt.Errorf("postgres ping: %w", err)
		}
		return postgresstorage.CheckDefaultTenantReadiness(ctx, db)
	})
	registerHTTPServer(appHarness, cfg, c, logger)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	runErr := make(chan error, 1)
	go func() { runErr <- appHarness.Run(ctx) }()

	select {
	case <-sigChan:
		logger.Info("Received shutdown signal")
		cancel()
	case <-ctx.Done():
		logger.Info("Context cancelled")
	case err := <-runErr:
		return normalizeHarnessError(err)
	}
	logger.Info("Application shutting down")
	return normalizeHarnessError(<-runErr)
}

func normalizeHarnessError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	return fmt.Errorf("run application harness: %w", err)
}

// trackedWorker tracks a worker goroutine so the component's Stop func can
// wait for it to exit within the shutdown budget. Workers exit when the run
// ctx (cancelled before Stop) fires; Stop receives a live bounded ctx.
type trackedWorker struct {
	wg sync.WaitGroup
}

// goRun launches run in a tracked goroutine. Safe to call from Start.
func (t *trackedWorker) goRun(ctx context.Context, run func(context.Context)) {
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		run(ctx)
	}()
}

// wait blocks until the tracked goroutine exits or the shutdown ctx expires.
func (t *trackedWorker) wait(ctx context.Context) error {
	return harnesspkg.WaitForGroup(ctx, &t.wg)
}

func registerWorkflowWorker(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	if c.Workflow == nil || c.Workflow.Worker == nil {
		return
	}
	var tw trackedWorker
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("workflow-worker", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			tw.goRun(ctx, func(runCtx context.Context) { c.Workflow.Worker.Run(runCtx, constants.WorkflowIdleInterval) })
			return nil
		}),
		harnesspkg.WithStopFunc(tw.wait),
		harnesspkg.WithHealthCheckFunc(func(context.Context) error { return nil }),
	), logger)
}

func registerCollabWorker(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	if c.Collab == nil || c.Collab.Worker == nil {
		return
	}
	var tw trackedWorker
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("collab-worker", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			tw.goRun(ctx, func(runCtx context.Context) { c.Collab.Worker.Run(runCtx, constants.CollabIdleInterval) })
			return nil
		}),
		harnesspkg.WithStopFunc(tw.wait),
		harnesspkg.WithHealthCheckFunc(func(context.Context) error { return nil }),
	), logger)
}

func registerSchedulerWorker(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	if c.Scheduler == nil || c.Scheduler.Worker == nil {
		return
	}
	var tw trackedWorker
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("scheduler-worker", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			tw.goRun(ctx, c.Scheduler.Worker.Start)
			return nil
		}),
		harnesspkg.WithStopFunc(tw.wait),
		harnesspkg.WithHealthCheckFunc(func(context.Context) error {
			last := c.Scheduler.Worker.LastPollAt()
			if last.IsZero() || time.Since(last) > 3*constants.SchedulerPollInterval {
				return fmt.Errorf("scheduler worker stale: last poll %v", last)
			}
			return nil
		}),
	), logger)
}

func registerHermes(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	start, stop, healthCheck := wiring.BuildHermesFuncs(c, logger)
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
	var tw trackedWorker
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("memory-workers", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			for _, w := range memWorkers {
				tw.goRun(ctx, w.Start)
			}
			logger.Info("Memory workers started", zap.Int("worker_count", len(memWorkers)))
			return nil
		}),
		harnesspkg.WithStopFunc(func(ctx context.Context) error {
			for _, w := range memWorkers {
				w.Stop()
			}
			logger.Info("Memory workers stopped")
			return tw.wait(ctx)
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
	var tw trackedWorker
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("chat-cleanup", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			db := c.DB()
			if db == nil || c.Agent == nil || c.Agent.ChatStore == nil {
				logger.Warn("chat-cleanup: no DB or ChatStore available, skipping")
				return nil
			}
			var metrics observability.MetricsProvider = observability.NoopMetrics{}
			if c.Platform != nil && c.Platform.Metrics != nil {
				metrics = c.Platform.Metrics
			}
			tw.goRun(ctx, func(runCtx context.Context) {
				runChatCleanup(runCtx, db, c.Agent.ChatStore, chatCleanupInterval, metrics, logger)
			})
			return nil
		}),
		harnesspkg.WithStopFunc(tw.wait),
	), logger)
}

// registerGuestReaper installs the background component that reaps expired
// guest accounts: for each expired guest it deletes every non-default tenant
// the guest owns (including the per-guest sandbox tenant provisioned at
// guest login), then hard-deletes the user (FK cascades clear membership +
// refresh tokens). Guests are never members of the default tenant, so reaping
// drops only the guest's own sandbox tenants.
func registerGuestReaper(appHarness *harnesspkg.Harness, c *wiring.Container, logger *zap.Logger) {
	var tw trackedWorker
	mustRegister(appHarness, harnesspkg.NewSimpleComponent("guest-reaper", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			if c.Platform == nil || c.Platform.OnboardSvc == nil || c.IAM == nil || c.IAM.AdminService == nil {
				logger.Warn("guest-reaper: OnboardSvc or AdminService unavailable, skipping")
				return nil
			}
			var metrics observability.MetricsProvider = observability.NoopMetrics{}
			if c.Platform.Metrics != nil {
				metrics = c.Platform.Metrics
			}
			tw.goRun(ctx, func(runCtx context.Context) {
				runGuestReaper(runCtx, c.Platform.OnboardSvc, c.IAM.AdminService, metrics, constants.GuestReaperInterval, logger)
			})
			return nil
		}),
		harnesspkg.WithStopFunc(tw.wait),
	), logger)
}

func runGuestReaper(ctx context.Context, onboard *iamapp.OnboardService, admin *iamapp.AdminService, metrics observability.MetricsProvider, interval time.Duration, logger *zap.Logger) {
	// Mark the component as started so the freshness alert does not treat the
	// pre-first-cycle window as "reaper down".
	metrics.SetReaperCycleTimestamp(float64(time.Now().Unix()))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("guest-reaper.panic",
							zap.Any("panic", r),
							zap.Stack("stack"))
						metrics.IncReaperCycle("panic")
						metrics.IncGoroutinePanic("reaper")
					}
				}()
				reapExpiredGuests(ctx, onboard, admin, metrics, logger)
			}()
		case <-ctx.Done():
			return
		}
	}
}

func reapExpiredGuests(ctx context.Context, onboard *iamapp.OnboardService, admin *iamapp.AdminService, metrics observability.MetricsProvider, logger *zap.Logger) {
	metrics.SetReaperCycleTimestamp(float64(time.Now().Unix()))
	guestIDs, err := onboard.ListExpiredGuests(ctx, time.Now())
	if err != nil {
		logger.Warn("guest-reaper: list expired guests", zap.Error(err))
		metrics.IncReaperCycle("error")
		metrics.IncReaperDeleteError("list")
		return
	}
	for _, userID := range guestIDs {
		reapGuest(ctx, userID, onboard, admin, metrics, logger)
	}
	metrics.IncReaperCycle("ok")
}

func reapGuest(ctx context.Context, userID string, onboard *iamapp.OnboardService, admin *iamapp.AdminService, metrics observability.MetricsProvider, logger *zap.Logger) {
	tenantIDs, err := onboard.ListOwnedNonDefaultTenants(ctx, userID)
	if err != nil {
		logger.Warn("guest-reaper: list owned tenants", zap.String("user_id", userID), zap.Error(err))
		metrics.IncReaperDeleteError("list_tenants")
		return
	}
	for _, tenantID := range tenantIDs {
		if err := admin.DeleteTenant(ctx, tenantID); err != nil {
			logger.Warn("guest-reaper: delete tenant", zap.String("tenant_id", tenantID), zap.Error(err))
			metrics.IncReaperDeleteError("delete_tenant")
		}
	}
	if err := onboard.DeleteUser(ctx, userID); err != nil {
		logger.Warn("guest-reaper: delete user", zap.String("user_id", userID), zap.Error(err))
		metrics.IncReaperDeleteError("delete_user")
		return
	}
	metrics.IncReaperGuestDeleted()
	logger.Info("guest-reaper: reaped expired guest", zap.String("user_id", userID), zap.Int("tenants_deleted", len(tenantIDs)))
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

func runChatCleanup(ctx context.Context, db *pgxpool.Pool, store chatCleaner, interval time.Duration, metrics observability.MetricsProvider, logger *zap.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("chat-cleanup.panic",
							zap.Any("panic", r),
							zap.Stack("stack"))
						metrics.IncComponentError("chat-cleanup", "panic")
						metrics.IncGoroutinePanic("chat-cleanup")
					}
				}()
				metrics.SetComponentCycleTimestamp("chat-cleanup", float64(time.Now().Unix()))
				rows, err := db.Query(ctx, `SELECT id::text FROM tenants WHERE deleted_at IS NULL`)
				if err != nil {
					logger.Warn("chat-cleanup: list tenants", zap.Error(err))
					metrics.IncComponentError("chat-cleanup", "list_tenants")
					return
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
						metrics.IncComponentError("chat-cleanup", "cleanup")
					}
				}
				metrics.RecordComponentCycle("chat-cleanup")
			}()
		case <-ctx.Done():
			return
		}
	}
}
