package application

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/byteBuilderX/stratum/internal/prompt/domain"
	"github.com/byteBuilderX/stratum/internal/prompt/domain/port"
)

// ABService manages A/B experiment bindings between stable and canary
// prompt versions. It is a thin orchestration layer over BindingRepo.
type ABService struct {
	bindings port.BindingRepo
	prompts  port.PromptRepo
}

// NewABService creates an A/B experiment service.
func NewABService(bindings port.BindingRepo, prompts port.PromptRepo) *ABService {
	return &ABService{bindings: bindings, prompts: prompts}
}

// BindExperiment creates or updates a prompt binding with an A/B split.
func (s *ABService) BindExperiment(
	ctx context.Context,
	key, scope, stableVersionID, canaryVersionID string,
	trafficPercent int,
) error {
	if trafficPercent < 0 || trafficPercent > 100 {
		return fmt.Errorf("prompt: traffic percent must be 0-100, got %d", trafficPercent)
	}
	// Verify both version IDs reference real templates.
	for _, hash := range []string{stableVersionID, canaryVersionID} {
		if hash == "" {
			continue
		}
		tmpl, err := s.prompts.GetByHash(ctx, hash)
		if err != nil || tmpl == nil {
			return fmt.Errorf("prompt: version %q not found", hash)
		}
	}
	binding := domain.PromptBinding{
		Key:             key,
		Scope:           scope,
		StableVersionID: stableVersionID,
		CanaryVersionID: canaryVersionID,
		TrafficPercent:  trafficPercent,
	}
	return s.bindings.UpsertBinding(ctx, binding)
}

// ClearExperiment removes the A/B binding for a key+scope pair.
func (s *ABService) ClearExperiment(ctx context.Context, key, scope string) error {
	return s.bindings.DeleteBinding(ctx, key, scope)
}

// ListBindings returns every A/B binding (scope prefix "" = all), for the
// admin bindings read endpoint.
func (s *ABService) ListBindings(ctx context.Context) ([]domain.PromptBinding, error) {
	bindings, err := s.bindings.ListBindings(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("prompt: list bindings: %w", err)
	}
	return bindings, nil
}

// resolveAB determines whether a request should be routed to the canary
// version based on a deterministic fnv hash of the request ID.
func resolveAB(requestID string, trafficPercent int) bool {
	if requestID == "" || trafficPercent <= 0 {
		return false
	}
	if trafficPercent >= 100 {
		return true
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(requestID))
	return int(h.Sum32()%100) < trafficPercent
}
