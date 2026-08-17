package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/parameters/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Service is the parameters application facade consumed by handlers and by
// sibling contexts through the wiring ACL. It enforces single-attribution
// (platform layer writes only platform-scope keys), per-key validation and
// merge-write semantics.
type Service struct {
	registry *domain.ParametersRegistry
	store    port.PlatformStore
	resolver *Resolver
}

func NewService(registry *domain.ParametersRegistry, store port.PlatformStore) *Service {
	return &Service{
		registry: registry,
		store:    store,
		resolver: NewResolver(registry, store),
	}
}

// Registry exposes the code-level definitions to consumers (schema-driven
// rendering, evaluation search space).
func (s *Service) Registry() *domain.ParametersRegistry { return s.registry }

// Resolver returns the two-level fallback resolver for execution paths.
func (s *Service) Resolver() *Resolver { return s.resolver }

// Schema returns all parameter definitions for schema-driven rendering.
func (s *Service) Schema() []domain.ParameterDefinition { return s.registry.Schema() }

// ValidateResourceValues validates resource-scope declared sampling values
// against registry definitions, mapping bare JSONB keys (temperature,
// max_tokens, ...) through EvaluationKeys. Resource keys that deliberately
// carry no EvaluationKeys alias (e.g. agent.compaction_temperature — kept out
// of the evaluation search space) fall back to a registry-key short-name match.
// Unknown keys and out-of-bounds values return an error. Callers skip 0=unset
// values before invoking — an explicit zero is indistinguishable from an
// absent key.
func (s *Service) ValidateResourceValues(declared map[string]any) error {
	for bareKey, value := range declared {
		key, ok := s.registry.KeyForEvaluation(bareKey)
		if !ok {
			key, ok = s.registry.KeyByShortName(bareKey)
		}
		if !ok {
			return fmt.Errorf("unknown parameter %s", bareKey)
		}
		def, ok := s.registry.Get(key)
		if !ok {
			return fmt.Errorf("parameter %s not registered", bareKey)
		}
		if err := def.Validate(value); err != nil {
			return err
		}
	}
	return nil
}

// PlatformValues returns the current effective platform-layer values: stored
// value when present, otherwise the definition default. Absent numeric-0
// defaults are omitted so the frontend sees "unset".
func (s *Service) PlatformValues(ctx context.Context) (map[string]any, error) {
	stored, err := s.store.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("parameters service: list platform values: %w", err)
	}
	byKey := make(map[string]json.RawMessage, len(stored))
	for _, v := range stored {
		byKey[v.Key] = v.Value
	}

	out := make(map[string]any, len(s.registry.Schema()))
	for _, def := range s.registry.ForScope(domain.ScopePlatform) {
		if raw, ok := byKey[def.Key]; ok {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("parameters service: decode %s: %w", def.Key, err)
			}
			out[def.Key] = value
			continue
		}
		if !isUnset(def.Default) {
			out[def.Key] = def.Default
		}
	}
	return out, nil
}

// PromptDefaults returns the visible default prompt templates by full registry
// key. It is the whitelist backing the prompt-defaults endpoint: keys missing
// from the platform store fall back to these templates at execution time, and
// the frontend renders them for copy/view when a prompt field is unset.
// Templates live in pkg/constants (single source), placeholders (%s/%d) are
// runtime-substituted; extend this map when adding new default prompts.
func (s *Service) PromptDefaults() map[string]string {
	return map[string]string{
		"agent.compaction_prompt":       constants.CompactionDefaultPrompt,
		"memory.extraction_prompt":      constants.MemoryExtractionDefaultPrompt,
		"memory.enrich_prompt":          constants.MemoryEnrichDefaultPrompt,
		"memory.summary_prompt":         constants.MemorySummaryDefaultPrompt,
		"memory.history_summary_prompt": constants.MemoryHistorySummaryDefaultPrompt,
		"memory.supersede_prompt":       constants.MemorySupersedeDefaultPrompt,
	}
}

// SetPlatformValues applies merge semantics: only keys present in input are
// written (a legacy client PUT can never wipe stored values it does not
// know). Every key must be platform-scope (single attribution) and pass its
// definition's validation. updatedBy is audited on every written row.
func (s *Service) SetPlatformValues(
	ctx context.Context,
	values map[string]any,
	updatedBy string,
) error {
	if len(values) == 0 {
		return nil
	}
	if updatedBy == "" {
		updatedBy = "api"
	}

	for key, rawValue := range values {
		def, ok := s.registry.Get(key)
		if !ok {
			return &domain.ErrInvalidParameter{Key: key, Err: fmt.Errorf("unknown parameter %s", key)}
		}
		if def.Scope != domain.ScopePlatform {
			return &domain.ErrInvalidParameter{
				Key: key, Err: fmt.Errorf("%s is %s-scope, not writable at platform layer", key, def.Scope),
			}
		}
		value, err := def.Normalize(rawValue)
		if err != nil {
			return &domain.ErrInvalidParameter{Key: key, Err: err}
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("parameters service: encode %s: %w", key, err)
		}
		if err := s.store.SetValue(ctx, key, encoded, updatedBy); err != nil {
			return err
		}
	}
	return nil
}
