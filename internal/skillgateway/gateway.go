// Package skillgateway provides skill gateway and routing.
package skillgateway

import (
	"context"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	"go.uber.org/zap"
)

// SkillGateway 统一 skill 调用入口接口
type SkillGateway interface {
	Execute(ctx context.Context, req SkillRequest) (SkillResponse, error)
	ExecutePipeline(ctx context.Context, pipeline Pipeline) (PipelineResult, error)
	RegisterProvider(provider SkillProvider) error
}

// DefaultGateway SkillGateway 的默认实现
type DefaultGateway struct {
	registry *ProviderRegistry
	atomic   *atomicEngine
	pipeline *pipelineEngine
}

// NewDefaultGateway 构造函数；cbConfig 为 nil 时使用默认熔断参数
func NewDefaultGateway(
	metrics *observability.PrometheusMetrics,
	logger *zap.Logger,
	cbConfig *CircuitBreakerConfig,
) *DefaultGateway {
	if cbConfig == nil {
		cbConfig = defaultCBConfig()
	}
	registry := newProviderRegistry()
	cbManager := newCircuitBreakerManager(*cbConfig, metrics, logger)
	auditor := newAuditor(logger)
	ae := newAtomicEngine(registry, cbManager, metrics, auditor, logger)
	pe := newPipelineEngine(ae, logger)
	return &DefaultGateway{
		registry: registry,
		atomic:   ae,
		pipeline: pe,
	}
}

func (g *DefaultGateway) Execute(ctx context.Context, req SkillRequest) (SkillResponse, error) {
	return g.atomic.execute(ctx, req)
}

func (g *DefaultGateway) ExecutePipeline(ctx context.Context, p Pipeline) (PipelineResult, error) {
	return g.pipeline.execute(ctx, p)
}

func (g *DefaultGateway) RegisterProvider(provider SkillProvider) error {
	return g.registry.Register(provider)
}
