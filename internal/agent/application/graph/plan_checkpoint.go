package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

const planCheckpointVersion = 1

// PlanCheckpointWriter is the narrow persistence boundary used by ReAct plan actions.
// BuildPlanExecuteGraph remains as a source-compatibility wrapper during migration.
type PlanCheckpointWriter interface {
	Upsert(ctx context.Context, tenantID string, cp domain.AgentExecutionCheckpoint) error
}

// BuildPlanExecuteGraph is retained only for callers compiled against the
// removed lazy-planning API; all execution now uses the unified ReAct graph.
func BuildPlanExecuteGraph(capGW port.CapabilityGateway, ledger TokenRecorder, _ PlanCheckpointWriter, _ func(context.Context, string, string, []domain.PlanStep), logger *zap.Logger) (*CompiledGraph[ReActState], error) {
	return BuildReActGraph(capGW, ledger, logger)
}

var ErrUnsupportedPlanCheckpoint = errors.New("unsupported plan checkpoint version")

type PlanCheckpointPayload struct {
	Plan                    *domain.Plan `json:"plan"`
	RemainingNodeBudget     int          `json:"remaining_node_budget"`
	RemainingRevisionBudget int64        `json:"remaining_revision_budget"`
	ActiveAttemptIDs        []string     `json:"active_attempt_ids,omitempty"`
	ActiveSkillID           string       `json:"active_skill_id,omitempty"`
	ActiveSkillRevisionID   string       `json:"active_skill_revision_id,omitempty"`
}

type planCheckpointEnvelope struct {
	Version int `json:"version"`
	PlanCheckpointPayload
}

type PlanCheckpointIdentity struct {
	CheckpointID   string
	ExecutionID    string
	TraceID        string
	ConversationID string
	AgentID        string
	UserID         string
}

func EncodePlanCheckpoint(payload PlanCheckpointPayload) ([]byte, error) {
	return json.Marshal(planCheckpointEnvelope{Version: planCheckpointVersion, PlanCheckpointPayload: payload})
}

func DecodePlanCheckpoint(data []byte) (PlanCheckpointPayload, error) {
	var envelope planCheckpointEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return PlanCheckpointPayload{}, fmt.Errorf("decode plan checkpoint: %w", err)
	}
	if envelope.Version != planCheckpointVersion {
		return PlanCheckpointPayload{}, fmt.Errorf("%w: %d", ErrUnsupportedPlanCheckpoint, envelope.Version)
	}
	if envelope.Plan != nil && envelope.Plan.ID == "" {
		return PlanCheckpointPayload{}, errors.New("decode plan checkpoint: plan id is required when plan is present")
	}
	return envelope.PlanCheckpointPayload, nil
}

// PersistPlanCheckpointSnapshot carries optional ReAct-level state to snapshot
// alongside the plan checkpoint. All fields are optional; nil values are skipped.
type PersistPlanCheckpointSnapshot struct {
	Messages     json.RawMessage // serialized []port.LLMMessage
	AllToolCalls json.RawMessage // serialized []port.ToolCall
	Steps        int
}

func PersistPlanCheckpoint(
	ctx context.Context,
	writer PlanCheckpointWriter,
	tenantID string,
	identity PlanCheckpointIdentity,
	payload PlanCheckpointPayload,
	snapshot *PersistPlanCheckpointSnapshot,
) error {
	if writer == nil {
		return errors.New("plan checkpoint: writer is required")
	}
	runtimeState, err := EncodePlanCheckpoint(payload)
	if err != nil {
		return fmt.Errorf("plan checkpoint: encode: %w", err)
	}
	now := time.Now().UTC()
	checkpoint := domain.AgentExecutionCheckpoint{
		ID: identity.CheckpointID, ExecutionID: identity.ExecutionID, TraceID: identity.TraceID,
		ConversationID: identity.ConversationID, AgentID: identity.AgentID, UserID: identity.UserID,
		RuntimeStateJSON: runtimeState, Status: "running", CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(constants.PlanCheckpointTTL),
	}
	if snapshot != nil {
		checkpoint.StepIndex = snapshot.Steps
		if len(snapshot.Messages) > 0 {
			checkpoint.MessagesSnapshotJSON = snapshot.Messages
		}
		if len(snapshot.AllToolCalls) > 0 {
			checkpoint.CompletedToolCallsJSON = snapshot.AllToolCalls
		}
	}
	if err := writer.Upsert(ctx, tenantID, checkpoint); err != nil {
		return fmt.Errorf("plan checkpoint: persist: %w", err)
	}
	return nil
}

// checkpointSnapshot builds a PersistPlanCheckpointSnapshot from a ReActState.
// Returns nil when state is nil.
func checkpointSnapshot(state *ReActState) *PersistPlanCheckpointSnapshot {
	if state == nil {
		return nil
	}
	snapshot := &PersistPlanCheckpointSnapshot{Steps: state.Steps}
	if len(state.Messages) > 0 {
		if encoded, err := json.Marshal(state.Messages); err == nil {
			snapshot.Messages = json.RawMessage(encoded)
		}
	}
	if len(state.AllToolCalls) > 0 {
		if encoded, err := json.Marshal(state.AllToolCalls); err == nil {
			snapshot.AllToolCalls = json.RawMessage(encoded)
		}
	}
	return snapshot
}

// buildReActRuntimeState encodes ReAct-only runtime state (ActiveSkill) into
// the checkpoint's RuntimeStateJSON. Returns {} when there is nothing to persist.
func buildReActRuntimeState(state *ReActState) json.RawMessage {
	if state == nil || state.ActiveSkill == nil {
		return json.RawMessage("{}")
	}
	payload := PlanCheckpointPayload{
		ActiveSkillID:         state.ActiveSkill.SkillID,
		ActiveSkillRevisionID: state.ActiveSkill.RevisionID,
	}
	if encoded, err := EncodePlanCheckpoint(payload); err == nil {
		return json.RawMessage(encoded)
	}
	return json.RawMessage("{}")
}

// PersistReActCheckpoint persists a lightweight ReAct execution checkpoint
// (no plan DAG). It snapshots Messages, tool calls, and the current graph node.
func PersistReActCheckpoint(
	ctx context.Context,
	writer PlanCheckpointWriter,
	tenantID string,
	identity PlanCheckpointIdentity,
	state *ReActState,
	currentNode string,
) error {
	if writer == nil || state == nil {
		return nil
	}
	snapshot := checkpointSnapshot(state)
	now := time.Now().UTC()
	cp := domain.AgentExecutionCheckpoint{
		ID:               identity.CheckpointID,
		ExecutionID:      identity.ExecutionID,
		TraceID:          identity.TraceID,
		ConversationID:   identity.ConversationID,
		AgentID:          identity.AgentID,
		UserID:           identity.UserID,
		CurrentNode:      currentNode,
		StepIndex:        state.Steps,
		RuntimeStateJSON: buildReActRuntimeState(state),
		Status:           "running",
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(constants.PlanCheckpointTTL),
	}
	if snapshot != nil {
		if len(snapshot.Messages) > 0 {
			cp.MessagesSnapshotJSON = snapshot.Messages
		}
		if len(snapshot.AllToolCalls) > 0 {
			cp.CompletedToolCallsJSON = snapshot.AllToolCalls
		}
		cp.StepIndex = snapshot.Steps
	}
	if identity.CheckpointID == "" {
		cp.ID = fmt.Sprintf("%s-step-%d", identity.ExecutionID, state.Steps)
	}
	return writer.Upsert(ctx, tenantID, cp)
}
