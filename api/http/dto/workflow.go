package dto

import (
	"fmt"

	workflowdomain "github.com/byteBuilderX/stratum/internal/workflow/domain"
)

type CreateWorkflowRequest struct {
	Name        string                     `json:"name" binding:"required"`
	Description string                     `json:"description"`
	Spec        workflowdomain.Spec        `json:"spec" binding:"required"`
	InputSchema workflowdomain.InputSchema `json:"input_schema" binding:"required"`
}

type UpdateWorkflowRequest struct {
	Name             string                     `json:"name" binding:"required"`
	Description      string                     `json:"description"`
	Spec             workflowdomain.Spec        `json:"spec" binding:"required"`
	InputSchema      workflowdomain.InputSchema `json:"input_schema" binding:"required"`
	ExpectedRevision int64                      `json:"expected_revision" binding:"required"`
}

type StartWorkflowRunRequest struct {
	VersionID      string         `json:"version_id" binding:"required"`
	Task           string         `json:"task" binding:"required"`
	Fields         map[string]any `json:"fields"`
	IdempotencyKey string         `json:"idempotency_key" binding:"required"`
}

func (r StartWorkflowRunRequest) RunInput() (map[string]any, error) {
	if _, exists := r.Fields["task"]; exists {
		return nil, fmt.Errorf("fields.task is reserved")
	}
	input := make(map[string]any, len(r.Fields)+1)
	input["task"] = r.Task
	for key, value := range r.Fields {
		input[key] = value
	}
	return input, nil
}

type WorkflowControlRequest struct {
	ExpectedGeneration int64  `json:"expected_generation" binding:"required"`
	Reason             string `json:"reason"`
}
type WorkflowApprovalDecisionRequest struct {
	RunID              string                          `json:"run_id" binding:"required"`
	AttemptID          string                          `json:"attempt_id" binding:"required"`
	ExpectedGeneration int64                           `json:"expected_generation" binding:"required"`
	Decision           workflowdomain.ApprovalDecision `json:"decision" binding:"required,oneof=approve reject"`
	Comment            string                          `json:"comment"`
}
type WorkflowManualResolveRequest struct {
	ExpectedGeneration int64                       `json:"expected_generation" binding:"required"`
	Action             workflowdomain.ManualAction `json:"action" binding:"required"`
	OutputSummary      string                      `json:"output_summary"`
}
