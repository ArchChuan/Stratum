package application

import (
	"context"
	"embed"
	"fmt"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// PromptKey identifies a specific prompt component that can be overridden.
type PromptKey string

const (
	PromptKeySystem           PromptKey = "system_prompt"
	PromptKeyMemoryExtraction PromptKey = "memory_extraction_prompt"
	PromptKeyMemorySummary    PromptKey = "memory_summary_prompt"
	PromptKeyMemoryEnrichment PromptKey = "memory_enrichment_prompt"
	PromptKeyCompaction       PromptKey = "compaction_prompt"
)

// AllPromptKeys is the ordered list of all overridable prompt keys.
var AllPromptKeys = []PromptKey{
	PromptKeySystem,
	PromptKeyMemoryExtraction,
	PromptKeyMemorySummary,
	PromptKeyMemoryEnrichment,
	PromptKeyCompaction,
}

//go:embed prompts/*.txt
var defaultPrompts embed.FS

// PromptOverrideRepo is the port for loading prompt overrides from persistent
// storage (e.g. a JSONB column on agent_config or memory_pipeline_config).
type PromptOverrideRepo interface {
	GetOverrides(ctx context.Context, tenantID string) (map[string]string, error)
}

// PromptResolver resolves prompt text at runtime. Resolution order:
// 1. PromptRegistry (agent > tenant > global, with A/B split)
// 2. PromptOverrideRepo (legacy DB overrides)
// 3. Embedded defaults from prompts/*.txt
type PromptResolver struct {
	defaults  map[string]string
	overrides PromptOverrideRepo
	registry  agentport.PromptRegistry
}

// NewPromptResolver loads default prompts from the embedded prompts/ directory
// and accepts an optional override store.
func NewPromptResolver(repo PromptOverrideRepo) *PromptResolver {
	r := &PromptResolver{
		defaults:  make(map[string]string),
		overrides: repo,
	}
	for _, key := range AllPromptKeys {
		b, err := defaultPrompts.ReadFile("prompts/" + string(key) + ".txt")
		if err != nil {
			panic(fmt.Sprintf("prompt_resolver: missing default prompt %s: %v", key, err))
		}
		r.defaults[string(key)] = string(b)
	}
	return r
}

// SetRegistry injects the prompt registry for centralized version resolution.
func (r *PromptResolver) SetRegistry(registry agentport.PromptRegistry) {
	r.registry = registry
}

// Resolve returns the effective prompt text for key.
// Priority: registry(agent>tenant>global) > DB override > embedded default.
func (r *PromptResolver) Resolve(ctx context.Context, tenantID string, key PromptKey) (string, error) {
	return r.ResolveWithRequest(ctx, tenantID, "", "", key)
}

// ResolveWithRequest resolves with agentID and requestID for A/B split support.
func (r *PromptResolver) ResolveWithRequest(ctx context.Context, tenantID, agentID, requestID string, key PromptKey) (string, error) {
	defaultText, ok := r.defaults[string(key)]
	if !ok {
		return "", fmt.Errorf("prompt_resolver: unknown prompt key %q", key)
	}
	// 1. Try registry first (agent > tenant > global, with A/B split).
	if r.registry != nil {
		if text, err := r.registry.GetEffectivePrompt(ctx, string(key), tenantID, agentID, requestID); err == nil && text != "" {
			return text, nil
		}
	}
	// 2. Fall back to legacy DB overrides.
	if tenantID != "" && r.overrides != nil {
		overrides, err := r.overrides.GetOverrides(ctx, tenantID)
		if err != nil {
			return defaultText, fmt.Errorf("prompt_resolver: load overrides: %w", err)
		}
		if text, ok := overrides[string(key)]; ok && text != "" {
			return text, nil
		}
	}
	// 3. Embedded default.
	return defaultText, nil
}

// ResolveAll returns a map of all prompt keys to their effective text.
func (r *PromptResolver) ResolveAll(ctx context.Context, tenantID string) (map[string]string, error) {
	result := make(map[string]string, len(AllPromptKeys))
	for _, key := range AllPromptKeys {
		text, err := r.Resolve(ctx, tenantID, key)
		if err != nil {
			return nil, err
		}
		result[string(key)] = text
	}
	return result, nil
}
