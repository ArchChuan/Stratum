package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/parameters/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/domain/port"
)

// Resolver implements scope-aware resolution:
//   - platform-scope keys: declared-layer value → stored platform value →
//     definition default (the 全局参数 contract, hot-updatable);
//   - resource-scope keys: declared-layer value only. Resources own their
//     required configuration; platform defaults and definition defaults are
//     deliberately NOT applied as fallback. An unset resource key degrades
//     to the consuming layer's documented default (gateway/provider/constant)
//     and is surfaced with a WARN at the consume point.
//
// 0=unset semantics: an explicit numeric 0 is indistinguishable from an
// absent key (omitempty JSON), so a resolved numeric 0 means "not set" and
// resolution continues to the next fallback tier. Bool/string values never
// carry this semantics.
type Resolver struct {
	registry *domain.ParametersRegistry
	store    port.PlatformStore
}

func NewResolver(registry *domain.ParametersRegistry, store port.PlatformStore) *Resolver {
	return &Resolver{registry: registry, store: store}
}

// ResolveForResource resolves a resource's declared parameter values without
// any platform/default fallback, returning only keys that resolve to an
// effective non-unset value. The result is the authoritative execution input
// (assembleOptions); an absent key == unset == the consuming layer's
// documented default (gateway/provider/constant).
func (r *Resolver) ResolveForResource(
	ctx context.Context,
	declared map[string]any,
) (map[string]any, error) {
	effective := make(map[string]any, len(declared)+4)
	for _, key := range r.registry.ResourceKeys() {
		value, present, err := r.resolveOne(ctx, declared, key)
		if err != nil {
			return nil, err
		}
		if present {
			effective[key] = value
		}
	}
	return effective, nil
}

// Resolve returns the effective value for a single registry key.
// present=false means the value resolved to unset.
func (r *Resolver) Resolve(ctx context.Context, key string, declared map[string]any) (any, bool, error) {
	return r.resolveOne(ctx, declared, key)
}

// resolveOne walks the scope's fallback tiers, treating numeric 0 as unset at
// every tier. Platform-scope keys walk declared → platform → definition
// default; resource-scope keys walk declared only (no fallback). The tiers
// share one evaluation loop; only their value source differs.
func (r *Resolver) resolveOne(ctx context.Context, declared map[string]any, key string) (any, bool, error) {
	def, ok := r.registry.Get(key)
	if !ok {
		return nil, false, fmt.Errorf("parameter resolver: unknown key %s", key)
	}
	declaredValue, declaredOK := declared[key]
	tiers := []tierCandidate{
		{name: "declared", raw: declaredValue, ok: declaredOK},
	}
	if def.Scope == domain.ScopePlatform {
		platformTiers, err := r.platformTiers(ctx, key, def)
		if err != nil {
			return nil, false, err
		}
		tiers = append(tiers, platformTiers...)
	}
	for _, tier := range tiers {
		if !tier.ok {
			continue
		}
		norm, set, err := r.resolveTier(tier.name, key, def, tier.raw)
		if err != nil {
			return nil, false, err
		}
		if set {
			return norm, true, nil
		}
	}
	return nil, false, nil
}

// platformTiers loads the stored platform value and definition default tiers
// for a platform-scope key (the 全局参数 contract). Resource-scope keys never
// reach this helper: resources own their required configuration and have no
// platform/default fallback.
func (r *Resolver) platformTiers(
	ctx context.Context,
	key string,
	def *domain.ParameterDefinition,
) ([]tierCandidate, error) {
	platformRaw, platformOK, err := r.store.GetValue(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("parameter resolver: platform %s: %w", key, err)
	}
	var platformValue any
	if platformOK {
		if err := json.Unmarshal(platformRaw, &platformValue); err != nil {
			return nil, fmt.Errorf("parameter resolver: platform %s decode: %w", key, err)
		}
	}
	return []tierCandidate{
		{name: "platform", raw: platformValue, ok: platformOK},
		{name: "default", raw: def.Default, ok: def.Default != nil},
	}, nil
}

// tierCandidate is one fallback layer's raw value and presence.
type tierCandidate struct {
	name string
	raw  any
	ok   bool
}

// resolveTier normalizes and validates one candidate value, reporting whether
// it resolves to a set (non-unset) value. tier labels the error source layer.
func (r *Resolver) resolveTier(tier, key string, def *domain.ParameterDefinition, value any) (any, bool, error) {
	norm, err := def.Normalize(value)
	if err != nil {
		return nil, false, fmt.Errorf("parameter resolver: %s %s: %w", tier, key, err)
	}
	return norm, !isUnset(norm), nil
}

// isUnset reports whether a normalized value carries the 0=unset semantics.
// nil (declared default-less complex params like bindings) is always unset.
func isUnset(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case int64:
		return v == 0
	case float64:
		return v == 0
	default:
		return false
	}
}
