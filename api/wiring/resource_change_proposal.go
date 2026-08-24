package wiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

type proposalAgentService interface {
	Create(context.Context, agentapp.CreateAgentInput) (agentapp.AgentDTO, error)
	Get(context.Context, string) (agentapp.AgentDTO, error)
	Update(context.Context, string, agentapp.UpdateAgentInput) (agentapp.AgentDTO, error)
}

type proposalSkillService interface {
	CreateSkillDraft(context.Context, skillapp.CreateSkillDraftInput) (skillapp.SkillWorkspaceView, error)
	GetWorkspace(context.Context, string, string) (skillapp.SkillWorkspaceView, error)
	UpdateDraftBundle(context.Context, string, string, skillapp.UpdateDraftBundleInput) (skillapp.SkillWorkspaceView, error)
}

type proposalMCPService interface {
	ConnectServer(context.Context, *mcpdomain.ServerConfig, []string, string) error
	UpdateServer(context.Context, *mcpdomain.ServerConfig, string) error
	GetServerConfig(context.Context, string) (*mcpdomain.ServerConfig, error)
}

type proposalKnowledgeService interface {
	CreateWorkspace(context.Context, string, knowledgeapp.CreateWorkspaceInput, string) (*knowledgedomain.Workspace, error)
	UpdateWorkspace(context.Context, string, string, knowledgeapp.UpdateWorkspaceInput, string) (*knowledgedomain.Workspace, error)
	GetWorkspaceByID(context.Context, string, string) (*knowledgedomain.Workspace, error)
}

type ResourceChangeProposalAdapters struct {
	agents    proposalAgentService
	skills    proposalSkillService
	mcp       proposalMCPService
	knowledge proposalKnowledgeService
}

type proposalAuthorizer struct {
	roles agentport.TenantRoleResolver
}

func (a proposalAuthorizer) AuthorizeProposal(
	ctx context.Context,
	tenantID, actorID string,
	_ agentdomain.ResourceKind,
	_ agentdomain.ProposalOperation,
	action agentdomain.ProposalAction,
) error {
	if a.roles == nil {
		return agentdomain.ErrProposalForbidden
	}
	role, err := a.roles.ResolveTenantRole(ctx, tenantID, actorID)
	if err != nil {
		return agentdomain.ErrProposalForbidden
	}
	switch role {
	case "admin", "owner":
		return nil
	case "member":
		// D6：member 可创建提案（落地 ready_for_review 交由审批流），
		// 编辑/取消/确认/应用仍仅 admin/owner。
		if action == agentdomain.ProposalActionCreate {
			return nil
		}
	}
	return agentdomain.ErrProposalForbidden
}

func NewResourceChangeProposalAdapters(
	agents proposalAgentService,
	skills proposalSkillService,
	mcp proposalMCPService,
	knowledge proposalKnowledgeService,
) *ResourceChangeProposalAdapters {
	return &ResourceChangeProposalAdapters{agents: agents, skills: skills, mcp: mcp, knowledge: knowledge}
}

func (a *ResourceChangeProposalAdapters) ResolveBaseline(
	ctx context.Context,
	proposal agentdomain.ResourceChangeProposal,
) (agentport.ResourceBaseline, error) {
	var projection any
	var fingerprintValue string
	switch proposal.ResourceKind {
	case agentdomain.ResourceAgent:
		value, err := a.agents.Get(ctx, proposal.ResourceID)
		if err != nil {
			return agentport.ResourceBaseline{}, err
		}
		projection = agentChangeProjection(value)
	case agentdomain.ResourceSkillDraft:
		value, err := a.skills.GetWorkspace(ctx, proposal.ResourceID, proposal.ProposerID)
		if err != nil {
			return agentport.ResourceBaseline{}, err
		}
		if value.Draft.Status != skilldomain.VersionStatusDraft {
			return agentport.ResourceBaseline{}, skilldomain.ErrSkillNotFound
		}
		projection = map[string]any{"name": value.Skill.Name, "description": value.Skill.Description,
			"instructions": value.Draft.Instructions}
		fingerprintValue = value.Draft.ContentHash
	case agentdomain.ResourceMCPConfig:
		value, err := a.mcp.GetServerConfig(ctx, proposal.ResourceID)
		if err != nil {
			return agentport.ResourceBaseline{}, err
		}
		projection = mcpChangeProjection(value)
		fingerprintValue, err = fingerprint(value)
		if err != nil {
			return agentport.ResourceBaseline{}, err
		}
	case agentdomain.ResourceKnowledgeWorkspace:
		value, err := a.knowledge.GetWorkspaceByID(ctx, proposal.TenantID, proposal.ResourceID)
		if err != nil {
			return agentport.ResourceBaseline{}, err
		}
		projection = map[string]any{"name": value.Name, "description": value.Description,
			"embeddingModel": value.Config.EmbeddingModel}
	default:
		return agentport.ResourceBaseline{}, agentdomain.ErrProposalInvalid
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return agentport.ResourceBaseline{}, fmt.Errorf("marshal resource baseline: %w", err)
	}
	if fingerprintValue == "" {
		fingerprintValue, err = fingerprint(projection)
		if err != nil {
			return agentport.ResourceBaseline{}, err
		}
	}
	return agentport.ResourceBaseline{Fingerprint: fingerprintValue, Projection: encoded}, nil
}

