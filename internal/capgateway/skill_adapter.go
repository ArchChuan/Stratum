package capgateway

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/skillgateway"
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

func (a *SkillAdapter) Route(ctx context.Context, req CapabilityRequest) (CapabilityResponse, error) {
	start := time.Now()
	skillReq := skillgateway.SkillRequest{
		TraceID: req.TraceID,
		SkillID: req.Skill.SkillID,
		Input:   req.Skill.Input,
	}
	skillResp, err := a.gw.Execute(ctx, skillReq)
	if err != nil {
		return CapabilityResponse{}, fmt.Errorf("skill_adapter: %w", err)
	}
	return CapabilityResponse{
		TraceID:  req.TraceID,
		Type:     CapSkill,
		Duration: time.Since(start),
		Output:   skillResp.Output,
	}, nil
}
