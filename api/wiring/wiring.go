// Package wiring is the composition root: it constructs concrete
// dependencies once at startup and exposes them as a Container.
// Handlers depend on application services through the Container; they
// never reach into infrastructure directly.
package wiring

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	mempipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors/code"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway/providers"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/storage/milvus"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	pkgredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
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
	Evaluation *Evaluation
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
		{"evaluation", c.buildEvaluation},
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

// DB returns the underlying *pgxpool.Pool, or nil when no PostgreSQL
// pool is available. Exported counterpart to dbOrNil for use by api/http.
func (c *Container) DB() *pgxpool.Pool {
	return c.dbOrNil()
}

// NewFromExisting wires a Container around dependencies already created
// by cmd/server/main.go. It bypasses buildStorage / buildLLMGateway /
// buildSkill (those resources come from the caller) and runs only the
// derived sub-builders.
//
// This exists for transitional compatibility while Task 10c migrates
// main.go to BuildContainer. After that migration this function is
// deleted.
func NewFromExisting(
	ctx context.Context,
	cfg *config.Config,
	logger *zap.Logger,
	gateway *llmgateway.Gateway,
	db *pgxpool.Pool,
	rdb *goredis.Client,
	skillAdapter port.Adapter,
	memPipeline *mempipeline.Pipeline,
) (*Container, error) {
	c := &Container{Config: cfg, Logger: logger}

	// Storage: adopt existing pgx + redis; Milvus + NATS still owned by
	// downstream sub-builders that need them (knowledge / memory).
	storage := &Storage{}
	if db != nil {
		storage.PG = postgres.Wrap(db)
	}
	if rdb != nil {
		storage.Redis = pkgredis.Wrap(rdb)
	}
	mil := milvus.NewVectorStore(cfg.MilvusHost, cfg.MilvusPort, logger)
	if err := mil.Connect(ctx); err != nil {
		logger.Warn("failed to connect to Milvus", zap.Error(err))
	}
	storage.Milvus = mil
	c.Storage = storage
	c.shutdown = append(c.shutdown, func(_ context.Context) error { return mil.Close() })

	// LLMGateway: adopt the gateway main.go already initialized;
	// build a fresh metrics provider (router used to do this inline).
	metrics := observability.NewPrometheusMetrics(logger)
	gateway.WithMetrics(metrics)
	c.LLMGateway = &LLMGateway{
		Gateway:      gateway,
		Metrics:      metrics,
		ModelService: llmapp.NewModelService(gateway),
	}

	// Run the derived sub-builders that don't need Skill or Memory yet.
	for _, step := range []buildStep{
		{"platform", c.buildPlatform},
		{"mcp", c.buildMCP},
		{"knowledge", c.buildKnowledge},
	} {
		if err := step.fn(ctx); err != nil {
			_ = c.Shutdown(ctx)
			return nil, fmt.Errorf("wiring.%s: %w", step.name, err)
		}
	}

	// Skill: prefer caller-provided capGW/skillAdapter so wiring stays
	// compatible with main.go's existing wiring path. The CodeExecutor
	// is constructed locally to mirror buildSkill — handlers depend on
	// it via Skill.CodeExecutor. The interface-typed skillAdapter is
	// asserted to the concrete *capgateway.SkillAdapter that main.go is
	// known to pass; assertion failure leaves the field nil (handlers
	// that depend on it must nil-check, matching main.go's behavior).
	c.Skill = &Skill{
		CodeExecutor: code.NewCodeExecutor(code.DefaultCodeExecutorConfig()),
	}
	if db != nil {
		c.Skill.VersionExecutor = providers.NewDBSkillAdapter(db, logger, c.Skill.CodeExecutor)
	}
	if sa, ok := skillAdapter.(*capgateway.SkillAdapter); ok {
		c.Skill.SkillAdapter = sa
	}

	// Memory: build injector + reuse caller's pipeline.
	if err := c.buildMemory(ctx); err != nil {
		_ = c.Shutdown(ctx)
		return nil, fmt.Errorf("wiring.memory: %w", err)
	}
	if memPipeline != nil {
		// Replace freshly-built pipeline with the caller's instance.
		c.Memory.Pipeline = memPipeline
		if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
			memPipeline.SetEmbedResolver(c.Knowledge.EmbedResolver)
		}
	}
	if err := c.buildIAM(ctx); err != nil {
		_ = c.Shutdown(ctx)
		return nil, fmt.Errorf("wiring.iam: %w", err)
	}
	if err := c.buildAgent(ctx); err != nil {
		_ = c.Shutdown(ctx)
		return nil, fmt.Errorf("wiring.agent: %w", err)
	}
	if err := c.buildEvaluation(ctx); err != nil {
		_ = c.Shutdown(ctx)
		return nil, fmt.Errorf("wiring.evaluation: %w", err)
	}
	return c, nil
}