func (a *ResourceChangeProposalAdapters) ApplyResourceChange(
	ctx context.Context,
	envelope agentdomain.ProposalEnvelope,
) (agentdomain.ApplyResult, error) {
	proposal := envelope.Proposal
	switch payload := envelope.Payload.(type) {
	case *agentdomain.AgentChange:
		ctx, actorID := proposalApplyContext(ctx, proposal)
		return a.applyAgentChange(ctx, proposal.TenantID, proposal.ResourceID, proposal.Operation, payload, actorID)
	case *agentdomain.SkillDraftChange:
		ctx, actorID := proposalApplyContext(ctx, proposal)
		return a.applySkillChange(ctx, proposal.TenantID, proposal.ResourceID, proposal.BaselineFingerprint,
			proposal.Operation, payload, actorID)
	case *agentdomain.MCPConfigChange:
		ctx, actorID := proposalApplyContext(ctx, proposal)
		return a.applyMCPChange(ctx, proposal.TenantID, proposal.ResourceID, proposal.Operation, payload, actorID)
	case *agentdomain.KnowledgeWorkspaceChange:
		ctx, actorID := proposalApplyContext(ctx, proposal)
		return a.applyKnowledgeChange(ctx, proposal.TenantID, proposal.ResourceID, proposal.Operation, payload, actorID)
	default:
		return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrProposalInvalid)
	}
}

// ApplyDirect applies a resource change immediately, bypassing the proposal
// lifecycle. Used by the system assistant in-process direct-write tool:
// payload is decoded strictly (unknown fields rejected), tenant identity is
// stamped on ctx, and the audit row is written with source=system_assistant.
// Ownership is still enforced by the service layer against actorID (the
// conversation initiator) — the direct write is not an isolation bypass.
// ApplyDirectFromTool adapts the system assistant tool argument map into the
// typed ApplyDirect entry point. TenantID is read from the execution context;
// actorID is the conversation initiator whose permissions are inherited.
func (a *ResourceChangeProposalAdapters) ApplyDirectFromTool(
	ctx context.Context,
	actorID string,
	args map[string]any,
) (agentdomain.ApplyResult, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	if tenantID == "" {
		return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrProposalInvalid)
	}
	kind, operation, resourceID, payload, parseErr := agentapp.ParseResourceChangeToolArguments(args)
	if parseErr != nil {
		return agentdomain.ApplyResult{}, definiteApplyError(parseErr)
	}
	return a.ApplyDirect(ctx, tenantID, actorID, kind, operation, resourceID, payload)
}

func (a *ResourceChangeProposalAdapters) ApplyDirect(
	ctx context.Context,
	tenantID, actorID string,
	kind agentdomain.ResourceKind,
	operation agentdomain.ProposalOperation,
	resourceID string,
	payload []byte,
) (agentdomain.ApplyResult, error) {
	if operation == agentdomain.OperationUpdate && resourceID == "" {
		return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrProposalInvalid)
	}
	if operation == agentdomain.OperationCreate && resourceID != "" {
		return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrProposalInvalid)
	}
	decoded, err := agentdomain.DecodeProposalPayload(kind, operation, payload)
	if err != nil {
		return agentdomain.ApplyResult{}, definiteApplyError(err)
	}
	ctx = reqctx.WithTenantID(ctx, tenantID)
	ctx = reqctx.WithChangeSource(ctx, auditdomain.ChangeSourceSystemAssistantDirect, "")
	switch change := decoded.(type) {
	case *agentdomain.AgentChange:
		return a.applyAgentChange(ctx, tenantID, resourceID, operation, change, actorID)
	case *agentdomain.SkillDraftChange:
		// Direct writes have no baseline: the fingerprint is intentionally
		// empty, matching the semantics of a plain API update.
		return a.applySkillChange(ctx, tenantID, resourceID, "", operation, change, actorID)
	case *agentdomain.MCPConfigChange:
		return a.applyMCPChange(ctx, tenantID, resourceID, operation, change, actorID)
	case *agentdomain.KnowledgeWorkspaceChange:
		return a.applyKnowledgeChange(ctx, tenantID, resourceID, operation, change, actorID)
	default:
		return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrProposalInvalid)
	}
}

