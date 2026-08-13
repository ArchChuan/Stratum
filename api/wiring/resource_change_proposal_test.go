package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/stretchr/testify/require"
)

func TestProposalAgentRejectsManagedAssistantAndReturnsSafeReadback(t *testing.T) {
	agents := &proposalAgentFake{values: map[string]agentapp.AgentDTO{
		agentdomain.SystemAssistantID: {ID: agentdomain.SystemAssistantID, SystemKey: agentdomain.SystemAssistantKey},
	}}
	adapter := NewResourceChangeProposalAdapters(agents, nil, nil, nil)
	_, err := adapter.ResolveBaseline(context.Background(), agentdomain.ResourceChangeProposal{
		ResourceKind: agentdomain.ResourceAgent, ResourceID: agentdomain.SystemAssistantID,
	})
	require.ErrorIs(t, err, agentdomain.ErrSystemAssistantManaged)

	result, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: agentdomain.ResourceChangeProposal{TenantID: "tenant-1", ResourceKind: agentdomain.ResourceAgent, Operation: agentdomain.OperationCreate},
		Payload:  &agentdomain.AgentChange{Name: "agent", Description: "desc", Model: "qwen-plus", MaxIterations: 5, MaxContextTokens: 4096},
	})
	require.NoError(t, err)
	require.NotContains(t, string(result.Readback), "systemPrompt")
	require.NotEmpty(t, result.Fingerprint)
}

func TestProposalAgentApplySystemPromptTransparency(t *testing.T) {
	agents := &proposalAgentFake{values: map[string]agentapp.AgentDTO{
		"agent-existing": {ID: "agent-existing", Name: "old", SystemPrompt: "keep-prompt", LLMModel: "m"},
	}}
	adapter := NewResourceChangeProposalAdapters(agents, nil, nil, nil)
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	base := &agentdomain.AgentChange{Name: "n", Description: "d", Model: "m", MaxIterations: 3, MaxContextTokens: 100}

	// create 透传 systemPrompt（修复 silent-drop）。
	created, err := adapter.applyAgentChange(ctx, "tenant-1", "", agentdomain.OperationCreate,
		&agentdomain.AgentChange{Name: "n", Description: "d", SystemPrompt: "create-prompt", Model: "m", MaxIterations: 3, MaxContextTokens: 100}, "actor-1")
	require.NoError(t, err)
	require.Equal(t, "create-prompt", agents.values[created.ResourceID].SystemPrompt)

	// update 非空覆盖既有 prompt。
	updated, err := adapter.applyAgentChange(ctx, "tenant-1", "agent-existing", agentdomain.OperationUpdate,
		&agentdomain.AgentChange{Name: "n2", Description: "d2", SystemPrompt: "new-prompt", Model: "m", MaxIterations: 3, MaxContextTokens: 100}, "actor-1")
	require.NoError(t, err)
	require.Equal(t, "new-prompt", agents.values[updated.ResourceID].SystemPrompt)

	// update 空保留既有 prompt（buildUpdateConfig 对空串会清除，不能因省略而误清）。
	kept, err := adapter.applyAgentChange(ctx, "tenant-1", "agent-existing", agentdomain.OperationUpdate, base, "actor-1")
	require.NoError(t, err)
	require.Equal(t, "new-prompt", agents.values[kept.ResourceID].SystemPrompt)
}

