package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// newChangeAudit builds the audit event for a business write. before/after are
// safe projections (no credentials); nil means `{}`. A system actor in ctx
// (evaluation worker) is recorded with actor_type=system, source=optimization
// and overrides the caller-provided actor.
func newChangeAudit(ctx context.Context, kind, resourceID, op, actorID string, before, after any) (*auditdomain.ResourceChangeAuditEvent, error) {
	ev := &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: kind, ResourceID: resourceID, Operation: op,
		ActorID: actorID, ActorType: auditdomain.ChangeActorUser,
	}
	if sysActor := reqctx.SystemActorFromContext(ctx); sysActor != "" {
		ev.ActorID = sysActor
		ev.ActorType = auditdomain.ChangeActorSystem
		ev.Source = auditdomain.ChangeSourceOptimization
	} else {
		source, proposalID := reqctx.ChangeSourceFromContext(ctx)
		ev.Source = source
		if ev.Source == "" {
			ev.Source = auditdomain.ChangeSourceAPI
		}
		ev.ProposalID = proposalID
	}
	var err error
	if before != nil {
		ev.Before, err = json.Marshal(before)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal before: %w", err)
		}
	}
	if after != nil {
		ev.After, err = json.Marshal(after)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal after: %w", err)
		}
	}
	return ev, nil
}

// skillSafeProjection is the credential-free projection of a skill product row
// plus its draft. The skill carries no credentials; the draft contributes the
// change-relevant instruction content and hash.
func skillSafeProjection(skill port.SkillProductRow, draft *domain.SkillRevision) map[string]any {
	out := map[string]any{
		"id": skill.ID, "name": skill.Name, "description": skill.Description, "status": skill.Status,
	}
	if draft != nil {
		out["instructions"] = draft.Instructions
		out["contentHash"] = draft.ContentHash
	}
	return out
}

// skillSafeProjectionWithEditors extends the safe projection with the granted
// editor set, used by the editors-management endpoint's audit event.
func skillSafeProjectionWithEditors(skill port.SkillProductRow, draft *domain.SkillRevision, editors []string) map[string]any {
	out := skillSafeProjection(skill, draft)
	if editors == nil {
		editors = []string{}
	}
	out["editors"] = editors
	return out
}

// isBuiltinSkill identifies platform-seeded skills. Their lifecycle is managed
// by the platform; tenant writes are rejected.
func isBuiltinSkill(skillID string) bool {
	return strings.HasPrefix(skillID, "builtin:")
}
