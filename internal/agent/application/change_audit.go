package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
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

// agentSafeProjection is the credential-free projection shared by change
// audits and proposal readbacks (wiring references this via the DTO shim).
func AgentSafeProjection(cfg *domain.AgentConfig) map[string]any {
	if cfg == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": cfg.ID, "name": cfg.Name, "type": string(cfg.Type), "description": cfg.Description,
		"model": cfg.LLMModel, "maxIterations": cfg.MaxIterations, "maxContextTokens": cfg.MaxContextTokens,
		"skillIds": cfg.AllowedSkills, "mcpToolIds": cfg.MCPToolIDs, "workspaceIds": cfg.KnowledgeWorkspaceIDs,
	}
}

// AgentSafeProjectionWithEditors extends the safe projection with the granted
// editor set (used by the editor-management audit).
func AgentSafeProjectionWithEditors(cfg *domain.AgentConfig, editors []string) map[string]any {
	p := AgentSafeProjection(cfg)
	if editors == nil {
		editors = []string{}
	}
	p["editors"] = editors
	return p
}

// AgentDTOSafeProjection 是 AgentSafeProjection 的 DTO 版本：List 返回 AgentDTO
// 而非 AgentConfig，stratum_list_agents 装配闭包复用同一字段集做安全投影，
// 不携带 systemPrompt/systemKey 等敏感字段。与 AgentSafeProjection 保持同构，
// 避免两条投影规则漂移。
func AgentDTOSafeProjection(dto AgentDTO) map[string]any {
	return map[string]any{
		"id": dto.ID, "name": dto.Name, "type": string(dto.Type), "description": dto.Description,
		"model": dto.LLMModel, "maxIterations": dto.MaxIterations, "maxContextTokens": dto.MaxContextTokens,
		"skillIds": dto.AllowedSkills, "mcpToolIds": dto.MCPToolIDs, "workspaceIds": dto.KnowledgeWorkspaceIDs,
	}
}
