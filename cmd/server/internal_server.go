package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"

	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	harnesspkg "github.com/byteBuilderX/stratum/internal/platform/harness"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

type internalHTTPServer struct {
	files    config.InternalAPIConfig
	server   *http.Server
	logger   *zap.Logger
	listener net.Listener
	done     chan error
	mu       sync.RWMutex
}

func newInternalHTTPServer(
	files config.InternalAPIConfig,
	handler http.Handler,
	logger *zap.Logger,
) *internalHTTPServer {
	return &internalHTTPServer{
		files: files,
		server: &http.Server{
			Addr:              ":" + files.Port,
			Handler:           handler,
			ReadHeaderTimeout: constants.HTTPReadHeaderTimeout,
		},
		logger: logger,
	}
}

func (s *internalHTTPServer) Name() string {
	return "internal-http-server"
}

func (s *internalHTTPServer) Start(ctx context.Context) error {
	tlsConfig, err := loadInternalServerTLS(s.files)
	if err != nil {
		return err
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("listen internal API: %w", err)
	}
	s.server.TLSConfig = tlsConfig
	s.mu.Lock()
	s.listener = listener
	s.done = make(chan error, 1)
	done := s.done
	s.mu.Unlock()
	go s.serve(tls.NewListener(listener, tlsConfig), done)
	return nil
}

func (s *internalHTTPServer) Stop(ctx context.Context) error {
	s.mu.RLock()
	done := s.done
	s.mu.RUnlock()
	if done == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), constants.HTTPShutdownTimeout)
	defer cancel()
	shutdownErr := s.server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = s.server.Close()
	}
	serveErr := waitForInternalServe(shutdownCtx, done)
	return errors.Join(shutdownErr, serveErr)
}

func (s *internalHTTPServer) HealthCheck(context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return errors.New("internal API listener is not started")
	}
	return nil
}

func (s *internalHTTPServer) serve(listener net.Listener, done chan<- error) {
	defer close(done)
	err := s.server.Serve(listener)
	s.mu.Lock()
	s.listener = nil
	s.mu.Unlock()
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return
	}
	wrapped := fmt.Errorf("serve internal API: %w", err)
	s.logger.Error("api.internal.serve", zap.Error(wrapped))
	done <- wrapped
}

func waitForInternalServe(ctx context.Context, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("wait for internal API shutdown: %w", ctx.Err())
	}
}

func registerInternalHTTPServer(
	harness *harnesspkg.Harness,
	cfg *config.Config,
	container *wiring.Container,
	logger *zap.Logger,
) error {
	if !cfg.InternalAPI.Configured() {
		return nil
	}
	router, err := buildInternalRouter(container)
	if err != nil {
		return err
	}
	return harness.Register(newInternalHTTPServer(cfg.InternalAPI, router, logger))
}

func buildInternalRouter(container *wiring.Container) (http.Handler, error) {
	if container == nil || container.PlatformMCP == nil || container.PlatformMCP.TokenExchange == nil {
		return nil, errors.New("internal API Platform MCP token exchange is not wired")
	}
	deps := apihttp.InternalRouterDeps{
		Exchange:     container.PlatformMCP.TokenExchange,
		Tokens:       container.PlatformMCP.Tokens,
		Capabilities: container.PlatformMCP.Capabilities,
		Logger:       container.Logger,
		Metrics:      container.Platform.Metrics,
		AuthMetrics:  container.Platform.Metrics,
	}
	if container.MCP != nil && container.MCP.Manager != nil {
		deps.MCPForward = container.MCP.Manager
	}
	return apihttp.NewInternalRouter(deps)
}

func loadInternalServerTLS(files config.InternalAPIConfig) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load internal API server certificate: %w", err)
	}
	clientCAs, err := loadClientCAPool(files.ClientCAFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		Certificates:     []tls.Certificate{certificate},
		ClientAuth:       tls.RequireAndVerifyClientCert,
		ClientCAs:        clientCAs,
		VerifyConnection: verifyPlatformMCPConnection,
	}, nil
}

func loadClientCAPool(path string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read internal API client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, errors.New("parse internal API client CA: no certificates found")
	}
	return pool, nil
}

func verifyPlatformMCPConnection(state tls.ConnectionState) error {
	if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return errors.New("platform MCP client certificate was not verified")
	}
	leaf := state.PeerCertificates[0]
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != middleware.PlatformMCPWorkloadURI {
		return errors.New("platform MCP client SPIFFE identity denied")
	}
	return nil
}
