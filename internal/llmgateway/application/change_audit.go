package application

import (
	"context"
	"encoding/json"
	"fmt"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

type changeAuditInput struct {
	Kind       string
	ResourceID string
	Operation  string
	ActorID    string
	Before     any
	After      any
}

// newChangeAudit builds the audit event for a business write. before/after are
// safe projections (no credentials); nil means `{}`. A system actor in ctx
// (evaluation worker) is recorded with actor_type=system, source=optimization
// and overrides the caller-provided actor. 与 skill/knowledge 的副本保持同构。
func newChangeAudit(ctx context.Context, input changeAuditInput) (*auditdomain.ResourceChangeAuditEvent, error) {
	ev := &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: input.Kind, ResourceID: input.ResourceID, Operation: input.Operation,
		ActorID: input.ActorID, ActorType: auditdomain.ChangeActorUser,
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
	if input.Before != nil {
		ev.Before, err = json.Marshal(input.Before)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal before: %w", err)
		}
	}
	if input.After != nil {
		ev.After, err = json.Marshal(input.After)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal after: %w", err)
		}
	}
	return ev, nil
}

// modelSafeProjection 是模型的无敏感投影（模型不携带凭据，全字段可入审计）。
func modelSafeProjection(m *domain.Model) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": m.ID, "providerID": m.ProviderID, "name": m.Name, "displayName": m.DisplayName,
		"capabilities": m.Capabilities, "contextWindow": m.ContextWindow, "maxTokens": m.MaxTokens,
		"recommended": m.Recommended, "enabled": m.Enabled, "providerManaged": m.ProviderManaged,
		"samplingParams": m.SamplingParams, "maxTemperature": m.MaxTemperature,
	}
}

// providerSafeProjection 是 provider 的无敏感投影：显式排除 apiKey（凭据
// 不得进入审计投影，CLAUDE.md 日志/审计红线）。
func providerSafeProjection(p *domain.Provider) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": p.ID, "name": p.Name, "kind": p.Kind, "baseURL": p.BaseURL,
		"defaultModel": p.DefaultModel, "enabled": p.Enabled,
		"defaultSampling": p.DefaultSampling,
	}
}
