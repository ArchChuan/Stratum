package wiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/google/uuid"
)

type proposalAgentService interface {
	Create(context.Context, agentapp.CreateAgentInput) (agentapp.AgentDTO, error)
	Get(context.Context, string) (agentapp.AgentDTO, error)
	Update(context.Context, string, agentapp.UpdateAgentInput) (agentapp.AgentDTO, error)
}

type proposalSkillService interface {
	CreateSkillDraft(context.Context, skillapp.CreateSkillDraftInput) (skillapp.SkillWorkspaceView, error)
	GetWorkspace(context.Context, string) (skillapp.SkillWorkspaceView, error)
	UpdateDraftBundle(context.Context, string, string, skillapp.UpdateDraftBundleInput) (skillapp.SkillWorkspaceView, error)
}

type proposalMCPService interface {
	ConnectServer(context.Context, *mcpdomain.ServerConfig) error
	UpdateServer(context.Context, *mcpdomain.ServerConfig) error
	GetServerConfig(context.Context, string) (*mcpdomain.ServerConfig, error)
}

type proposalKnowledgeService interface {
	CreateWorkspace(context.Context, string, knowledgeapp.CreateWorkspaceInput) (*knowledgedomain.Workspace, error)
	UpdateWorkspace(context.Context, string, string, knowledgeapp.UpdateWorkspaceInput) (*knowledgedomain.Workspace, error)
	GetWorkspace(context.Context, string, string) (*knowledgedomain.Workspace, error)
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
) error {
	if a.roles == nil {
		return agentdomain.ErrProposalForbidden
	}
	role, err := a.roles.ResolveTenantRole(ctx, tenantID, actorID)
	if err != nil {
		return agentdomain.ErrProposalForbidden
	}
	if role != "admin" && role != "owner" {
		return agentdomain.ErrProposalForbidden
	}
	return nil
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
) (string, error) {
	switch proposal.ResourceKind {
	case agentdomain.ResourceAgent:
		value, err := a.agents.Get(ctx, proposal.ResourceID)
		if err != nil {
			return "", err
		}
		if value.SystemKey != "" {
			return "", agentdomain.ErrSystemAssistantManaged
		}
		return fingerprint(agentSafeProjection(value))
	case agentdomain.ResourceSkillDraft:
		value, err := a.skills.GetWorkspace(ctx, proposal.ResourceID)
		if err != nil {
			return "", err
		}
		if value.Draft.Status != skilldomain.VersionStatusDraft {
			return "", skilldomain.ErrSkillNotFound
		}
		return value.Draft.ContentHash, nil
	case agentdomain.ResourceMCPConfig:
		value, err := a.mcp.GetServerConfig(ctx, proposal.ResourceID)
		if err != nil {
			return "", err
		}
		return fingerprint(mcpSafeProjection(value))
	case agentdomain.ResourceKnowledgeWorkspace:
		value, err := a.knowledge.GetWorkspace(ctx, proposal.TenantID, proposal.ResourceID)
		if err != nil {
			return "", err
		}
		return fingerprint(knowledgeSafeProjection(value))
	default:
		return "", agentdomain.ErrProposalInvalid
	}
}

func (a *ResourceChangeProposalAdapters) ApplyResourceChange(
	ctx context.Context,
	envelope agentdomain.ProposalEnvelope,
) (agentdomain.ApplyResult, error) {
	switch payload := envelope.Payload.(type) {
	case *agentdomain.AgentChange:
		return a.applyAgent(ctx, envelope.Proposal, payload)
	case *agentdomain.SkillDraftChange:
		return a.applySkill(ctx, envelope.Proposal, payload)
	case *agentdomain.MCPConfigChange:
		return a.applyMCP(ctx, envelope.Proposal, payload)
	case *agentdomain.KnowledgeWorkspaceChange:
		return a.applyKnowledge(ctx, envelope.Proposal, payload)
	default:
		return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrProposalInvalid)
	}
}

func (a *ResourceChangeProposalAdapters) applyAgent(
	ctx context.Context,
	proposal agentdomain.ResourceChangeProposal,
	change *agentdomain.AgentChange,
) (agentdomain.ApplyResult, error) {
	var value agentapp.AgentDTO
	var err error
	if proposal.Operation == agentdomain.OperationCreate {
		value, err = a.agents.Create(ctx, agentapp.CreateAgentInput{
			TenantID: proposal.TenantID, Name: change.Name, Type: "react", Description: change.Description,
			LLMModel: change.Model, MaxIterations: change.MaxIterations, MaxContextTokens: change.MaxContextTokens,
			AllowedSkills: change.SkillIDs, MCPToolIDs: change.MCPToolIDs,
			KnowledgeWorkspaceIDs: change.WorkspaceIDs, MemoryScope: "user",
		})
	} else {
		existing, getErr := a.agents.Get(ctx, proposal.ResourceID)
		if getErr != nil {
			return agentdomain.ApplyResult{}, definiteApplyError(getErr)
		}
		if existing.SystemKey != "" {
			return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrSystemAssistantManaged)
		}
		value, err = a.agents.Update(ctx, proposal.ResourceID, agentapp.UpdateAgentInput{
			Name: change.Name, Type: existing.Type, Description: change.Description, SystemPrompt: existing.SystemPrompt,
			LLMModel: change.Model, MaxIterations: change.MaxIterations, MaxContextTokens: change.MaxContextTokens,
			AllowedSkills: change.SkillIDs, MCPToolIDs: change.MCPToolIDs,
			KnowledgeWorkspaceIDs: change.WorkspaceIDs, MemoryScope: existing.MemoryScope,
		})
	}
	if err != nil {
		return agentdomain.ApplyResult{}, definiteApplyError(err)
	}
	return safeApplyResult(value.ID, agentSafeProjection(value))
}