func TestProposalMCPBaselineContainsTypedFieldsWithoutCredentials(t *testing.T) {
	mcp := &proposalMCPFake{configs: map[string]*mcpdomain.ServerConfig{"server-1": {
		ID: "server-1", Name: "docs", Version: "1", Transport: "streamable-http",
		URL: "https://user:keep-me@example.test/mcp?api_token=keep-me&mode=safe", Timeout: 30 * time.Second,
		Headers: map[string]string{"Authorization": "Bearer keep-me"},
		Env:     map[string]string{"API_TOKEN": "keep-me"},
		Auth:    &mcpdomain.AuthConfig{Type: mcpdomain.AuthTypeBearer, Token: "keep-me"},
	}}}
	adapter := NewResourceChangeProposalAdapters(nil, nil, mcp, nil)
	baseline, err := adapter.ResolveBaseline(context.Background(), agentdomain.ResourceChangeProposal{
		ResourceKind: agentdomain.ResourceMCPConfig, ResourceID: "server-1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, baseline.Fingerprint)
	require.JSONEq(t, `{"name":"docs","version":"1","transport":"streamable-http","url":"https://example.test/mcp?mode=safe","timeoutSec":30}`, string(baseline.Projection))
	require.NotContains(t, string(baseline.Projection), "keep-me")
}

func TestProposalMCPCreateHasNoCredentialsAndUpdatePreservesStoredCredentials(t *testing.T) {
	mcp := &proposalMCPFake{configs: map[string]*mcpdomain.ServerConfig{}}
	adapter := NewResourceChangeProposalAdapters(nil, nil, mcp, nil)
	created, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: agentdomain.ResourceChangeProposal{ResourceKind: agentdomain.ResourceMCPConfig, Operation: agentdomain.OperationCreate},
		Payload:  &agentdomain.MCPConfigChange{Name: "docs", Version: "1", Transport: "streamable-http", URL: "https://example.test/mcp", TimeoutSec: 30},
	})
	require.NoError(t, err)
	createdConfig := mcp.configs[created.ResourceID]
	require.Equal(t, mcpdomain.AuthTypeNone, createdConfig.Auth.Type)
	require.Empty(t, createdConfig.Env)
	require.Empty(t, createdConfig.Headers)

	mcp.configs["server-1"] = &mcpdomain.ServerConfig{
		ID: "server-1", Name: "old", Transport: "streamable-http", URL: "https://old.test/mcp",
		Headers: map[string]string{"Authorization": "Bearer keep-me"},
		Auth:    &mcpdomain.AuthConfig{Type: mcpdomain.AuthTypeBearer, Token: "keep-me"},
	}
	result, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: agentdomain.ResourceChangeProposal{ResourceKind: agentdomain.ResourceMCPConfig, ResourceID: "server-1", Operation: agentdomain.OperationUpdate},
		Payload:  &agentdomain.MCPConfigChange{Name: "new", Version: "2", Transport: "streamable-http", URL: "https://new.test/mcp", TimeoutSec: 30},
	})
	require.NoError(t, err)
	require.Equal(t, "keep-me", mcp.configs["server-1"].Auth.Token)
	require.Equal(t, "Bearer keep-me", mcp.configs["server-1"].Headers["Authorization"])
	require.NotContains(t, string(result.Readback), "keep-me")
}

func TestProposalMCPUpdateFailureHasUnknownOutcome(t *testing.T) {
	mcp := &proposalMCPFake{
		configs: map[string]*mcpdomain.ServerConfig{"server-1": {
			ID: "server-1", Name: "old", Transport: "streamable-http", URL: "https://old.test/mcp",
		}},
		updateErr: errors.New("replacement connection failed"),
	}
	adapter := NewResourceChangeProposalAdapters(nil, nil, mcp, nil)
	_, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: agentdomain.ResourceChangeProposal{ResourceKind: agentdomain.ResourceMCPConfig, ResourceID: "server-1", Operation: agentdomain.OperationUpdate},
		Payload:  &agentdomain.MCPConfigChange{Name: "new", Transport: "streamable-http", URL: "https://new.test/mcp", TimeoutSec: 30},
	})
	var applyErr *agentport.ResourceApplyError
	require.ErrorAs(t, err, &applyErr)
	require.Equal(t, agentport.ResourceApplyUnknownOutcome, applyErr.Outcome)
}

