package wiring

import (
	"context"
	"encoding/json"
	"testing"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
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

func TestProposalMCPCreateHasNoCredentialsAndUpdatePreservesStoredCredentials(t *testing.T) {
	mcp := &proposalMCPFake{configs: map[string]*mcpdomain.ServerConfig{}}
	adapter := NewResourceChangeProposalAdapters(nil, nil, mcp, nil)
	created, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: agentdomain.ResourceChangeProposal{ResourceKind: agentdomain.ResourceMCPConfig, Operation: agentdomain.OperationCreate},
		Payload:  &agentdomain.MCPConfigChange{Name: "docs", Version: "1", Transport: "streamable_http", URL: "https://example.test/mcp", TimeoutSec: 30},
	})
	require.NoError(t, err)
	createdConfig := mcp.configs[created.ResourceID]
	require.Equal(t, mcpdomain.AuthTypeNone, createdConfig.Auth.Type)
	require.Empty(t, createdConfig.Env)
	require.Empty(t, createdConfig.Headers)

	mcp.configs["server-1"] = &mcpdomain.ServerConfig{
		ID: "server-1", Name: "old", Transport: "streamable_http", URL: "https://old.test/mcp",
		Headers: map[string]string{"Authorization": "Bearer keep-me"},
		Auth:    &mcpdomain.AuthConfig{Type: mcpdomain.AuthTypeBearer, Token: "keep-me"},
	}
	result, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: agentdomain.ResourceChangeProposal{ResourceKind: agentdomain.ResourceMCPConfig, ResourceID: "server-1", Operation: agentdomain.OperationUpdate},
		Payload:  &agentdomain.MCPConfigChange{Name: "new", Version: "2", Transport: "streamable_http", URL: "https://new.test/mcp", TimeoutSec: 30},
	})
	require.NoError(t, err)
	require.Equal(t, "keep-me", mcp.configs["server-1"].Auth.Token)
	require.Equal(t, "Bearer keep-me", mcp.configs["server-1"].Headers["Authorization"])
	require.NotContains(t, string(result.Readback), "keep-me")
}

func TestProposalKnowledgeUpdateCannotRenameWorkspace(t *testing.T) {
	knowledge := &proposalKnowledgeFake{value: &knowledgedomain.Workspace{ID: "ws-1", Name: "docs", Description: "old", Config: knowledgedomain.WorkspaceConfig{EmbeddingModel: knowledgedomain.DefaultEmbeddingModel}}}
	adapter := NewResourceChangeProposalAdapters(nil, nil, nil, knowledge)
	_, err := adapter.ApplyResourceChange(context.Background(), agentdomain.ProposalEnvelope{
		Proposal: agentdomain.ResourceChangeProposal{TenantID: "tenant-1", ResourceKind: agentdomain.ResourceKnowledgeWorkspace, ResourceID: "docs", Operation: agentdomain.OperationUpdate},
		Payload:  &agentdomain.KnowledgeWorkspaceChange{Name: "renamed", Description: "new", EmbeddingModel: knowledgedomain.DefaultEmbeddingModel},
	})
	require.Error(t, err)
	require.Equal(t, "docs", knowledge.value.Name)
}

type proposalAgentFake struct{ values map[string]agentapp.AgentDTO }

func (f *proposalAgentFake) Create(_ context.Context, in agentapp.CreateAgentInput) (agentapp.AgentDTO, error) {
	value := agentapp.AgentDTO{ID: "agent-created", Name: in.Name, Type: in.Type, Description: in.Description, LLMModel: in.LLMModel, MaxIterations: in.MaxIterations, MaxContextTokens: in.MaxContextTokens}
	f.values[value.ID] = value
	return value, nil
}
func (f *proposalAgentFake) Get(_ context.Context, id string) (agentapp.AgentDTO, error) {
	return f.values[id], nil
}
func (f *proposalAgentFake) Update(_ context.Context, id string, in agentapp.UpdateAgentInput) (agentapp.AgentDTO, error) {
	value := f.values[id]
	value.Name, value.Description, value.LLMModel = in.Name, in.Description, in.LLMModel
	f.values[id] = value
	return value, nil
}

type proposalMCPFake struct {
	configs map[string]*mcpdomain.ServerConfig
}

func (f *proposalMCPFake) ConnectServer(_ context.Context, cfg *mcpdomain.ServerConfig) error {
	f.configs[cfg.ID] = cloneMCPConfigForTest(cfg)
	return nil
}
func (f *proposalMCPFake) UpdateServer(_ context.Context, cfg *mcpdomain.ServerConfig) error {
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

type proposalKnowledgeFake struct{ value *knowledgedomain.Workspace }

func (f *proposalKnowledgeFake) CreateWorkspace(_ context.Context, _ string, in knowledgeapp.CreateWorkspaceInput) (*knowledgedomain.Workspace, error) {
	f.value = &knowledgedomain.Workspace{ID: "ws-created", Name: in.Name, Description: in.Description, Config: in.Config}
	return f.value, nil
}
func (f *proposalKnowledgeFake) UpdateWorkspace(_ context.Context, _, _ string, in knowledgeapp.UpdateWorkspaceInput) (*knowledgedomain.Workspace, error) {
	if in.Description != nil {
		f.value.Description = *in.Description
	}
	return f.value, nil
}
func (f *proposalKnowledgeFake) GetWorkspace(context.Context, string, string) (*knowledgedomain.Workspace, error) {
	return f.value, nil
}

var _ proposalSkillService = (*proposalSkillFake)(nil)

type proposalSkillFake struct{}

func (*proposalSkillFake) CreateSkillDraft(context.Context, skillapp.CreateSkillDraftInput) (skillapp.SkillWorkspaceView, error) {
	return skillapp.SkillWorkspaceView{}, nil
}
func (*proposalSkillFake) GetWorkspace(context.Context, string) (skillapp.SkillWorkspaceView, error) {
	return skillapp.SkillWorkspaceView{}, nil
}
func (*proposalSkillFake) UpdateDraftBundle(context.Context, string, string, skillapp.UpdateDraftBundleInput) (skillapp.SkillWorkspaceView, error) {
	return skillapp.SkillWorkspaceView{}, nil
}
