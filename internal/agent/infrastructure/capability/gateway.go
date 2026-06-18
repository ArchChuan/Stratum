// Package capgateway provides the unified capability routing facade,
// implementing internal/agent/domain/port.CapabilityGateway.
package capgateway

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

type DefaultCapabilityGateway struct {
	llm    port.Adapter
	skill  port.Adapter
	logger *zap.Logger
}

func NewDefaultCapabilityGateway(llm port.Adapter, skill port.Adapter, logger *zap.Logger) *DefaultCapabilityGateway {
	return &DefaultCapabilityGateway{llm: llm, skill: skill, logger: logger}
}

func (g *DefaultCapabilityGateway) Route(ctx context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	if err := req.Validate(); err != nil {
		return port.CapabilityResponse{}, err
	}
	switch req.Type {
	case port.CapLLM:
		return g.llm.Route(ctx, req)
	case port.CapSkill:
		return g.skill.Route(ctx, req)
	default:
		return port.CapabilityResponse{}, fmt.Errorf("capgateway: unknown type %q", req.Type)
	}
}
