package capgateway

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// skillGateway is the minimal interface SkillAdapter needs.
// The concrete adapter wrapping skill/infrastructure/gateway is provided
// by the wiring layer, keeping this package free of cross-context
// infrastructure imports.
type skillGateway interface {
	Execute(ctx context.Context, traceID, skillID, versionID string, input any) (any, error)
}

type SkillAdapter struct {
	gw     skillGateway
	logger *zap.Logger
}

func NewSkillAdapter(gw skillGateway, logger *zap.Logger) *SkillAdapter {
	return &SkillAdapter{gw: gw, logger: logger}
}

func (a *SkillAdapter) Route(ctx context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	start := time.Now()
	output, err := a.gw.Execute(ctx, req.TraceID, req.Skill.SkillID, req.Skill.VersionID, req.Skill.Input)
	if err != nil {
		return port.CapabilityResponse{}, fmt.Errorf("skill_adapter: %w", err)
	}
	return port.CapabilityResponse{
		TraceID:  req.TraceID,
		Type:     port.CapSkill,
		Duration: time.Since(start),
		Output:   output,
	}, nil
}
