package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrExperimentStateConflict      = errors.New("experiment state version conflict")
	ErrExperimentCommandConflict    = errors.New("experiment command idempotency conflict")
	ErrExperimentCommandNotAllowed  = errors.New("experiment command not allowed")
	ErrExperimentDeploymentConflict = errors.New("resource already has an active experiment deployment")
)

type ExperimentCommandAction string

const (
	CommandPause    ExperimentCommandAction = "pause"
	CommandPromote  ExperimentCommandAction = "promote"
	CommandRollback ExperimentCommandAction = "rollback"
)

type ActorType string

const (
	ActorTypeAdmin  ActorType = "admin"
	ActorTypeSystem ActorType = "system"
)

type ExperimentCommand struct {
	ActorID              string    `json:"actor_id"`
	ActorType            ActorType `json:"actor_type"`
	Reason               string    `json:"reason"`
	IdempotencyKey       string    `json:"idempotency_key"`
	ExpectedStateVersion int64     `json:"expected_state_version"`
}

func MetricsFingerprint(metrics StageMetrics) string {
	payload, _ := json.Marshal(metrics)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (c ExperimentCommand) Validate() error {
	if strings.TrimSpace(c.ActorID) == "" || strings.TrimSpace(string(c.ActorType)) == "" ||
		strings.TrimSpace(c.Reason) == "" || strings.TrimSpace(c.IdempotencyKey) == "" || c.ExpectedStateVersion <= 0 {
		return errors.New("actor, actor type, reason, idempotency key, and expected state version are required")
	}
	if c.ActorType != ActorTypeAdmin {
		return errors.New("experiment commands require an admin actor")
	}
	return nil
}

func (c ExperimentCommand) Fingerprint(action ExperimentCommandAction) string {
	value := fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%d",
		action, c.ActorType, c.ActorID, c.Reason, c.ExpectedStateVersion,
	)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