// proposalApplyContext stamps the audit source/proposal on ctx and resolves
// the acting user: the confirmer, falling back to the proposer. The service
// layer re-validates ownership against this actor.
func proposalApplyContext(ctx context.Context, proposal agentdomain.ResourceChangeProposal) (context.Context, string) {
	// The apply path must carry tenant identity: ownership checks and audit
	// reads resolve the tenant from ctx, and replays/worker-driven applies
	// cannot rely on request-scoped middleware to have stamped it.
	ctx = reqctx.WithTenantID(ctx, proposal.TenantID)
	ctx = reqctx.WithChangeSource(ctx, auditdomain.ChangeSourceProposalApply, proposal.ID)
	actorID := proposal.ConfirmerID
	if actorID == "" {
		actorID = proposal.ProposerID
	}
	return ctx, actorID
}

func (a *ResourceChangeProposalAdapters) applyAgentChange(
	ctx context.Context,
	tenantID, resourceID string,
	operation agentdomain.ProposalOperation,
	change *agentdomain.AgentChange,
	actorID string,
) (agentdomain.ApplyResult, error) {
	var value agentapp.AgentDTO
	var err error
	if operation == agentdomain.OperationCreate {
		value, err = a.agents.Create(ctx, agentapp.CreateAgentInput{
			TenantID: tenantID, Name: change.Name, Type: "react", Description: change.Description,
			// create 透传 systemPrompt：修复现存 silent-drop——AgentChange 早已
			// 携带该字段，但 create 分支从未写入，模型创建的 agent 永远丢 prompt。
			SystemPrompt: change.SystemPrompt, LLMModel: change.Model,
			MaxIterations: change.MaxIterations, MaxContextTokens: change.MaxContextTokens,
			AllowedSkills: change.SkillIDs, MCPToolIDs: change.MCPToolIDs,
			KnowledgeWorkspaceIDs: change.WorkspaceIDs, MemoryScope: "user",
			ActorID: actorID,
		})
	} else {
		existing, getErr := a.agents.Get(ctx, resourceID)
		if getErr != nil {
			return agentdomain.ApplyResult{}, definiteApplyError(getErr)
		}
		// update 非空才覆盖 systemPrompt：buildUpdateConfig 对空串直接赋值会
		// 清除既有 prompt，而 proposal 默认省略该字段，不能因省略而误清。
		systemPrompt := existing.SystemPrompt
		if change.SystemPrompt != "" {
			systemPrompt = change.SystemPrompt
		}
		value, err = a.agents.Update(ctx, resourceID, agentapp.UpdateAgentInput{
			Name: change.Name, Type: existing.Type, Description: change.Description, SystemPrompt: systemPrompt,
			LLMModel: change.Model, MaxIterations: change.MaxIterations, MaxContextTokens: change.MaxContextTokens,
			AllowedSkills: change.SkillIDs, MCPToolIDs: change.MCPToolIDs,
			KnowledgeWorkspaceIDs: change.WorkspaceIDs, MemoryScope: existing.MemoryScope,
			ActorID: actorID,
		})
	}
	if err != nil {
		return agentdomain.ApplyResult{}, definiteApplyError(err)
	}
	return safeApplyResult(value.ID, agentSafeProjection(value))
}

