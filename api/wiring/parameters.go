package wiring

import (
	"context"
	"fmt"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/application"
	"github.com/byteBuilderX/stratum/internal/parameters/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/infrastructure/persistence"
)

// Parameters groups the unified parameter registry services for wiring
// consumers. Other contexts read platform defaults through the resolver
// (injected via consumer-side ports); they never import this package.
type Parameters struct {
	Service  *application.Service
	Registry *domain.ParametersRegistry
}

// resourceParameterProvider adapts the parameters resolver to the agent
// domain port (thin ACL; wiring is the only allowed adapter seam). A nil
// service (db unavailable) reports a resolution error so the agent execution
// path keeps its gateway defaults instead of panicking.
type resourceParameterProvider struct {
	svc *application.Service
}

func (p resourceParameterProvider) ResolveForResource(
	ctx context.Context, declared map[string]any,
) (map[string]any, error) {
	if p.svc == nil {
		return nil, fmt.Errorf("parameters service not configured")
	}
	return p.svc.Resolver().ResolveForResource(ctx, declared)
}

// ValidateResource validates declared sampling values through the registry;
// a nil service (db unavailable) skips validation so the write path still
// works — the same degrade convention as resolution.
func (p resourceParameterProvider) ValidateResource(
	_ context.Context, declared map[string]any,
) error {
	if p.svc == nil {
		return nil
	}
	return p.svc.ValidateResourceValues(declared)
}

var _ agentport.ParametersProvider = resourceParameterProvider{}

// agentParametersProvider wires the unified parameter registry resource
// validator into the agent service when available; nil keeps the previous
// no-op behaviour (db unavailable / registry not built).
func agentParametersProvider(c *Container) agentport.ParametersProvider {
	if c.Parameters == nil {
		return nil
	}
	return resourceParameterProvider{svc: c.Parameters.Service}
}

// validatePatchKeys rejects candidate patch keys that are not registered
// evaluation keys; the registry is the single source of truth (legacy
// per-adapter whitelists removed). Called once before patch application so
// per-key checks stay out of the apply loops. A nil registry (degraded
// wiring) admits all keys, matching the previous adapter behaviour.
func validatePatchKeys(registry *domain.ParametersRegistry, patch evaldomain.CandidatePatch) error {
	if registry == nil {
		return nil
	}
	for key := range patch.PromptPatch {
		if !registry.IsEvaluationKey(key) {
			return fmt.Errorf("evaluation adapter: prompt field is not registered: %s", key)
		}
	}
	for key := range patch.ParameterPatch {
		if !registry.IsEvaluationKey(key) {
			return fmt.Errorf("evaluation adapter: parameter field is not registered: %s", key)
		}
	}
	return nil
}

// buildParameters builds the registry-backed service over the public
// platform_settings table. It degrades to an empty service when the database
// is unavailable (matches platform.go degrade-rather-than-panic convention);
// consumers must nil-check before use.
func (c *Container) buildParameters(_ context.Context) error {
	db := c.dbOrNil()
	c.Parameters = &Parameters{Registry: domain.NewParametersRegistry()}
	if db == nil {
		return nil
	}
	repo := persistence.NewPlatformRepository(db)
	c.Parameters.Service = application.NewService(c.Parameters.Registry, repo)
	return nil
}
