package domain

import "encoding/json"

// Resource change audit: one row per committed create/update/delete of a
// tenant-managed resource (agent, skill, mcp_config, knowledge workspace).
// Written in the same transaction as the business change; projections must be
// de-sensitized (no credentials).

// Resource kinds audited by the ownership/audit feature.
const (
	ResourceKindAgent     = "agent"
	ResourceKindSkill     = "skill"
	ResourceKindMCP       = "mcp"
	ResourceKindKnowledge = "knowledge"
)

// Operations recorded for each committed change.
const (
	ChangeOpCreate = "create"
	ChangeOpUpdate = "update"
	ChangeOpDelete = "delete"
)

// Actor types: who performed the change.
const (
	ChangeActorUser            = "user"
	ChangeActorSystemAssistant = "system_assistant"
	ChangeActorSystem          = "system"
)

// Change sources: which write path produced the change.
const (
	ChangeSourceAPI           = "api"
	ChangeSourceProposalApply = "proposal_apply"
	ChangeSourceOptimization  = "optimization"
)

// ResourceChangeAuditEvent is the pure, context-free record of one committed
// resource change. Before/After carry opaque JSON projections; each owning
// context marshals its own domain types at the boundary.
type ResourceChangeAuditEvent struct {
	ResourceKind string // agent|skill|mcp|knowledge
	ResourceID   string
	Operation    string // create|update|delete
	ActorID      string
	ActorType    string          // user|system_assistant|system
	Source       string          // api|proposal_apply|optimization
	ProposalID   string          // set when Source == proposal_apply
	Before       json.RawMessage // projection of the pre-change state; {} for create
	After        json.RawMessage // projection of the post-change state
}

// Normalized returns a copy with storage defaults filled: empty before/after
// become {}, actor_type defaults to user and source to api. Repositories call
// this once before the INSERT so every write path applies the same defaults.
// A nil event stays nil.
func (ev *ResourceChangeAuditEvent) Normalized() *ResourceChangeAuditEvent {
	if ev == nil {
		return nil
	}
	before := ev.Before
	if len(before) == 0 {
		before = json.RawMessage(`{}`)
	}
	after := ev.After
	if len(after) == 0 {
		after = json.RawMessage(`{}`)
	}
	actorType := ev.ActorType
	if actorType == "" {
		actorType = ChangeActorUser
	}
	source := ev.Source
	if source == "" {
		source = ChangeSourceAPI
	}
	ev.Before, ev.After, ev.ActorType, ev.Source = before, after, actorType, source
	return ev
}

// ChangeAuditInsertSQL is the shared column contract for persisting a change
// audit row inside the business transaction. Each repository uses this exact
// statement so the column list stays in lockstep with tenant_schema.sql
// (asserted by change_audit_insert_test.go).
const ChangeAuditInsertSQL = `INSERT INTO resource_change_audits
    (id, tenant_id, resource_kind, resource_id, operation, actor_id, actor_type,
     source, proposal_id, before_projection, after_projection)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