func (a *ResourceChangeProposalAdapters) applySkillChange(
	ctx context.Context,
	tenantID, resourceID, fingerprint string,
	operation agentdomain.ProposalOperation,
	change *agentdomain.SkillDraftChange,
	actorID string,
) (agentdomain.ApplyResult, error) {
	var value skillapp.SkillWorkspaceView
	var err error
	if operation == agentdomain.OperationCreate {
		value, err = a.skills.CreateSkillDraft(ctx, skillapp.CreateSkillDraftInput{
			Name: change.Name, Goal: change.Description, WhenToUse: change.Description,
			Instructions: change.Instructions, ActorID: actorID,
		})
	} else {
		value, err = a.skills.UpdateDraftBundle(ctx, resourceID, fingerprint,
			skillapp.UpdateDraftBundleInput{
				Name: change.Name, Description: change.Description, Instructions: change.Instructions, ActorID: actorID,
			})
	}
	if err != nil {
		return agentdomain.ApplyResult{}, definiteApplyError(err)
	}
	projection := map[string]any{
		"id": value.Skill.ID, "name": value.Skill.Name, "description": value.Skill.Description,
		"status": value.Draft.Status, "contentHash": value.Draft.ContentHash,
	}
	return safeApplyResult(value.Skill.ID, projection)
}

func (a *ResourceChangeProposalAdapters) applyMCPChange(
	ctx context.Context,
	tenantID, resourceID string,
	operation agentdomain.ProposalOperation,
	change *agentdomain.MCPConfigChange,
	actorID string,
) (agentdomain.ApplyResult, error) {
	id := resourceID
	config := mcpConfigFromChange(change)
	if operation == agentdomain.OperationCreate {
		id = uuid.Must(uuid.NewV7()).String()
		config.ID = id
		config.Auth = &mcpdomain.AuthConfig{Type: mcpdomain.AuthTypeNone}
		config.Env = map[string]string{}
		config.Headers = map[string]string{}
		if err := a.mcp.ConnectServer(ctx, config, nil, actorID); err != nil {
			return agentdomain.ApplyResult{}, definiteApplyError(err)
		}
	} else {
		stored, err := a.mcp.GetServerConfig(ctx, id)
		if err != nil {
			return agentdomain.ApplyResult{}, definiteApplyError(err)
		}
		if change.Transport != stored.Transport {
			return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrProposalInvalid)
		}
		config.ID = id
		config.Env = cloneStringValues(stored.Env)
		config.Headers = cloneStringValues(stored.Headers)
		config.Auth = cloneMCPAuth(stored.Auth)
		if err := a.mcp.UpdateServer(ctx, config, actorID); err != nil {
			return agentdomain.ApplyResult{}, &agentport.ResourceApplyError{
				Outcome: agentport.ResourceApplyUnknownOutcome,
				Err:     err,
			}
		}
	}
	readback, err := a.mcp.GetServerConfig(ctx, id)
	if err != nil {
		return agentdomain.ApplyResult{}, &agentport.ResourceApplyError{Outcome: agentport.ResourceApplyUnknownOutcome, Err: err}
	}
	return safeApplyResult(id, mcpapp.MCPSafeProjection(readback))
}

func (a *ResourceChangeProposalAdapters) applyKnowledgeChange(
	ctx context.Context,
	tenantID, resourceID string,
	operation agentdomain.ProposalOperation,
	change *agentdomain.KnowledgeWorkspaceChange,
	actorID string,
) (agentdomain.ApplyResult, error) {
	config := knowledgedomain.WorkspaceConfig{EmbeddingModel: change.EmbeddingModel}
	var value *knowledgedomain.Workspace
	var err error
	if operation == agentdomain.OperationCreate {
		value, err = a.knowledge.CreateWorkspace(ctx, tenantID, knowledgeapp.CreateWorkspaceInput{
			Name: change.Name, Description: change.Description, Config: config,
		}, actorID)
		if err != nil {
			return agentdomain.ApplyResult{}, &agentport.ResourceApplyError{
				Outcome: agentport.ResourceApplyUnknownOutcome,
				Err:     err,
			}
		}
	} else {
		existing, getErr := a.knowledge.GetWorkspaceByID(ctx, tenantID, resourceID)
		if getErr != nil {
			return agentdomain.ApplyResult{}, definiteApplyError(getErr)
		}
		if change.Name != "" && change.Name != existing.Name {
			return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrProposalInvalid)
		}
		description := change.Description
		value, err = a.knowledge.UpdateWorkspace(ctx, tenantID, existing.Name, knowledgeapp.UpdateWorkspaceInput{
			Description: &description, Config: &config,
		}, actorID)
	}
	if err != nil {
		return agentdomain.ApplyResult{}, definiteApplyError(err)
	}
	return safeApplyResult(value.ID, knowledgeapp.KnowledgeSafeProjection(value))
}

