package application

import (
	"context"
	"encoding/json"
	"fmt"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
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

// knowledgeSafeProjectionWithEditors is the credential-free projection of a
// knowledge workspace. WorkspaceConfig carries no credentials; it is the
// chunking contract, not storage keys. editors is included when non-nil so
// editor-set changes surface in the audit before/after diff.
func knowledgeSafeProjectionWithEditors(value *domain.Workspace, editors []string) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	proj := map[string]any{
		"id": value.ID, "name": value.Name, "description": value.Description, "config": value.Config,
	}
	if editors != nil {
		proj["editors"] = editors
	}
	return proj
}

// KnowledgeSafeProjection is the credential-free projection of a knowledge
// workspace (audit write paths that do not involve the editor set).
func KnowledgeSafeProjection(value *domain.Workspace) map[string]any {
	return knowledgeSafeProjectionWithEditors(value, nil)
}
