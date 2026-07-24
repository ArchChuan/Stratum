package port

import "context"

// TenantCapabilityResolver resolves per-tenant LLM configuration into a
// CapabilityGateway ready for agent execution. Implemented by the
// composition root (api/wiring) which is the only layer allowed to touch
// llmgateway/infrastructure.
type TenantCapabilityResolver interface {
	// Resolve returns the per-tenant CapabilityGateway and raw API-key map.
	// Returns (nil, nil, false) when no tenant-specific keys are configured.
	Resolve(ctx context.Context, tenantID string) (CapabilityGateway, map[string]string, bool)
	// InjectCompleter returns a ctx carrying the per-tenant LLM completer for
	// streaming execution. Returns the original ctx unchanged when no keys exist.
	InjectCompleter(ctx context.Context, tenantID string) context.Context
}

// TenantChatModelValidator verifies that a model belongs to the current
// tenant's strictly loaded provider configuration. Implementations must not
// use platform fallback credentials.
type TenantChatModelValidator interface {
	ValidateTenantChatModel(ctx context.Context, tenantID, model string) error
}

// TenantChatModelCatalog lists models backed by providers configured for the
// current tenant. Implementations must not include platform fallback models.
type TenantChatModelCatalog interface {
	ListTenantChatModels(ctx context.Context, tenantID string) ([]string, error)
}
