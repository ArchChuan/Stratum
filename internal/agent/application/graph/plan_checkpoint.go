package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

const planCheckpointVersion = 1

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
