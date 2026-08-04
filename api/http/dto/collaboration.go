package dto

import (
	"time"

	collabdomain "github.com/byteBuilderX/stratum/internal/collab/domain"
)

// CreateCollabRequest creates a plan. Strategy validation lives in the
// service layer so unknown values surface as ErrCollabInvalidInput.
type CreateCollabRequest struct {
	TaskDescription string                      `json:"task_description" binding:"required"`
	Strategy        collabdomain.CollabStrategy `json:"strategy" binding:"required"`
	Participants    []string                    `json:"participants" binding:"required"`
}

// CollabResponse is the plan surface shown to members.
type CollabResponse struct {
	ID              string     `json:"id"`
	TaskDescription string     `json:"taskDescription"`
	Strategy        string     `json:"strategy"`
	Status          string     `json:"status"`
	CreatedBy       string     `json:"createdBy"`
	Participants    []string   `json:"participants"`
	CreatedAt       time.Time  `json:"createdAt"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

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

// TaskStepResponse is the detail-view step surface: dependency structure,
// status, and the step's own input/output payloads.
type TaskStepResponse struct {
	ID           string         `json:"id"`
	PlanID       string         `json:"planId"`
	AgentID      string         `json:"agentId"`
	Dependencies []string       `json:"dependencies"`
	Status       string         `json:"status"`
	Input        map[string]any `json:"input"`
	Output       map[string]any `json:"output,omitempty"`
	Error        string         `json:"error,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
}

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
