package wiring

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

type tenantCapabilityResolver struct {
	registry *llmgateway.ModelRegistry
	gateway  *llmgateway.Gateway
	logger   *zap.Logger
}

func (r *tenantCapabilityResolver) DiagnosticModelStatus(
	ctx context.Context, tenantID string,
) (status agentdomain.TenantModelDiagnosticStatus, err error) {
	if r.registry == nil {
		return status, fmt.Errorf("tenant model diagnostics: registry unavailable")
	}
	models, err := r.registry.ListChatModelsByTenant(ctx, tenantID)
	if err != nil {
		return status, fmt.Errorf("tenant model diagnostics: list models: %w", err)
	}
	status.Configured = len(models) > 0
	return status, nil
}

func newTenantCapabilityResolver(
	registry *llmgateway.ModelRegistry,
	gateway *llmgateway.Gateway,
	logger *zap.Logger,
) agentport.TenantCapabilityResolver {
	return &tenantCapabilityResolver{
		registry: registry,
		gateway:  gateway,
		logger:   logger,
	}
}

func (r *tenantCapabilityResolver) resolveGateway(ctx context.Context, tenantID string) (*llmgateway.Gateway, bool) {
	gw, ok, _ := r.resolveGatewayResult(ctx, tenantID, false)
	return gw, ok
}

func (r *tenantCapabilityResolver) resolveGatewayResult(ctx context.Context, tenantID string, strict bool) (*llmgateway.Gateway, bool, error) {
	if r.registry == nil {
		if strict {
			return nil, false, fmt.Errorf("tenant llm: registry unavailable")
		}
		return r.gateway, r.gateway != nil, nil
	}
	if err := r.registry.WarmTenant(ctx, tenantID); err != nil {
		if strict {
			return nil, false, fmt.Errorf("tenant llm: warm: %w", err)
		}
		return r.gateway, r.gateway != nil, nil
	}
	return r.gateway, true, nil
}

// Resolve returns a per-tenant CapabilityGateway.
func (r *tenantCapabilityResolver) Resolve(ctx context.Context, tenantID string) (agentport.CapabilityGateway, bool) {
	gw, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return nil, false
	}
	llmAdapter := newAgentLLMAdapter(gw)
	capGW := capgateway.NewDefaultCapabilityGateway(llmAdapter, r.logger)
	return capGW, true
}

// ResolveLLM returns the tenant's LLM gateway as a pipeline.LLMClient. Returns
// nil when the tenant has no provider configured. Used by the memory pipeline
// to drive enrich/summary jobs against tenant-private gateways.
func (r *tenantCapabilityResolver) ResolveLLM(ctx context.Context, tenantID string) *llmgateway.Gateway {
	gw, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return nil
	}
	return gw
}

// ResolveWorkerLLM resolves the current tenant gateway without hiding
// infrastructure or credential failures behind the global fallback.
func (r *tenantCapabilityResolver) ResolveWorkerLLM(ctx context.Context, tenantID string) (*llmgateway.Gateway, error) {
	gw, ok, err := r.resolveGatewayResult(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	if !ok || gw == nil {
		return nil, fmt.Errorf("tenant llm: unavailable")
	}
	return gw, nil
}

func (r *tenantCapabilityResolver) ValidateTenantChatModel(ctx context.Context, tenantID, model string) error {
	if r.registry == nil {
		return fmt.Errorf("tenant llm model validation: %w", agentdomain.ErrAssistantModelUnavailable)
	}
	names, err := r.registry.ListChatModelsByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("tenant llm model validation: %w", err)
	}
	for _, n := range names {
		if n == model {
			return nil
		}
	}
	return agentdomain.ErrInvalidSystemAssistantModel
}

func (r *tenantCapabilityResolver) ListTenantChatModels(ctx context.Context, tenantID string) ([]string, error) {
	if r.registry == nil {
		return []string{}, nil
	}
	names, err := r.registry.ListChatModelsByTenant(ctx, tenantID)
	if err != nil {
		if errors.Is(err, agentdomain.ErrAssistantModelUnavailable) ||
			errors.Is(err, agentdomain.ErrInvalidSystemAssistantModel) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("tenant llm model catalogue: %w", err)
	}
	return names, nil
}

func (r *tenantCapabilityResolver) GetChatModelContextWindow(ctx context.Context, tenantID, model string) (int, error) {
	if r.registry == nil {
		return 0, nil
	}
	return r.registry.GetChatModelContextWindow(ctx, tenantID, model)
}

// InjectCompleter injects the per-tenant LLM completer into ctx for streaming.
func (r *tenantCapabilityResolver) InjectCompleter(ctx context.Context, tenantID string) context.Context {
	gw, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return ctx
	}
	return llmgatewaydomain.WithCompleter(ctx, gw)
}
