package wiring

import (
	"context"
	"fmt"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/internal/parameters/application"
	"github.com/byteBuilderX/stratum/internal/parameters/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/constants"
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

// Resolve resolves a single registry key (platform-scope toggles included)
// through the same two-level fallback the resource path uses.
func (p resourceParameterProvider) Resolve(ctx context.Context, key string, declared map[string]any) (any, bool, error) {
	if p.svc == nil {
		return nil, false, fmt.Errorf("parameters service not configured")
	}
	return p.svc.Resolver().Resolve(ctx, key, declared)
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

// ValidateResourceKey validates a single resource-scope value by its full
// dotted registry key (memory.* params, which ValidateResourceValues cannot
// reach because it maps bare evaluation names). Platform-scope keys are
// rejected fail-closed: they must never leak into agents.parameters (write
// attribution is single-layer). Unknown key and out-of-bounds values return
// an error; nil service (db unavailable) degrades to no-op, matching
// ValidateResource.
func (p resourceParameterProvider) ValidateResourceKey(
	_ context.Context, key string, value any,
) error {
	if p.svc == nil {
		return nil
	}
	def, ok := p.svc.Registry().Get(key)
	if !ok {
		return fmt.Errorf("unknown parameter %s", key)
	}
	if def.Scope != domain.ScopeResource {
		return fmt.Errorf("parameter %s is %s-scope, not writable at resource layer", key, def.Scope)
	}
	return def.Validate(value)
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
	c.injectModelDirectoryValidation()
	return nil
}

// memoryWorkerModelKeys are the platform-scope model selectors rendered as a
// provider→model picker; their stored value is a model name string validated
// against the llmgateway enabled-chat directory at write time.
var memoryWorkerModelKeys = []string{
	"memory.enrich_model", "memory.summary_model",
	"memory.history_summary_model", "memory.supersede_model",
}

// injectModelDirectoryValidation attaches a model-directory existence check to
// the platform *_model keys. The check runs on PUT /admin/parameters
// (SetPlatformValues → Normalize → ValidateFn) and fails closed when the model
// is absent/disabled. The registry getter is lazy because buildParameters may
// run before buildLLMGateway in some harness paths; it resolves at write time.
func (c *Container) injectModelDirectoryValidation() {
	for _, key := range memoryWorkerModelKeys {
		def, ok := c.Parameters.Registry.Get(key)
		if !ok {
			continue
		}
		def.ValidateFn = c.validateModelInDirectory(key)
	}
}

// validateModelInDirectory returns a ValidateFn that checks the model name
// exists in the enabled chat model directory. Empty string = unset sentinel
// passes (worker falls back to its const default); a registry unavailable at
// write time rejects fail-closed rather than admitting a possibly-bogus model.
func (c *Container) validateModelInDirectory(key string) func(any) error {
	return func(value any) error {
		model, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: expected string model name", key)
		}
		if model == "" {
			return nil
		}
		reg := c.modelRegistryOrNil()
		if reg == nil {
			return fmt.Errorf("%s: model directory unavailable", key)
		}
		ctx, cancel := context.WithTimeout(context.Background(), constants.PlatformModelValidationTimeout)
		defer cancel()
		models, err := reg.ListChatModelsByTenant(ctx)
		if err != nil {
			return fmt.Errorf("%s: validate model: %w", key, err)
		}
		for _, m := range models {
			if m == model {
				return nil
			}
		}
		return fmt.Errorf("%s: model %q not in enabled chat model directory", key, model)
	}
}

// modelRegistryOrNil returns the llmgateway model registry or nil when the
// gateway was not wired (db unavailable / harness paths).
func (c *Container) modelRegistryOrNil() *llmgateway.ModelRegistry {
	if c.LLMGateway == nil {
		return nil
	}
	return c.LLMGateway.Registry
}
