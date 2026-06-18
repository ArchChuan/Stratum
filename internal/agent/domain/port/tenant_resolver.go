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