func TestProposalKnowledgeUpdateCannotRenameWorkspace(t *testing.T) {
	knowledge := &proposalKnowledgeFake{value: &knowledgedomain.Workspace{ID: "ws-1", Name: "docs", Description: "old", Config: knowledgedomain.WorkspaceConfig{EmbeddingModel: knowledgedomain.DefaultEmbeddingModel}}}
	adapter := NewResourceChangeProposalAdapters(nil, nil, nil, knowledge)
	_, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: agentdomain.ResourceChangeProposal{TenantID: "tenant-1", ResourceKind: agentdomain.ResourceKnowledgeWorkspace, ResourceID: "ws-1", Operation: agentdomain.OperationUpdate},
		Payload:  &agentdomain.KnowledgeWorkspaceChange{Name: "renamed", Description: "new", EmbeddingModel: knowledgedomain.DefaultEmbeddingModel},
	})
	require.Error(t, err)
	require.Equal(t, "docs", knowledge.value.Name)
}

func TestProposalKnowledgeUpdateResolvesApplyResultResourceID(t *testing.T) {
	knowledge := &proposalKnowledgeFake{}
	adapter := NewResourceChangeProposalAdapters(nil, nil, nil, knowledge)
	created, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: agentdomain.ResourceChangeProposal{
			TenantID: "tenant-1", ResourceKind: agentdomain.ResourceKnowledgeWorkspace,
			Operation: agentdomain.OperationCreate,
		},
		Payload: &agentdomain.KnowledgeWorkspaceChange{
			Name: "docs", Description: "old", EmbeddingModel: knowledgedomain.DefaultEmbeddingModel,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "ws-created", created.ResourceID)

	proposal := agentdomain.ResourceChangeProposal{
		TenantID: "tenant-1", ResourceKind: agentdomain.ResourceKnowledgeWorkspace,
		ResourceID: created.ResourceID, Operation: agentdomain.OperationUpdate,
	}
	baseline, err := adapter.ResolveBaseline(context.Background(), proposal)
	require.NoError(t, err)
	require.NotEmpty(t, baseline.Fingerprint)

	updated, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: proposal,
		Payload: &agentdomain.KnowledgeWorkspaceChange{
			Name: "docs", Description: "new", EmbeddingModel: knowledgedomain.DefaultEmbeddingModel,
		},
	})
	require.NoError(t, err)
	require.Equal(t, created.ResourceID, updated.ResourceID)
	require.Equal(t, []string{created.ResourceID, created.ResourceID}, knowledge.lookupIDs)
	require.Equal(t, "docs", knowledge.lastUpdateName)
	require.Equal(t, "new", knowledge.value.Description)
}

type proposalAgentFake struct {
	values      map[string]agentapp.AgentDTO
	lastCtx     context.Context
	lastActorID string
}

func (f *proposalAgentFake) Create(ctx context.Context, in agentapp.CreateAgentInput) (agentapp.AgentDTO, error) {
	f.lastCtx = ctx
	f.lastActorID = in.ActorID
	value := agentapp.AgentDTO{ID: "agent-created", Name: in.Name, Type: in.Type, Description: in.Description, SystemPrompt: in.SystemPrompt, LLMModel: in.LLMModel, MaxIterations: in.MaxIterations, MaxContextTokens: in.MaxContextTokens}
	f.values[value.ID] = value
	return value, nil
}
func (f *proposalAgentFake) Get(_ context.Context, id string) (agentapp.AgentDTO, error) {
	return f.values[id], nil
}
func (f *proposalAgentFake) Update(_ context.Context, id string, in agentapp.UpdateAgentInput) (agentapp.AgentDTO, error) {
	value := f.values[id]
	value.Name, value.Description, value.SystemPrompt, value.LLMModel = in.Name, in.Description, in.SystemPrompt, in.LLMModel
	f.values[id] = value
	return value, nil
}

type proposalMCPFake struct {
	configs   map[string]*mcpdomain.ServerConfig
	updateErr error
}

