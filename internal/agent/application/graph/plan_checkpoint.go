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
	if envelope.Plan == nil || envelope.Plan.ID == "" {
		return PlanCheckpointPayload{}, errors.New("decode plan checkpoint: plan is required")
	}
	return envelope.PlanCheckpointPayload, nil
}

func PersistPlanCheckpoint(
	ctx context.Context,
	writer PlanCheckpointWriter,
	tenantID string,
	identity PlanCheckpointIdentity,
	payload PlanCheckpointPayload,
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
	if err := writer.Upsert(ctx, tenantID, checkpoint); err != nil {
		return fmt.Errorf("plan checkpoint: persist: %w", err)
	}
	return nil
}