func (a *ResourceChangeProposalAdapters) applySkill(
	ctx context.Context,
	proposal agentdomain.ResourceChangeProposal,
	change *agentdomain.SkillDraftChange,
) (agentdomain.ApplyResult, error) {
	var value skillapp.SkillWorkspaceView
	var err error
	if proposal.Operation == agentdomain.OperationCreate {
		value, err = a.skills.CreateSkillDraft(ctx, skillapp.CreateSkillDraftInput{
			Name: change.Name, Goal: change.Description, WhenToUse: change.Description,
			Instructions: change.Instructions,
		})
	} else {
		value, err = a.skills.UpdateDraftBundle(ctx, proposal.ResourceID, proposal.BaselineFingerprint,
			skillapp.UpdateDraftBundleInput{Name: change.Name, Description: change.Description, Instructions: change.Instructions})
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

func (a *ResourceChangeProposalAdapters) applyMCP(
	ctx context.Context,
	proposal agentdomain.ResourceChangeProposal,
	change *agentdomain.MCPConfigChange,
) (agentdomain.ApplyResult, error) {
	id := proposal.ResourceID
	config := mcpConfigFromChange(change)
	if proposal.Operation == agentdomain.OperationCreate {
		id = uuid.Must(uuid.NewV7()).String()
		config.ID = id
		config.Auth = &mcpdomain.AuthConfig{Type: mcpdomain.AuthTypeNone}
		config.Env = map[string]string{}
		config.Headers = map[string]string{}
		if err := a.mcp.ConnectServer(ctx, config); err != nil {
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
		if err := a.mcp.UpdateServer(ctx, config); err != nil {
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
	return safeApplyResult(id, mcpSafeProjection(readback))
}

func (a *ResourceChangeProposalAdapters) applyKnowledge(
	ctx context.Context,
	proposal agentdomain.ResourceChangeProposal,
	change *agentdomain.KnowledgeWorkspaceChange,
) (agentdomain.ApplyResult, error) {
	config := knowledgedomain.WorkspaceConfig{EmbeddingModel: change.EmbeddingModel}
	var value *knowledgedomain.Workspace
	var err error
	if proposal.Operation == agentdomain.OperationCreate {
		value, err = a.knowledge.CreateWorkspace(ctx, proposal.TenantID, knowledgeapp.CreateWorkspaceInput{
			Name: change.Name, Description: change.Description, Config: config,
		})
		if err != nil {
			return agentdomain.ApplyResult{}, &agentport.ResourceApplyError{
				Outcome: agentport.ResourceApplyUnknownOutcome,
				Err:     err,
			}
		}
	} else {
		existing, getErr := a.knowledge.GetWorkspace(ctx, proposal.TenantID, proposal.ResourceID)
		if getErr != nil {
			return agentdomain.ApplyResult{}, definiteApplyError(getErr)
		}
		if change.Name != "" && change.Name != existing.Name {
			return agentdomain.ApplyResult{}, definiteApplyError(agentdomain.ErrProposalInvalid)
		}
		description := change.Description
		value, err = a.knowledge.UpdateWorkspace(ctx, proposal.TenantID, existing.Name, knowledgeapp.UpdateWorkspaceInput{
			Description: &description, Config: &config,
		})
	}
	if err != nil {
		return agentdomain.ApplyResult{}, definiteApplyError(err)
	}
	return safeApplyResult(value.ID, knowledgeSafeProjection(value))
}

func agentSafeProjection(value agentapp.AgentDTO) map[string]any {
	return map[string]any{
		"id": value.ID, "name": value.Name, "type": value.Type, "description": value.Description,
		"model": value.LLMModel, "maxIterations": value.MaxIterations, "maxContextTokens": value.MaxContextTokens,
		"skillIds": value.AllowedSkills, "mcpToolIds": value.MCPToolIDs, "workspaceIds": value.KnowledgeWorkspaceIDs,
	}
}

func mcpSafeProjection(value *mcpdomain.ServerConfig) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": value.ID, "name": value.Name, "version": value.Version, "transport": value.Transport,
		"command": value.Command, "args": value.Args, "url": value.URL, "capabilities": value.Capabilities,
		"timeoutMs": value.Timeout.Milliseconds(), "retry": value.Retry,
	}
}

func knowledgeSafeProjection(value *knowledgedomain.Workspace) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return map[string]any{"id": value.ID, "name": value.Name, "description": value.Description, "config": value.Config}
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