func (f *proposalMCPFake) ConnectServer(_ context.Context, cfg *mcpdomain.ServerConfig, _ []string, _ string) error {
	f.configs[cfg.ID] = cloneMCPConfigForTest(cfg)
	return nil
}
func (f *proposalMCPFake) UpdateServer(_ context.Context, cfg *mcpdomain.ServerConfig, _ string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.configs[cfg.ID] = cloneMCPConfigForTest(cfg)
	return nil
}
func (f *proposalMCPFake) GetServerConfig(_ context.Context, id string) (*mcpdomain.ServerConfig, error) {
	return cloneMCPConfigForTest(f.configs[id]), nil
}
func cloneMCPConfigForTest(cfg *mcpdomain.ServerConfig) *mcpdomain.ServerConfig {
	encoded, _ := json.Marshal(cfg)
	var cloned mcpdomain.ServerConfig
	_ = json.Unmarshal(encoded, &cloned)
	return &cloned
}

type proposalKnowledgeFake struct {
	value          *knowledgedomain.Workspace
	lookupIDs      []string
	lastUpdateName string
}

func (f *proposalKnowledgeFake) CreateWorkspace(_ context.Context, _ string, in knowledgeapp.CreateWorkspaceInput, _ string) (*knowledgedomain.Workspace, error) {
	f.value = &knowledgedomain.Workspace{ID: "ws-created", Name: in.Name, Description: in.Description, Config: in.Config}
	return f.value, nil
}
func (f *proposalKnowledgeFake) UpdateWorkspace(_ context.Context, _, name string, in knowledgeapp.UpdateWorkspaceInput, _ string) (*knowledgedomain.Workspace, error) {
	f.lastUpdateName = name
	if in.Description != nil {
		f.value.Description = *in.Description
	}
	return f.value, nil
}
func (f *proposalKnowledgeFake) GetWorkspaceByID(_ context.Context, _, id string) (*knowledgedomain.Workspace, error) {
	f.lookupIDs = append(f.lookupIDs, id)
	if f.value == nil || f.value.ID != id {
		return nil, knowledgedomain.ErrWorkspaceNotFound
	}
	return f.value, nil
}

var _ proposalSkillService = (*proposalSkillFake)(nil)

type proposalSkillFake struct {
	fingerprints []string
}

func (*proposalSkillFake) CreateSkillDraft(context.Context, skillapp.CreateSkillDraftInput) (skillapp.SkillWorkspaceView, error) {
	return skillapp.SkillWorkspaceView{
		Skill: skillapp.SkillProduct{ID: "skill-created", Name: "s"},
		Draft: skilldomain.SkillRevision{Status: skilldomain.VersionStatusDraft, ContentHash: "hash"},
	}, nil
}
func (*proposalSkillFake) GetWorkspace(context.Context, string) (skillapp.SkillWorkspaceView, error) {
	return skillapp.SkillWorkspaceView{}, nil
}
func (f *proposalSkillFake) UpdateDraftBundle(_ context.Context, _, fingerprint string, in skillapp.UpdateDraftBundleInput) (skillapp.SkillWorkspaceView, error) {
	f.fingerprints = append(f.fingerprints, fingerprint)
	return skillapp.SkillWorkspaceView{
		Skill: skillapp.SkillProduct{ID: "skill-1", Name: in.Name},
		Draft: skilldomain.SkillRevision{Status: skilldomain.VersionStatusDraft, ContentHash: "hash"},
	}, nil
}

