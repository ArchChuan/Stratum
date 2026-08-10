package gen

import (
	collabdomain "github.com/byteBuilderX/stratum/internal/collab/domain"
)

// ToCollabResponse 与手写 dto.ToCollabResponse 逐行一致(迁移保留)。
func ToCollabResponse(c collabdomain.Collaboration) CollabResponse {
	return CollabResponse{
		ID:              c.ID,
		TaskDescription: c.TaskDescription,
		Strategy:        string(c.Strategy),
		Status:          string(c.Status),
		CreatedBy:       c.CreatedBy,
		Participants:    c.Participants,
		CreatedAt:       c.CreatedAt,
		StartedAt:       c.StartedAt,
		CompletedAt:     c.CompletedAt,
	}
}

// ToTaskStepResponse 与手写 dto.ToTaskStepResponse 逐行一致(迁移保留)。
func ToTaskStepResponse(s collabdomain.TaskStep) TaskStepResponse {
	return TaskStepResponse{
		ID:           s.ID,
		PlanID:       s.PlanID,
		AgentID:      s.AgentID,
		Dependencies: s.Dependencies,
		Status:       string(s.Status),
		Input:        s.Input,
		Output:       s.Output,
		Error:        s.Error,
		CreatedAt:    s.CreatedAt,
	}
}
