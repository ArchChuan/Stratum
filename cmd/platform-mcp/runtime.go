package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

func runHTTPServer(
	ctx context.Context,
	server *http.Server,
	listener net.Listener,
	logger *zap.Logger,
) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	select {
	case err := <-serveErr:
		return normalizeServeError(err)
	case <-ctx.Done():
		logger.Info("platform_mcp.runtime.stopping")
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), constants.HTTPShutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = server.Close()
	}
	return errors.Join(shutdownErr, normalizeServeError(<-serveErr))
}

func normalizeServeError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve Platform MCP: %w", err)
}