func TestApplyDirectAgentCreateAndUpdate(t *testing.T) {
	agents := &proposalAgentFake{values: map[string]agentapp.AgentDTO{}}
	adapter := NewResourceChangeProposalAdapters(agents, nil, nil, nil)
	ctx := context.Background()

	created, err := adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceAgent,
		agentdomain.OperationCreate, "", []byte(`{"name":"a","description":"d","model":"m","maxIterations":3,"maxContextTokens":100}`))
	require.NoError(t, err)
	require.Equal(t, "agent-created", created.ResourceID)
	require.NotEmpty(t, created.Fingerprint)

	updated, err := adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceAgent,
		agentdomain.OperationUpdate, "agent-created", []byte(`{"name":"a2","description":"d2","model":"m2","maxIterations":4,"maxContextTokens":200}`))
	require.NoError(t, err)
	require.Equal(t, "agent-created", updated.ResourceID)
	require.Equal(t, "a2", agents.values["agent-created"].Name)
	require.Equal(t, "m2", agents.values["agent-created"].LLMModel)
}

func TestApplyDirectAgentRejectsSystemAssistantManaged(t *testing.T) {
	agents := &proposalAgentFake{values: map[string]agentapp.AgentDTO{
		agentdomain.SystemAssistantID: {ID: agentdomain.SystemAssistantID, SystemKey: agentdomain.SystemAssistantKey},
	}}
	adapter := NewResourceChangeProposalAdapters(agents, nil, nil, nil)
	_, err := adapter.ApplyDirect(context.Background(), "tenant-1", "user-1", agentdomain.ResourceAgent,
		agentdomain.OperationUpdate, agentdomain.SystemAssistantID,
		[]byte(`{"name":"a","description":"d","model":"m","maxIterations":3,"maxContextTokens":100}`))
	require.ErrorIs(t, err, agentdomain.ErrSystemAssistantManaged)
}

func TestApplyDirectMCPCreateHasNoCredentialsAndUpdatePreservesStored(t *testing.T) {
	mcp := &proposalMCPFake{configs: map[string]*mcpdomain.ServerConfig{}}
	adapter := NewResourceChangeProposalAdapters(nil, nil, mcp, nil)
	ctx := context.Background()

	created, err := adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceMCPConfig,
		agentdomain.OperationCreate, "", []byte(`{"name":"docs","version":"1","transport":"streamable-http","url":"https://example.test/mcp","timeoutSec":30}`))
	require.NoError(t, err)
	createdConfig := mcp.configs[created.ResourceID]
	require.Equal(t, mcpdomain.AuthTypeNone, createdConfig.Auth.Type)
	require.Empty(t, createdConfig.Env)
	require.Empty(t, createdConfig.Headers)

	mcp.configs["server-1"] = &mcpdomain.ServerConfig{
		ID: "server-1", Name: "old", Transport: "streamable-http", URL: "https://old.test/mcp",
		Headers: map[string]string{"Authorization": "Bearer keep-me"},
		Auth:    &mcpdomain.AuthConfig{Type: mcpdomain.AuthTypeBearer, Token: "keep-me"},
	}
	updated, err := adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceMCPConfig,
		agentdomain.OperationUpdate, "server-1", []byte(`{"name":"new","version":"2","transport":"streamable-http","url":"https://new.test/mcp","timeoutSec":30}`))
	require.NoError(t, err)
	require.Equal(t, "server-1", updated.ResourceID)
	require.Equal(t, "keep-me", mcp.configs["server-1"].Auth.Token)
	require.Equal(t, "Bearer keep-me", mcp.configs["server-1"].Headers["Authorization"])
	require.NotContains(t, string(updated.Readback), "keep-me")
}

func TestApplyDirectKnowledgeCreateAndUpdate(t *testing.T) {
	knowledge := &proposalKnowledgeFake{}
	adapter := NewResourceChangeProposalAdapters(nil, nil, nil, knowledge)
	ctx := context.Background()

	created, err := adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceKnowledgeWorkspace,
		agentdomain.OperationCreate, "", []byte(`{"name":"docs","description":"old","embeddingModel":"bge-m3"}`))
	require.NoError(t, err)
	require.Equal(t, "ws-created", created.ResourceID)

	updated, err := adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceKnowledgeWorkspace,
		agentdomain.OperationUpdate, "ws-created", []byte(`{"name":"docs","description":"new","embeddingModel":"bge-m3"}`))
	require.NoError(t, err)
	require.Equal(t, "ws-created", updated.ResourceID)
	require.Equal(t, "new", knowledge.value.Description)
}

