package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	jschema "github.com/byteBuilderX/stratum/pkg/jsonschema"
)

var ErrPlanCheckpointRequired = errors.New("plan checkpoint writer is required")

func PlanToolDefinitions() []port.ToolDefinition {
	return []port.ToolDefinition{
		{Name: "stratum_create_plan", Description: "Create an explicit dependency plan for this task.", InputSchema: planSchema(
			jschema.OptionalProp("nodes", jschema.UntypedArray("")),
		)},
		{Name: "stratum_revise_plan", Description: "Revise the active plan using explicit add, update, remove, or dependency operations.", InputSchema: planSchema(
			jschema.OptionalProp("operations", jschema.UntypedArray("")),
		)},
		{Name: "stratum_continue_plan", Description: "Execute the active plan ready set.", InputSchema: planSchema()},
		{Name: "stratum_cancel_plan", Description: "Cancel the active plan and all outstanding nodes.", InputSchema: planSchema()},
		{Name: "stratum_complete_task", Description: "Mark the current goal as fully achieved and stop task tracking.", InputSchema: planSchema()},
	}
}

func planSchema(props ...jschema.Prop) map[string]any {
	props = append(props, jschema.RequiredProp("expected_revision", jschema.Integer(jschema.Ptr(0), nil, "")))
	return jschema.Must(jschema.Object(props...)).Map()
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
		return state.recordCorrection(call.Name, fmt.Errorf("invalid arguments: %w", err), state.ActivePlan), nil
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
	case "stratum_complete_task":
		// 完成信号不修改 plan（ApplyPlanCommand 无此 command），仅记录状态；
		// expected_revision 为 planSchema 强制参数，此处忽略（独立于 plan 版本）。
		state.TaskCompleteRequested = true
		return planObservation("stratum_complete_task", state.ActivePlan), nil
	default:
		return "", fmt.Errorf("plan tool: unknown reserved tool %q", call.Name)
	}
	idSource := state.PlanIDSource
	if idSource == nil {
		idSource = func() string { return "" }
	}
	next, err := domain.ApplyPlanCommand(state.ActivePlan, command, idSource, state.PlanLimits)
	if err != nil {
		return state.recordCorrection(call.Name, err, state.ActivePlan), nil
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
	if state.PlanCheckpointWriter == nil {
		return "", ErrPlanCheckpointRequired
	}
	if err := PersistPlanCheckpoint(ctx, state.PlanCheckpointWriter, state.TenantID, identity, PlanCheckpointPayload{
		Plan: next, RemainingNodeBudget: state.PlanLimits.MaxNodes - len(next.Nodes), RemainingRevisionBudget: state.PlanLimits.MaxRevisions - next.Revision,
	}, checkpointSnapshot(state)); err != nil {
		return "", err
	}
	state.ActivePlan = next
	state.PlanCheckpointIdentity = identity
	if call.Name == "stratum_continue_plan" && state.PlanNodeExecutor != nil {
		return scheduleContinue(state, call)
	}
	return planObservation(call.Name, next), nil
}

// scheduleContinue registers the ready wave for engine execution. With no
// ready nodes it returns the plain observation. When a wave is scheduled the
// tool node skips the direct observation — the finalize node appends it after
// the wave joins — and PlanContinueCallID carries the call for that skip.
func scheduleContinue(state *ReActState, call port.ToolCall) (string, error) {
	content, err := schedulePlanWave(state)
	if err != nil {
		return content, err
	}
	if len(state.PlanWavePending) > 0 {
		state.PlanContinueCallID = call.ID
	}
	return content, nil
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
	status := map[string]string{}
	planID := ""
	var revision int64
	var planStatus domain.PlanStatus
	if plan != nil {
		planID, revision = plan.ID, plan.Revision
		planStatus = plan.Status
		for _, node := range plan.Nodes {
			status[node.ID] = string(node.Status)
		}
	}
	payload, _ := json.Marshal(map[string]any{"tool": toolName, "plan_id": planID, "revision": revision, "status": planStatus, "nodes": status})
	return string(payload)
}
