// Package wiring is the composition root: it constructs concrete
// dependencies once at startup and exposes them as a Container.
// Handlers depend on application services through the Container; they
// never reach into infrastructure directly.
package wiring

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/config"
)

// Container is the root holder for all wired dependencies. It is
// constructed once at startup by BuildContainer and torn down by
// Shutdown in reverse construction order.
type Container struct {
	Config *config.Config
	Logger *zap.Logger

	Storage    *Storage
	LLMGateway *LLMGateway

	shutdown []func(context.Context) error
}

// BuildContainer wires all dependencies in dependency order. On any
// error after partial construction, it invokes Shutdown to release
// already-built resources before returning.
func BuildContainer(ctx context.Context, cfg *config.Config, logger *zap.Logger) (*Container, error) {
	c := &Container{Config: cfg, Logger: logger}

	if err := c.buildStorage(ctx); err != nil {
		return nil, fmt.Errorf("wiring.storage: %w", err)
	}
	if err := c.buildLLMGateway(ctx); err != nil {
		_ = c.Shutdown(ctx)
		return nil, fmt.Errorf("wiring.llmgateway: %w", err)
	}
	return c, nil
}

// Shutdown invokes registered teardown hooks in reverse order. The
// first error encountered is returned; remaining hooks still run.
func (c *Container) Shutdown(ctx context.Context) error {
	var firstErr error
	for i := len(c.shutdown) - 1; i >= 0; i-- {
		if err := c.shutdown[i](ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
