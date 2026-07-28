package wiring

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

// tenantCapabilityResolver resolves per-tenant LLM capability through
// ModelRegistry. The registry handles caching, decryption, and provider
// selection — no more per-tenant Gateway construction or apiKeys maps.
type tenantCapabilityResolver struct {
	registry *llmgateway.ModelRegistry
	gateway  *llmgateway.Gateway
	logger   *zap.Logger
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

func (r *tenantCapabilityResolver) DiagnosticModelStatus(
	ctx context.Context, tenantID string,
) (status agentdomain.TenantModelDiagnosticStatus, err error) {
	if r.registry == nil {
		return status, fmt.Errorf("tenant model diagnostics: registry unavailable")
	}
	models, err := r.registry.ListChatModelsByTenant(ctx, tenantID)
	if err != nil {
		return status, fmt.Errorf("tenant model diagnostics: %w", err)
	}
	if len(models) > 0 {
		status.Configured = true
	}
	return status, nil
}

// Resolve returns the per-tenant CapabilityGateway by wrapping the shared
// Gateway instance. Per-tenant isolation is handled by ModelRegistry inside
// Gateway.Complete — the gateway reads tenantID from ctx and resolves the
// correct provider client.
func (r *tenantCapabilityResolver) Resolve(ctx context.Context, tenantID string) (agentport.CapabilityGateway, bool) {
	// Warm the registry so subsequent Complete calls don't have cold-start latency.
	if err := r.registry.WarmTenant(ctx, tenantID); err != nil {
		r.logger.Warn("tenant_resolver.warm_failed",
			zap.String("tenant_id", tenantID),
			zap.Error(err))
		return nil, false
	}
	llmAdapter := newAgentLLMAdapter(r.gateway)
	capGW := capgateway.NewDefaultCapabilityGateway(llmAdapter, r.logger)
	return capGW, true
}

// ResolveLLM returns the shared gateway. Per-tenant routing is done inside
// Complete via ctx → tenantID → registry lookup.
func (r *tenantCapabilityResolver) ResolveLLM(_ context.Context, _ string) *llmgateway.Gateway {
	return r.gateway
}

// ResolveWorkerLLM returns the shared gateway after verifying the tenant has
// at least one provider configured.
func (r *tenantCapabilityResolver) ResolveWorkerLLM(ctx context.Context, tenantID string) (*llmgateway.Gateway, error) {
	if r.registry == nil {
		return nil, fmt.Errorf("tenant llm: registry unavailable")
	}
	if err := r.registry.WarmTenant(ctx, tenantID); err != nil {
		return nil, fmt.Errorf("tenant llm: %w", err)
	}
	return r.gateway, nil
}

func (r *tenantCapabilityResolver) ValidateTenantChatModel(ctx context.Context, tenantID, model string) error {
	models, err := r.registry.ListChatModelsByTenant(ctx, tenantID)
	if err != nil || len(models) == 0 {
		return agentdomain.ErrAssistantModelUnavailable
	}
	for _, available := range models {
		if available == model {
			return nil
		}
	}
	return agentdomain.ErrInvalidSystemAssistantModel
}

func (r *tenantCapabilityResolver) ListTenantChatModels(ctx context.Context, tenantID string) ([]string, error) {
	models, err := r.registry.ListChatModelsByTenant(ctx, tenantID)
	if err != nil {
		return []string{}, nil
	}
	return models, nil
}

// InjectCompleter injects the shared Gateway into ctx for streaming execution.
func (r *tenantCapabilityResolver) InjectCompleter(ctx context.Context, _ string) context.Context {
	if r.gateway == nil {
		return ctx
	}
	return llmgatewaydomain.WithCompleter(ctx, r.gateway)
}
