// Package wiring is the composition root: it constructs concrete
// dependencies once at startup and exposes them as a Container.
// Handlers depend on application services through the Container; they
// never reach into infrastructure directly.
package wiring

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
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
	Platform   *Platform
	MCP        *MCP
	Skill      *Skill
	Knowledge  *Knowledge
	Memory     *Memory
	IAM        *IAM
	Agent      *Agent

	shutdown []func(context.Context) error
}

// buildStep names a wiring stage and its builder. The name is used in
// the wrapped error returned to BuildContainer's caller.
type buildStep struct {
	name string
	fn   func(context.Context) error
}

// BuildContainer wires all dependencies in dependency order. On any
// error after partial construction, it invokes Shutdown to release
// already-built resources before returning.
//
// Order: storage → llmgateway → platform → mcp → skill → knowledge →
// memory → iam → agent. Shutdown reverses construction.
func BuildContainer(ctx context.Context, cfg *config.Config, logger *zap.Logger) (*Container, error) {
	c := &Container{Config: cfg, Logger: logger}

	steps := []buildStep{
		{"storage", c.buildStorage},
		{"llmgateway", c.buildLLMGateway},
		{"platform", c.buildPlatform},
		{"mcp", c.buildMCP},
		{"skill", c.buildSkill},
		{"knowledge", c.buildKnowledge},
		{"memory", c.buildMemory},
		{"iam", c.buildIAM},
		{"agent", c.buildAgent},
	}

	for _, step := range steps {
		if err := step.fn(ctx); err != nil {
			_ = c.Shutdown(ctx)
			return nil, fmt.Errorf("wiring.%s: %w", step.name, err)
		}
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

// dbOrNil returns the underlying pgxpool.Pool if Storage and its PG
// pool are present, otherwise nil. Builders use this to degrade
// gracefully when running without a database (matches main.go/router.go
// nil-checks).
func (c *Container) dbOrNil() *pgxpool.Pool {
	if c.Storage == nil || c.Storage.PG == nil {
		return nil
	}
	return c.Storage.PG.DB()
}
