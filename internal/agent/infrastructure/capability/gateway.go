package capgateway

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// CapabilityGateway is the unified capability routing facade.
type CapabilityGateway interface {
	Route(ctx context.Context, req CapabilityRequest) (CapabilityResponse, error)
}

// Adapter is the common interface for LLM and Skill adapters.
type Adapter interface {
	Route(ctx context.Context, req CapabilityRequest) (CapabilityResponse, error)
}

type DefaultCapabilityGateway struct {
	llm    Adapter
	skill  Adapter
	logger *zap.Logger
}

func NewDefaultCapabilityGateway(llm Adapter, skill Adapter, logger *zap.Logger) *DefaultCapabilityGateway {
	return &DefaultCapabilityGateway{llm: llm, skill: skill, logger: logger}
}

func (g *DefaultCapabilityGateway) Route(ctx context.Context, req CapabilityRequest) (CapabilityResponse, error) {
	if err := req.Validate(); err != nil {
		return CapabilityResponse{}, err
	}
	switch req.Type {
	case CapLLM:
		return g.llm.Route(ctx, req)
	case CapSkill:
		return g.skill.Route(ctx, req)
	default:
		return CapabilityResponse{}, fmt.Errorf("capgateway: unknown type %q", req.Type)
	}
}