// agentSafeProjection is the DTO shim over the application-layer projection;
// both change audits and proposal readbacks share one field mapping.
func agentSafeProjection(value agentapp.AgentDTO) map[string]any {
	return agentapp.AgentSafeProjection(&agentdomain.AgentConfig{
		ID: value.ID, Name: value.Name, Type: agentdomain.AgentType(value.Type), Description: value.Description,
		LLMModel: value.LLMModel, MaxIterations: value.MaxIterations, MaxContextTokens: value.MaxContextTokens,
		AllowedSkills: value.AllowedSkills, MCPToolIDs: value.MCPToolIDs, KnowledgeWorkspaceIDs: value.KnowledgeWorkspaceIDs,
	})
}

func agentChangeProjection(value agentapp.AgentDTO) map[string]any {
	return map[string]any{
		"name": value.Name, "description": value.Description, "model": value.LLMModel,
		"maxIterations": value.MaxIterations, "maxContextTokens": value.MaxContextTokens,
		"skillIds": value.AllowedSkills, "mcpToolIds": value.MCPToolIDs,
		"workspaceIds": value.KnowledgeWorkspaceIDs,
	}
}

func mcpChangeProjection(value *mcpdomain.ServerConfig) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	projection := map[string]any{
		"name": value.Name, "version": value.Version, "transport": value.Transport,
		"timeoutSec": int(value.Timeout / time.Second),
	}
	if value.Command != "" {
		projection["command"] = value.Command
	}
	if len(value.Args) > 0 {
		projection["args"] = value.Args
	}
	if safeURL := mcpapp.MCPSafeURL(value.URL); safeURL != "" {
		projection["url"] = safeURL
	}
	if len(value.Capabilities) > 0 {
		projection["capabilities"] = value.Capabilities
	}
	if value.Retry != nil {
		projection["retry"] = map[string]any{
			"enabled": value.Retry.Enabled, "maxRetries": value.Retry.MaxRetries,
			"initialDelayMs": value.Retry.InitialDelayMs, "maxDelayMs": value.Retry.MaxDelayMs,
			"backoffFactor": value.Retry.BackoffFactor,
		}
	}
	return projection
}

func mcpConfigFromChange(change *agentdomain.MCPConfigChange) *mcpdomain.ServerConfig {
	config := &mcpdomain.ServerConfig{
		Name: change.Name, Version: change.Version, Transport: change.Transport, Command: change.Command,
		Args: append([]string(nil), change.Args...), URL: change.URL,
		Capabilities: append([]string(nil), change.Capabilities...), Timeout: time.Duration(change.TimeoutSec) * time.Second,
	}
	if change.Retry != nil {
		config.Retry = &mcpdomain.RetryConfig{
			Enabled: change.Retry.Enabled, MaxRetries: change.Retry.MaxRetries,
			InitialDelayMs: change.Retry.InitialDelayMs, MaxDelayMs: change.Retry.MaxDelayMs,
			BackoffFactor: change.Retry.BackoffFactor,
		}
	}
	return config
}

func safeApplyResult(resourceID string, projection any) (agentdomain.ApplyResult, error) {
	readback, err := json.Marshal(projection)
	if err != nil {
		return agentdomain.ApplyResult{}, definiteApplyError(err)
	}
	fingerprint, err := fingerprint(projection)
	if err != nil {
		return agentdomain.ApplyResult{}, definiteApplyError(err)
	}
	return agentdomain.ApplyResult{ResourceID: resourceID, Fingerprint: fingerprint, Readback: readback}, nil
}

func fingerprint(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("fingerprint resource projection: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func definiteApplyError(err error) error {
	var classified *agentport.ResourceApplyError
	if errors.As(err, &classified) {
		return err
	}
	return &agentport.ResourceApplyError{Outcome: agentport.ResourceApplyDefiniteFailure, Err: err}
}

func cloneStringValues(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneMCPAuth(value *mcpdomain.AuthConfig) *mcpdomain.AuthConfig {
	if value == nil {
		return &mcpdomain.AuthConfig{Type: mcpdomain.AuthTypeNone}
	}
	cloned := *value
	cloned.OAuth2Scopes = append([]string(nil), value.OAuth2Scopes...)
	return &cloned
}
