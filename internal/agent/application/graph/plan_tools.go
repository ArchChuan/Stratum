package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

var ErrPlanCheckpointRequired = errors.New("plan checkpoint writer is required")

func PlanToolDefinitions() []port.ToolDefinition {
	return []port.ToolDefinition{
		{Name: "stratum_create_plan", Description: "Create an explicit dependency plan for this task.", InputSchema: planSchema(map[string]any{
			"nodes": map[string]any{"type": "array"},
		})},
		{Name: "stratum_revise_plan", Description: "Revise the active plan using explicit add, update, remove, or dependency operations.", InputSchema: planSchema(map[string]any{
			"operations": map[string]any{"type": "array"},
		})},
		{Name: "stratum_continue_plan", Description: "Execute the active plan ready set.", InputSchema: planSchema(nil)},
		{Name: "stratum_cancel_plan", Description: "Cancel the active plan and all outstanding nodes.", InputSchema: planSchema(nil)},
	}
}

func planSchema(properties map[string]any) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	properties["expected_revision"] = map[string]any{"type": "integer", "minimum": 0}
	return map[string]any{"type": "object", "properties": properties, "required": []string{"expected_revision"}}
}

// ExecutePlanTool applies a reserved plan action. Invalid model input is
// returned as a corrective observation; persistence errors remain execution
// errors and never become successful observations.
func ExecutePlanTool(ctx context.Context, state *ReActState, call port.ToolCall) (string, error) {
	if state == nil {
		return "", errors.New("plan tool: state is required")
	}
	command := domain.PlanCommand{}
	payload, err := json.Marshal(call.Arguments)
	if err != nil {
		return "", fmt.Errorf("plan tool: encode arguments: %w", err)
	}
	if err := json.Unmarshal(payload, &command); err != nil {
		return correction(call.Name, fmt.Errorf("invalid arguments: %w", err), state.ActivePlan), nil
	}
	switch call.Name {
	case "stratum_create_plan":
		command.Kind = domain.PlanCommandCreate
	case "stratum_revise_plan":
		command.Kind = domain.PlanCommandRevise
	case "stratum_continue_plan":
		command.Kind = domain.PlanCommandContinue
	case "stratum_cancel_plan":
		command.Kind = domain.PlanCommandCancel
	default:
		return "", fmt.Errorf("plan tool: unknown reserved tool %q", call.Name)
	}
	idSource := state.PlanIDSource
	if idSource == nil {
		idSource = func() string { return "" }
	}
	next, err := domain.ApplyPlanCommand(state.ActivePlan, command, idSource, state.PlanLimits)
	if err != nil {
		return correction(call.Name, err, state.ActivePlan), nil
	}
	if state.PlanCheckpointWriter == nil {
		return "", ErrPlanCheckpointRequired
	}
	identity := state.PlanCheckpointIdentity
	if identity.CheckpointID == "" {
		identity.CheckpointID = fmt.Sprintf("%s-rev-%d", next.ID, next.Revision)
	}
	if identity.ExecutionID == "" {
		identity.ExecutionID = state.ExecutionID
	}
	if identity.TraceID == "" {
		identity.TraceID = state.TraceID
	}
	if identity.ConversationID == "" {
		identity.ConversationID = state.ConversationID
	}
	if err := PersistPlanCheckpoint(ctx, state.PlanCheckpointWriter, state.TenantID, identity, PlanCheckpointPayload{
		Plan: next, RemainingNodeBudget: state.PlanLimits.MaxNodes - len(next.Nodes), RemainingRevisionBudget: state.PlanLimits.MaxRevisions - next.Revision,
	}); err != nil {
		return "", err
	}
	state.ActivePlan = next
	state.PlanCheckpointIdentity = identity
	return planObservation(call.Name, next), nil
}

func correction(toolName string, err error, plan *domain.Plan) string {
	planID := ""
	revision := int64(0)
	if plan != nil {
		planID, revision = plan.ID, plan.Revision
	}
	payload, _ := json.Marshal(map[string]any{"correction": err.Error(), "tool": toolName, "plan_id": planID, "revision": revision})
	return string(payload)
}

func planObservation(toolName string, plan *domain.Plan) string {
	status := make(map[string]string, len(plan.Nodes))
	for _, node := range plan.Nodes {
		status[node.ID] = string(node.Status)
	}
	payload, _ := json.Marshal(map[string]any{"tool": toolName, "plan_id": plan.ID, "revision": plan.Revision, "status": plan.Status, "nodes": status})
	return string(payload)
}
