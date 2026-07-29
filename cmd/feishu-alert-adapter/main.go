package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/byteBuilderX/stratum/internal/platform/alerting"
	"github.com/byteBuilderX/stratum/pkg/httpclient"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

const (
	defaultAddress        = ":8080"
	serverHeaderTimeout   = 5 * time.Second
	serverReadTimeout     = 15 * time.Second
	serverWriteTimeout    = 15 * time.Second
	serverIdleTimeout     = 60 * time.Second
	serverShutdownTimeout = 10 * time.Second
)

func main() {
	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		panic(err)
	}
	defer logger.Sync() //nolint:errcheck
	if err := run(logger); err != nil {
		logger.Fatal("adapter.run", zap.Error(err))
	}
}

func run(logger *zap.Logger) error {
	webhookURL := os.Getenv("FEISHU_WEBHOOK_URL")
	if err := validateWebhookURL(webhookURL); err != nil {
		return err
	}
	address := os.Getenv("ADAPTER_ADDRESS")
	if address == "" {
		address = defaultAddress
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	metrics := alerting.NewMetrics(registry)
	client := httpclient.New(
		httpclient.WithTimeout(10*time.Second),
		httpclient.WithDisableRedirects(),
	)
	delivery := alerting.NewDeliveryWithMetrics(client, nil, metrics)
	handler := alerting.NewHandler(delivery, webhookURL, metrics)
	mux := http.NewServeMux()
	mux.Handle("/alertmanager", handler)
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/livez", okHandler)
	mux.HandleFunc("/readyz", okHandler)

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: serverHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errorsCh := make(chan error, 1)
	go func() {
		logger.Info("adapter.start", zap.String("address", address))
		errorsCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-errorsCh; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func validateWebhookURL(rawURL string) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || strings.Contains(rawURL, "#") || parsed.Scheme != "https" || parsed.User != nil ||
		parsed.Fragment != "" ||
		(parsed.Hostname() != "open.feishu.cn" && parsed.Hostname() != "open.larksuite.com") {
		return errors.New("FEISHU_WEBHOOK_URL must be a valid HTTPS URL")
	}
	return nil
}

func okHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
