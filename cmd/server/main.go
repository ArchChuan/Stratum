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

	"encoding/json"

	"github.com/joho/godotenv"

	"github.com/byteBuilderX/stratum/api"
	agentpkg "github.com/byteBuilderX/stratum/internal/agent"
	"github.com/byteBuilderX/stratum/internal/capgateway"
	"github.com/byteBuilderX/stratum/internal/config"
	"github.com/byteBuilderX/stratum/internal/embedding"
	harnesspkg "github.com/byteBuilderX/stratum/internal/harness"
	"github.com/byteBuilderX/stratum/internal/hermes"
	"github.com/byteBuilderX/stratum/internal/llmgateway"
	mempipeline "github.com/byteBuilderX/stratum/internal/memory/pipeline"
	"github.com/byteBuilderX/stratum/internal/migration"
	"github.com/byteBuilderX/stratum/internal/skill"
	"github.com/byteBuilderX/stratum/internal/skillgateway"
	"github.com/byteBuilderX/stratum/internal/skillgateway/providers"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/postgres"
	pkgredis "github.com/byteBuilderX/stratum/pkg/redis"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

func main() {
	const chatCleanupInterval = 24 * time.Hour
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: could not load .env file: %v", err)
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

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

	// Ensure the global default tenant exists (idempotent).
	if err := tenantdb.EnsureDefaultTenant(ctx, pgPool.DB(), logger); err != nil {
		logger.Fatal("failed to ensure default tenant", zap.Error(err))
	}

	// Provision all existing tenant schemas — idempotent, picks up new tables added to tenant_schema.sql.
	if err := tenantdb.ProvisionAllTenantSchemas(ctx, pgPool.DB(), logger); err != nil {
		logger.Warn("failed to provision tenant schemas", zap.Error(err))
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

	// 3b. Memory Pipeline component
	var memPipeline *mempipeline.Pipeline
	var pipelineNC *nats.Conn
	pipelineCfg := mempipeline.DefaultConfig()
	pipelineCfg.Enabled = cfg.MemoryPipelineEnabled
	pipelineCfg.NatsURL = cfg.NatsURL

	pipelineComponent := harnesspkg.NewSimpleComponent("memory-pipeline", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			if !pipelineCfg.Enabled {
				logger.Info("Memory pipeline disabled, skipping")
				return nil
			}
			nc, err := nats.Connect(pipelineCfg.NatsURL)
			if err != nil {
				logger.Warn("memory-pipeline: NATS connect failed", zap.Error(err))
				return nil
			}
			pipelineNC = nc
			var embedSvc mempipeline.EmbedClient
			if gateway.HasEmbeddingClient() {
				embedSvc = embedding.NewEmbeddingService(gateway, logger)
			}
			dimResolver := mempipeline.DimResolver(func(ctx context.Context, tenantID string) int {
				var settingsJSON []byte
				if err := pgPool.DB().QueryRow(ctx,
					"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
					tenantID,
				).Scan(&settingsJSON); err != nil {
					return 1536
				}
				var s map[string]interface{}
				if err := json.Unmarshal(settingsJSON, &s); err != nil {
					return 1536
				}
				if d, ok := s["embedding_dim"].(float64); ok && d > 0 {
					return int(d)
				}
				return 1536
			})
			vectorAdapter := mempipeline.NewMilvusVectorAdapter(services.VectorStore).WithDimResolver(dimResolver)
			memPipeline = mempipeline.New(pipelineCfg, pgPool.DB(), nc, embedSvc, vectorAdapter, gateway, logger)
			return memPipeline.Start(ctx)
		}),
		harnesspkg.WithStopFunc(func(ctx context.Context) error {
			if memPipeline != nil {
				memPipeline.Stop()
			}
			if pipelineNC != nil {
				_ = pipelineNC.Drain()
			}
			return nil
		}),
		harnesspkg.WithHealthCheckFunc(func(ctx context.Context) error {
			if !pipelineCfg.Enabled {
				return nil
			}
			if memPipeline == nil {
				return fmt.Errorf("memory pipeline not initialized")
			}
			return nil
		}),
	)
	if err := appHarness.Register(pipelineComponent); err != nil {
		logger.Fatal("Failed to register memory pipeline component", zap.Error(err))
	}

	// 5. CapabilityGateway
	codeExec := skill.NewCodeExecutor(skill.DefaultCodeExecutorConfig())
	skillGW := skillgateway.NewDefaultGateway(observability.NewPrometheusMetrics(logger), logger, nil)
	if err := skillGW.RegisterProvider(providers.NewDBSkillAdapter(pgPool.DB(), gateway, logger, codeExec)); err != nil {
		logger.Fatal("failed to register skill provider", zap.Error(err))
	}
	llmAdapter := capgateway.NewLLMAdapter(gateway, logger)
	skillAdapter := capgateway.NewSkillAdapter(skillGW, logger)
	capGW := capgateway.NewDefaultCapabilityGateway(llmAdapter, skillAdapter, logger)

	// 5b. Chat expiry cleanup — runs daily, removes conversations inactive for >30 days.
	chatCleanupComponent := harnesspkg.NewSimpleComponent("chat-cleanup", logger,
		harnesspkg.WithStartFunc(func(ctx context.Context) error {
			chatStore := agentpkg.NewPgChatStore(pgPool.DB())
			go func() {
				ticker := time.NewTicker(chatCleanupInterval)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						rows, err := pgPool.DB().Query(ctx,
							`SELECT id::text FROM tenants WHERE deleted_at IS NULL`)
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
			}()
			return nil
		}),
		harnesspkg.WithStopFunc(func(_ context.Context) error { return nil }),
	)
	if err := appHarness.Register(chatCleanupComponent); err != nil {
		logger.Fatal("Failed to register chat-cleanup component", zap.Error(err))
	}

	// 6. HTTP Server component
	router := api.SetupRouter(cfg, logger, gateway, pgPool.DB(), redisClient.Client(), capGW, skillAdapter, memPipeline)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: constants.HTTPReadHeaderTimeout,
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

			shutdownCtx, cancel := context.WithTimeout(ctx, constants.HTTPShutdownTimeout)
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
