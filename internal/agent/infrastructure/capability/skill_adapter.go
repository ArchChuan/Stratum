package capgateway

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	skillgateway "github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway"
	"go.uber.org/zap"
)

// SkillExecutor is the minimal interface SkillAdapter needs from skillgateway.
type SkillExecutor interface {
	Execute(ctx context.Context, req skillgateway.SkillRequest) (skillgateway.SkillResponse, error)
}

type SkillAdapter struct {
	gw     SkillExecutor
	logger *zap.Logger
}

func NewSkillAdapter(gw SkillExecutor, logger *zap.Logger) *SkillAdapter {
	return &SkillAdapter{gw: gw, logger: logger}
}

func (a *SkillAdapter) Route(ctx context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	start := time.Now()
	skillReq := skillgateway.SkillRequest{
		TraceID: req.TraceID,
		SkillID: req.Skill.SkillID,
		Input:   req.Skill.Input,
	}
	skillResp, err := a.gw.Execute(ctx, skillReq)
	if err != nil {
		return port.CapabilityResponse{}, fmt.Errorf("skill_adapter: %w", err)
	}
	return port.CapabilityResponse{
		TraceID:  req.TraceID,
		Type:     port.CapSkill,
		Duration: time.Since(start),
		Output:   skillResp.Output,
	}, nil
}
