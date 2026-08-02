package domain

import (
	"encoding/json"
	"time"
)

// AuditActorType classifies who performed the audited action.
type AuditActorType string

const (
	ActorTypeUser    AuditActorType = "user"
	ActorTypeService AuditActorType = "service"
	ActorTypeSystem  AuditActorType = "system"
)

// AuditActor identifies the principal that triggered an auditable event.
type AuditActor struct {
	ActorType AuditActorType `json:"actor_type"`
	ActorID   string         `json:"actor_id"`
}

// AuditEvent is the append-only record of a single platform operation.
type AuditEvent struct {
	ID           string          `json:"id"`
	TenantID     string          `json:"tenant_id"`
	Actor        AuditActor      `json:"actor"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Before       json.RawMessage `json:"before,omitempty"`
	After        json.RawMessage `json:"after,omitempty"`
	RequestID    string          `json:"request_id"`
	TraceID      string          `json:"trace_id"`
	RiskLevel    string          `json:"risk_level"`
	Outcome      string          `json:"outcome"`
	OccurredAt   time.Time       `json:"occurred_at"`
}

// AuditFilter narrows audit queries by tenant, actor, resource, risk, and time.
type AuditFilter struct {
	TenantID     string
	ActorID      string
	ResourceType string
	ResourceID   string
	RiskLevel    string
	Action       string
	Outcome      string
	From         time.Time
	To           time.Time
	Limit        int
	Offset       int
}
