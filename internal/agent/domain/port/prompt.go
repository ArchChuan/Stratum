package port

import "context"

// PromptRegistry resolves the effective prompt text for a key with scope
// priority (agent > tenant > global) and optional A/B split. Provided by
// the prompt context's RegistryService; the agent context consumes it
// through this port so application layers never import sibling contexts.
type PromptRegistry interface {
	GetEffectivePrompt(ctx context.Context, key, tenantID, agentID, requestID string) (string, error)
}
