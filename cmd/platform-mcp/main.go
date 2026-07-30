package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	platformapp "github.com/byteBuilderX/stratum/internal/platformmcp/application"
	"github.com/byteBuilderX/stratum/internal/platformmcp/infrastructure"
	platformserver "github.com/byteBuilderX/stratum/internal/platformmcp/server"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: could not load .env file: %v", err)
	}
	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		log.Fatalf("create Platform MCP logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck
	if err := run(logger); err != nil {
		logger.Fatal("platform_mcp.runtime.failed", zap.Error(err))
	}
}

func run(logger *zap.Logger) error {
	cfg, err := loadRuntimeConfig()
	if err != nil {
		return fmt.Errorf("load Platform MCP runtime config: %w", err)
	}
	shutdownTracing := initTracing(logger)
	if shutdownTracing != nil {
		defer shutdownTracer(shutdownTracing, logger)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return servePlatformMCP(ctx, cfg, logger)
}

func servePlatformMCP(ctx context.Context, cfg runtimeConfig, logger *zap.Logger) error {
	metrics := observability.NewPrometheusMetrics(logger)
	reloader := infrastructure.NewTLSReloader(cfg.tlsFiles)
	if err := reloader.Reload(); err != nil {
		return fmt.Errorf("initialize Platform MCP TLS: %w", err)
	}
	if err := updateCertificateMetrics(reloader, metrics); err != nil {
		return err
	}
	httpClient, err := infrastructure.NewReloadableBackendClient(reloader, cfg.backendServerName)
	if err != nil {
		return fmt.Errorf("create Stratum backend HTTP client: %w", err)
	}
	defer httpClient.Close()
	handler, err := buildHandler(cfg, reloader, httpClient, logger, metrics)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.listenAddress())
	if err != nil {
		return fmt.Errorf("listen for Platform MCP: %w", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: constants.HTTPReadHeaderTimeout}
	serveCtx, cancel := context.WithCancel(ctx)
	reloadDone := make(chan struct{})
	go func() {
		defer close(reloadDone)
		reloadTLSOnSignal(serveCtx, reloader, httpClient, logger, metrics)
	}()
	defer func() {
		cancel()
		<-reloadDone
	}()
	logger.Info("platform_mcp.runtime.started", zap.String("address", listener.Addr().String()))
	return runHTTPServer(serveCtx, server, tls.NewListener(listener, reloader.ServerConfig()), logger)
}

func buildHandler(
	cfg runtimeConfig,
	reloader *infrastructure.TLSReloader,
	client infrastructure.HTTPDoer,
	logger *zap.Logger,
	metrics *observability.PrometheusMetrics,
) (http.Handler, error) {
	stratumClient, err := infrastructure.NewObservedStratumClient(client, cfg.backendBaseURL, metrics)
	if err != nil {
		return nil, fmt.Errorf("create Stratum client: %w", err)
	}
	dispatcher, err := platformapp.NewToolDispatcher(stratumClient)
	if err != nil {
		return nil, fmt.Errorf("create Platform MCP dispatcher: %w", err)
	}
	probe, err := infrastructure.NewBackendProbe(client, cfg.backendBaseURL+"/internal/livez")
	if err != nil {
		return nil, fmt.Errorf("create Stratum readiness probe: %w", err)
	}
	readiness := platformapp.NewReadiness(reloader, dispatcher, probe)
	server, err := platformserver.New(platformserver.Config{
		Dispatcher: dispatcher, Readiness: readiness, Logger: logger,
		Metrics: metrics, MetricsHandler: metrics.GetHandler(),
	})
	if err != nil {
		return nil, fmt.Errorf("create Platform MCP server: %w", err)
	}
	return server.Handler(), nil
}

func reloadTLSOnSignal(
	ctx context.Context,
	reloader *infrastructure.TLSReloader,
	backend *infrastructure.ReloadableBackendClient,
	logger *zap.Logger,
	metrics *observability.PrometheusMetrics,
) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP)
	defer signal.Stop(signals)
	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
			if err := reloader.Reload(); err != nil {
				metrics.SetPlatformMCPCertificateRotation("error", 1)
				logger.Error("platform_mcp.tls.reload_failed", zap.Error(err))
				continue
			}
			if err := backend.Reload(); err != nil {
				metrics.SetPlatformMCPCertificateRotation("error", 1)
				logger.Error("platform_mcp.backend_tls.reload_failed", zap.Error(err))
				continue
			}
			if err := updateCertificateMetrics(reloader, metrics); err != nil {
				metrics.SetPlatformMCPCertificateRotation("error", 1)
				logger.Error("platform_mcp.tls.metrics_failed", zap.Error(err))
				continue
			}
			metrics.SetPlatformMCPCertificateRotation("error", 0)
			metrics.SetPlatformMCPCertificateRotation("success", 1)
			logger.Info("platform_mcp.tls.reloaded")
		}
	}
}

func updateCertificateMetrics(
	reloader *infrastructure.TLSReloader,
	metrics *observability.PrometheusMetrics,
) error {
	seconds, err := reloader.CertificateExpirySeconds()
	if err != nil {
		return fmt.Errorf("read Platform MCP certificate expiry: %w", err)
	}
	metrics.SetPlatformMCPCertificateExpiry(seconds)
	return nil
}

func shutdownTracer(shutdown func(context.Context) error, logger *zap.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), constants.HTTPShutdownTimeout)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		logger.Error("platform_mcp.tracing.shutdown_failed", zap.Error(err))
	}
}
