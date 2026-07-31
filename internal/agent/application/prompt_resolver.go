package application

import (
	"context"
	"embed"
	"fmt"
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

// PromptResolver resolves prompt text at runtime using a Go-embed default
// overridden by DB-stored values. Prompts whose DB override is empty fall
// back to the embedded default.
type PromptResolver struct {
	defaults  map[string]string
	overrides PromptOverrideRepo
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
			// Missing default is a startup error — the binary is broken.
			panic(fmt.Sprintf("prompt_resolver: missing default prompt %s: %v", key, err))
		}
		r.defaults[string(key)] = string(b)
	}
	return r
}

// Resolve returns the effective prompt text for key, preferring DB override
// over embedded default. An empty tenantID skips the DB lookup.
func (r *PromptResolver) Resolve(ctx context.Context, tenantID string, key PromptKey) (string, error) {
	defaultText, ok := r.defaults[string(key)]
	if !ok {
		return "", fmt.Errorf("prompt_resolver: unknown prompt key %q", key)
	}
	if tenantID == "" || r.overrides == nil {
		return defaultText, nil
	}
	overrides, err := r.overrides.GetOverrides(ctx, tenantID)
	if err != nil {
		return defaultText, fmt.Errorf("prompt_resolver: load overrides: %w", err)
	}
	if text, ok := overrides[string(key)]; ok && text != "" {
		return text, nil
	}
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
