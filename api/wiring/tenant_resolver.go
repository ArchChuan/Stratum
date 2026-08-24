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
	models, err := r.registry.ListChatModelsByTenant(ctx)
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

func (r *tenantCapabilityResolver) resolveGateway(ctx context.Context) (*llmgateway.Gateway, bool) {
	gw, ok, _ := r.resolveGatewayResult(ctx, false)
	return gw, ok
}

func (r *tenantCapabilityResolver) resolveGatewayResult(ctx context.Context, strict bool) (*llmgateway.Gateway, bool, error) {
	if r.registry == nil {
		if strict {
			return nil, false, fmt.Errorf("tenant llm: registry unavailable")
		}
		return r.gateway, r.gateway != nil, nil
	}
	// 全局目录已由启动期 Warm 预热一次；解析链内置 ②③④ 兜底与 ⑤ fail-closed，
	// 无需在此按租户预热。
	if strict {
		// 触发一次目录读取以传播 registry 基础设施故障（DB/provider 缺失），
		// 不把 worker 解析降级成静默 unavailable。
		if _, err := r.registry.ListChatModelsByTenant(ctx); err != nil {
			return nil, false, fmt.Errorf("tenant llm: %w", err)
		}
	}
	return r.gateway, true, nil
}

// Resolve returns a per-tenant CapabilityGateway.
func (r *tenantCapabilityResolver) Resolve(ctx context.Context, tenantID string) (agentport.CapabilityGateway, bool) {
	gw, ok := r.resolveGateway(ctx)
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
	gw, ok := r.resolveGateway(ctx)
	if !ok {
		return nil
	}
	return gw
}

// ResolveWorkerLLM resolves the current tenant gateway without hiding
// infrastructure or credential failures behind the global fallback.
func (r *tenantCapabilityResolver) ResolveWorkerLLM(ctx context.Context, tenantID string) (*llmgateway.Gateway, error) {
	gw, ok, err := r.resolveGatewayResult(ctx, true)
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
	names, err := r.registry.ListChatModelsByTenant(ctx)
	if err != nil {
		return fmt.Errorf("tenant llm model validation: %w", err)
	}
	for _, n := range names {
		if n == model {
			return nil
		}
	}
	return agentdomain.ErrInvalidAgentModel
}

func (r *tenantCapabilityResolver) ListTenantChatModels(ctx context.Context, tenantID string) ([]string, error) {
	if r.registry == nil {
		return []string{}, nil
	}
	names, err := r.registry.ListChatModelsByTenant(ctx)
	if err != nil {
		if errors.Is(err, agentdomain.ErrAssistantModelUnavailable) ||
			errors.Is(err, agentdomain.ErrInvalidAgentModel) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("tenant llm model catalogue: %w", err)
	}
	return names, nil
}

func (r *tenantCapabilityResolver) GetChatModelContextWindow(ctx context.Context, model string) (int, error) {
	if r.registry == nil {
		return 0, nil
	}
	return r.registry.GetChatModelContextWindow(ctx, model)
}

// ListTenantModelDetails projects the full tenant model catalog (including
// disabled and provider-managed models) into the platform-assistant DTO.
// Registry unavailability fails closed so the assistant never presents an
// empty catalog as healthy.
func (r *tenantCapabilityResolver) ListTenantModelDetails(ctx context.Context, tenantID string) ([]agentdomain.TenantModelDetail, error) {
	if r.registry == nil {
		return nil, fmt.Errorf("tenant llm model catalogue: registry unavailable")
	}
	models, err := r.registry.ListModelsByTenantDetails(ctx)
	if err != nil {
		return nil, fmt.Errorf("tenant llm model catalogue: %w", err)
	}
	details := make([]agentdomain.TenantModelDetail, 0, len(models))
	for _, m := range models {
		capabilities := make([]string, 0, len(m.Capabilities))
		for _, capability := range m.Capabilities {
			capabilities = append(capabilities, string(capability))
		}
		details = append(details, agentdomain.TenantModelDetail{
			Model:                  m.Name,
			Provider:               m.ProviderID,
			Capabilities:           capabilities,
			Enabled:                m.Enabled,
			ProviderManaged:        m.ProviderManaged,
			MaxTokens:              m.MaxTokens,
			EffectiveMaxTokens:     m.EffectivePolicy().MaxOutputTokens,
			DefaultOutputTokens:    m.EffectivePolicy().DefaultOutputTokens,
			EffectiveContextWindow: m.EffectivePolicy().ContextWindow,
			PolicySource:           string(m.EffectivePolicy().MaxOutputSource),
		})
	}
	return details, nil
}

// InjectCompleter injects the per-tenant LLM completer into ctx for streaming.
func (r *tenantCapabilityResolver) InjectCompleter(ctx context.Context, tenantID string) context.Context {
	gw, ok := r.resolveGateway(ctx)
	if !ok {
		return ctx
	}
	return llmgatewaydomain.WithCompleter(ctx, gw)
}