func TestApplyDirectSkillUpdatePassesEmptyFingerprint(t *testing.T) {
	skills := &proposalSkillFake{}
	adapter := NewResourceChangeProposalAdapters(nil, skills, nil, nil)
	ctx := context.Background()

	created, err := adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceSkillDraft,
		agentdomain.OperationCreate, "", []byte(`{"name":"s","description":"d","instructions":"i"}`))
	require.NoError(t, err)
	require.Equal(t, "skill-created", created.ResourceID)

	updated, err := adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceSkillDraft,
		agentdomain.OperationUpdate, "skill-1", []byte(`{"name":"s2","description":"d2","instructions":"i2"}`))
	require.NoError(t, err)
	require.Equal(t, "skill-1", updated.ResourceID)
	// Direct writes carry no baseline: the fingerprint must stay empty so the
	// update behaves like a plain API update, not a concurrency-guarded apply.
	require.Equal(t, []string{""}, skills.fingerprints)
}

func TestApplyDirectRejectsMismatchedResourceIDAndUnknownFields(t *testing.T) {
	adapter := NewResourceChangeProposalAdapters(nil, nil, nil, nil)
	ctx := context.Background()
	payload := []byte(`{"name":"a","description":"d","model":"m","maxIterations":3,"maxContextTokens":100}`)

	_, err := adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceAgent,
		agentdomain.OperationCreate, "agent-1", payload)
	require.ErrorIs(t, err, agentdomain.ErrProposalInvalid)
	_, err = adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceAgent,
		agentdomain.OperationUpdate, "", payload)
	require.ErrorIs(t, err, agentdomain.ErrProposalInvalid)
	// Unknown fields are rejected by the strict decoder.
	_, err = adapter.ApplyDirect(ctx, "tenant-1", "user-1", agentdomain.ResourceAgent,
		agentdomain.OperationCreate, "", []byte(`{"name":"a","description":"d","model":"m","maxIterations":3,"maxContextTokens":100,"editors":["u"]}`))
	require.ErrorIs(t, err, agentdomain.ErrProposalInvalid)
}

func TestApplyDirectStampsSystemAssistantAuditSource(t *testing.T) {
	agents := &proposalAgentFake{values: map[string]agentapp.AgentDTO{}}
	adapter := NewResourceChangeProposalAdapters(agents, nil, nil, nil)
	_, err := adapter.ApplyDirect(context.Background(), "tenant-1", "user-1", agentdomain.ResourceAgent,
		agentdomain.OperationCreate, "", []byte(`{"name":"a","description":"d","model":"m","maxIterations":3,"maxContextTokens":100}`))
	require.NoError(t, err)
	require.NotNil(t, agents.lastCtx)
	source, proposalID := reqctx.ChangeSourceFromContext(agents.lastCtx)
	require.Equal(t, auditdomain.ChangeSourceSystemAssistantDirect, source)
	require.Empty(t, proposalID)
	// Ownership semantics: the direct write acts as the conversation
	// initiator, so the service layer applies the B ownership matrix — the
	// direct write is not an isolation bypass.
	require.Equal(t, "user-1", agents.lastActorID)
}

func TestApplyDirectFromToolRequiresTenantInContext(t *testing.T) {
	adapter := NewResourceChangeProposalAdapters(nil, nil, nil, nil)
	// No tenant stamped on ctx: the tool adapter fails closed.
	_, err := adapter.ApplyDirectFromTool(context.Background(), "user-1", map[string]any{})
	require.ErrorIs(t, err, agentdomain.ErrProposalInvalid)
	// Malformed tool arguments also fail closed before reaching ApplyDirect.
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	_, err = adapter.ApplyDirectFromTool(ctx, "user-1", map[string]any{"resourceKind": "agent", "operation": "delete"})
	require.ErrorContains(t, err, "invalid system assistant tool arguments")
}

// TestApplyDirectFromToolAcceptsStringifiedPayload covers the production tool
// boundary variance where a model serializes the nested payload as a JSON
// string (observed with glm-5). The adapter must decode it and reach the
// agent service instead of failing argument parsing.
func TestApplyDirectFromToolAcceptsStringifiedPayload(t *testing.T) {
	agents := &proposalAgentFake{values: map[string]agentapp.AgentDTO{}}
	adapter := NewResourceChangeProposalAdapters(agents, nil, nil, nil)
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	rawPayload := `{"name":"a","description":"d","model":"m","maxIterations":3,"maxContextTokens":100}`
	args := map[string]any{
		"resourceKind": "agent",
		"operation":    "create",
		"payload":      rawPayload,
	}
	result, err := adapter.ApplyDirectFromTool(ctx, "user-1", args)
	require.NoError(t, err)
	require.NotEmpty(t, result.ResourceID)
	require.Equal(t, "user-1", agents.lastActorID)
}

// proposalRoleStub 固定角色的 TenantRoleResolver stub。
type proposalRoleStub struct {
	role string
	err  error
}

func (s proposalRoleStub) ResolveTenantRole(context.Context, string, string) (string, error) {
	return s.role, s.err
}

// TestProposalAuthorizerMemberCreateAllowedDecideForbidden 用真实 wiring
// authorizer 验证 D6 分流：member 可创建提案（ready_for_review 流），但
// 编辑/确认/应用（decide）被拒绝；admin/owner 全部放行。防 regression：
// 测试 fake 的宽松行为不得掩盖 wiring 授权。
func TestProposalAuthorizerMemberCreateAllowedDecideForbidden(t *testing.T) {
	auth := proposalAuthorizer{roles: proposalRoleStub{role: "member"}}
	require.NoError(t, auth.AuthorizeProposal(context.Background(), "tenant-1", "member-1",
		agentdomain.ResourceAgent, agentdomain.OperationCreate, agentdomain.ProposalActionCreate))
	require.ErrorIs(t, auth.AuthorizeProposal(context.Background(), "tenant-1", "member-1",
		agentdomain.ResourceAgent, agentdomain.OperationCreate, agentdomain.ProposalActionDecide), agentdomain.ErrProposalForbidden)
}

func TestProposalAuthorizerAdminAndOwnerDecideAllowed(t *testing.T) {
	for _, role := range []string{"admin", "owner"} {
		auth := proposalAuthorizer{roles: proposalRoleStub{role: role}}
		require.NoError(t, auth.AuthorizeProposal(context.Background(), "tenant-1", "admin-1",
			agentdomain.ResourceAgent, agentdomain.OperationUpdate, agentdomain.ProposalActionCreate))
		require.NoError(t, auth.AuthorizeProposal(context.Background(), "tenant-1", "admin-1",
			agentdomain.ResourceAgent, agentdomain.OperationUpdate, agentdomain.ProposalActionDecide))
	}
}

func TestProposalAuthorizerFailClosedOnResolverError(t *testing.T) {
	auth := proposalAuthorizer{roles: proposalRoleStub{err: errors.New("resolver down")}}
	require.ErrorIs(t, auth.AuthorizeProposal(context.Background(), "tenant-1", "user-1",
		agentdomain.ResourceAgent, agentdomain.OperationCreate, agentdomain.ProposalActionCreate), agentdomain.ErrProposalForbidden)
	require.ErrorIs(t, auth.AuthorizeProposal(context.Background(), "tenant-1", "user-1",
		agentdomain.ResourceAgent, agentdomain.OperationCreate, agentdomain.ProposalActionDecide), agentdomain.ErrProposalForbidden)
}
