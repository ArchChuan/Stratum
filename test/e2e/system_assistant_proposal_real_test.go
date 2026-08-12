package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/wiring"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/officialdocs"
	agentpersist "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgepersist "github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/persistence"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpinfra "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"github.com/byteBuilderX/stratum/internal/mcp/infrastructure/testserver"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skillpersist "github.com/byteBuilderX/stratum/internal/skill/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type classifiedProposalApplier struct {
	calls int
	err   error
}

// realTenantRoleResolver resolves the actor's actual tenant role from the real
// public.tenant_members table — the same chain production wiring uses
// (tenantRoleAdapter → TenantService).
type realTenantRoleResolver struct {
	members *iamapp.TenantService
}

func (r realTenantRoleResolver) ResolveTenantRole(ctx context.Context, tenantID, userID string) (string, error) {
	if r.members == nil {
		return "", errors.New("members service not wired")
	}
	return r.members.GetMemberRole(ctx, tenantID, userID)
}

// realMemberProposalAuthorizer resolves the actor's actual tenant role from the
// real public.tenant_members table through iam.TenantService — the same chain
// production wiring uses (proposalAuthorizer → tenantRoleAdapter → TenantService).
type realMemberProposalAuthorizer struct {
	members *iamapp.TenantService
}

func (a realMemberProposalAuthorizer) AuthorizeProposal(
	ctx context.Context,
	tenantID, actorID string,
	_ domain.ResourceKind,
	_ domain.ProposalOperation,
) error {
	if a.members == nil {
		return domain.ErrProposalForbidden
	}
	role, err := a.members.GetMemberRole(ctx, tenantID, actorID)
	if err != nil {
		return domain.ErrProposalForbidden
	}
	if role != "admin" && role != "owner" {
		return domain.ErrProposalForbidden
	}
	return nil
}

type roleAwareAssistantDiagnostics struct{ role string }

func (d roleAwareAssistantDiagnostics) Authorize(
	_ context.Context,
	req domain.DiagnosticRequest,
) (domain.DiagnosticAuthorization, error) {
	req.Scope = domain.DiagnosticScopeTenant
	return domain.DiagnosticAuthorization{Request: req, RoleClass: d.role}, nil
}

func (roleAwareAssistantDiagnostics) CollectAuthorized(
	context.Context,
	domain.DiagnosticRequest,
) (domain.DiagnosticEvidence, error) {
	return domain.DiagnosticEvidence{}, nil
}

type proposalToolGateway struct{ call int }

func (g *proposalToolGateway) Route(
	_ context.Context,
	request agentport.CapabilityRequest,
) (agentport.CapabilityResponse, error) {
	g.call++
	switch g.call {
	case 1:
		return agentport.CapabilityResponse{ToolCalls: []agentport.ToolCall{{
			ID: "proposal-1", Name: domain.SystemAssistantToolProposeResourceChange,
			Arguments: map[string]any{
				"resourceKind": "agent", "operation": "create",
				"payload": map[string]any{
					"name": "真实权限提案", "description": "real permission e2e",
					"model": "e2e-model", "maxIterations": 4, "maxContextTokens": 4096,
				},
			},
		}}}, nil
	case 2:
		return agentport.CapabilityResponse{Content: "已创建受治理提案。", Usage: agentport.TokenUsage{Total: 12}}, nil
	default:
		return agentport.CapabilityResponse{}, errors.New("unexpected deterministic model call")
	}
}

func insertRealMember(t *testing.T, pool *pgxpool.Pool, tenantID, userID, role string) {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(userID, "-", "")[:12]
	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenants (id, name, slug) VALUES ($1, $2, $3)
		 ON CONFLICT (id) DO NOTHING`,
		tenantID, "e2e-"+suffix, "e2e-"+suffix)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO public.users (id, github_id, github_login) VALUES ($1, $2, $2)
		 ON CONFLICT (id) DO NOTHING`,
		userID, "e2e-github-"+suffix)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO public.tenant_members (tenant_id, user_id, role) VALUES ($1, $2, $3)`,
		tenantID, userID, role)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenant_members WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.users WHERE id=$1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenants WHERE id=$1`, tenantID)
	})
}

func (a *classifiedProposalApplier) ApplyResourceChange(
	context.Context,
	domain.ProposalEnvelope,
) (domain.ApplyResult, error) {
	a.calls++
	return domain.ApplyResult{}, a.err
}

func TestSystemAssistantProposalRealServices(t *testing.T) {
	pool, tenants := setupSystemAssistantPostgres(t)
	tenantID := tenants[0]
	actorID := uuid.NewString()
	insertRealMember(t, pool, tenantID, actorID, "admin")
	ctx := assistantTenantContext(tenantID, actorID, tenantdb.RoleTenantAdmin)
	otherTenantCtx := assistantTenantContext(tenants[1], actorID, tenantdb.RoleTenantAdmin)

	agentRepo := agentpersist.NewPgAgentRepo(pool)
	members := iamapp.NewTenantService(iampersistence.NewTenantRepo(pool), zap.NewNop())
	roles := realTenantRoleResolver{members: members}
	agentSvc := agentapp.NewAgentService(agentapp.AgentServiceDeps{
		Registry: agentapp.NewRegistry(
			agentRepo,
			agentapp.BuiltinSystemAssistantProfileSource(),
			zap.NewNop(),
		),
		TenantRoleResolver: roles,
		Logger:             zap.NewNop(),
	})
	skillSvc := skillapp.NewVersionService(skillpersist.NewPgSkillRevisionRepo(pool), zap.NewNop())
	skillSvc.SetTenantRoleResolver(roles)
	workspaceSvc := knowledgeapp.NewWorkspaceService(knowledgepersist.NewWorkspaceRepo(pool), nil, zap.NewNop())
	workspaceSvc.SetTenantRoleResolver(roles)
	mcpServer := testserver.New(t)
	mcpServer.SetTools([]testserver.Tool{{
		Name: "read_verified_docs", InputSchema: map[string]any{"type": "object"},
	}})
	manager := mcpinfra.NewClientManager(zap.NewNop(), nil, pool)
	t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
	registry := mcpinfra.NewMCPToolRegistry(manager, zap.NewNop())
	mcpSvc := mcpapp.NewMCPService(
		mcpinfra.ToolRegistryAsPort(registry),
		mcpinfra.ServerManagerAsPort(manager),
		zap.NewNop(),
	)
	mcpSvc.SetTenantRoleResolver(roles)
	adapters := wiring.NewResourceChangeProposalAdapters(agentSvc, skillSvc, mcpSvc, workspaceSvc)
	repo := agentpersist.NewPgResourceChangeProposalRepo(pool)
	service := agentapp.NewResourceChangeProposalService(
		repo,
		proposalE2EAuthorizer{roles: map[string]string{actorID: "admin"}},
		adapters,
		map[domain.ResourceKind]agentport.ResourceChangeApplier{
			domain.ResourceAgent:              adapters,
			domain.ResourceSkillDraft:         adapters,
			domain.ResourceMCPConfig:          adapters,
			domain.ResourceKnowledgeWorkspace: adapters,
		},
		nil,
	)

	cases := []struct {
		name           string
		kind           domain.ResourceKind
		payload        json.RawMessage
		updatePayload  json.RawMessage
		assertCreated  func(string)
		assertUpdated  func(string)
		assertIsolated func(string)
	}{
		{
			name: "agent", kind: domain.ResourceAgent,
			payload:       json.RawMessage(`{"name":"E2E Agent","description":"real service","model":"e2e-model","maxIterations":4,"maxContextTokens":4096}`),
			updatePayload: json.RawMessage(`{"name":"E2E Agent","description":"updated real service","model":"e2e-model-v2","maxIterations":5,"maxContextTokens":8192}`),
			assertCreated: func(id string) {
				got, err := agentSvc.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, "E2E Agent", got.Name)
			},
			assertUpdated: func(id string) {
				got, err := agentSvc.Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, "updated real service", got.Description)
				require.Equal(t, 5, got.MaxIterations)
			},
			assertIsolated: func(id string) {
				_, err := agentSvc.Get(otherTenantCtx, id)
				require.Error(t, err)
			},
		},
		{
			name: "skill draft", kind: domain.ResourceSkillDraft,
			payload:       json.RawMessage(`{"name":"E2E Skill","description":"verified docs","instructions":"Use verified sources."}`),
			updatePayload: json.RawMessage(`{"name":"E2E Skill","description":"updated verified docs","instructions":"Use only verified sources."}`),
			assertCreated: func(id string) {
				got, err := skillSvc.GetWorkspace(ctx, id)
				require.NoError(t, err)
				require.Equal(t, "E2E Skill", got.Skill.Name)
				require.Equal(t, "draft", string(got.Draft.Status))
			},
			assertUpdated: func(id string) {
				got, err := skillSvc.GetWorkspace(ctx, id)
				require.NoError(t, err)
				require.Equal(t, "updated verified docs", got.Skill.Description)
				require.Equal(t, "Use only verified sources.", got.Draft.Instructions)
				require.Equal(t, "draft", string(got.Draft.Status))
			},
			assertIsolated: func(id string) {
				_, err := skillSvc.GetWorkspace(otherTenantCtx, id)
				require.Error(t, err)
			},
		},
		{
			name: "mcp config", kind: domain.ResourceMCPConfig,
			payload:       json.RawMessage(`{"name":"E2E MCP","version":"1.0.0","transport":"streamable-http","url":"` + mcpServer.URL() + `","timeoutSec":5}`),
			updatePayload: json.RawMessage(`{"name":"E2E MCP","version":"2.0.0","transport":"streamable-http","url":"` + mcpServer.URL() + `","timeoutSec":8}`),
			assertCreated: func(id string) {
				got, err := mcpSvc.GetServerConfig(ctx, id)
				require.NoError(t, err)
				require.Equal(t, "E2E MCP", got.Name)
				require.Empty(t, got.Headers)
			},
			assertUpdated: func(id string) {
				got, err := mcpSvc.GetServerConfig(ctx, id)
				require.NoError(t, err)
				require.Equal(t, "2.0.0", got.Version)
				require.Empty(t, got.Headers)
			},
			assertIsolated: func(id string) {
				_, err := mcpSvc.GetServerConfig(otherTenantCtx, id)
				require.Error(t, err)
			},
		},
		{
			name: "knowledge workspace", kind: domain.ResourceKnowledgeWorkspace,
			payload:       json.RawMessage(`{"name":"E2E Knowledge","description":"verified knowledge","embeddingModel":"text-embedding-v3"}`),
			updatePayload: json.RawMessage(`{"name":"E2E Knowledge","description":"updated verified knowledge","embeddingModel":"text-embedding-v3"}`),
			assertCreated: func(id string) {
				workspaces, err := workspaceSvc.ListWorkspaces(ctx, tenantID)
				require.NoError(t, err)
				var gotName string
				for _, workspace := range workspaces {
					if workspace.ID == id {
						gotName = workspace.Name
					}
				}
				require.Equal(t, "E2E Knowledge", gotName)
			},
			assertUpdated: func(_ string) {
				got, err := workspaceSvc.GetWorkspace(ctx, tenantID, "E2E Knowledge")
				require.NoError(t, err)
				require.Equal(t, "updated verified knowledge", got.Description)
			},
			assertIsolated: func(id string) {
				_, err := workspaceSvc.GetWorkspaceByID(otherTenantCtx, tenants[1], id)
				require.Error(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proposal, err := service.CreateProposal(ctx, agentapp.CreateProposalInput{
				TenantID: tenantID, ActorID: actorID, Kind: tc.kind,
				Operation: domain.OperationCreate, Payload: tc.payload,
			})
			require.NoError(t, err)
			applied, err := service.ConfirmAndApply(ctx, tenantID, proposal.ID, actorID)
			require.NoError(t, err)
			require.Equal(t, domain.StatusApplied, applied.Status)
			require.NotEmpty(t, applied.ApplyResult.ResourceID)
			resourceID := applied.ApplyResult.ResourceID
			tc.assertCreated(resourceID)
			tc.assertIsolated(resourceID)
			_, err = service.Get(otherTenantCtx, tenants[1], actorID, proposal.ID)
			require.Error(t, err)

			updated, err := service.CreateProposal(ctx, agentapp.CreateProposalInput{
				TenantID: tenantID, ActorID: actorID, Kind: tc.kind, ResourceID: resourceID,
				Operation: domain.OperationUpdate, Payload: tc.updatePayload,
			})
			require.NoError(t, err)
			require.NotEmpty(t, updated.BaselineFingerprint)
			appliedUpdate, err := service.ConfirmAndApply(ctx, tenantID, updated.ID, actorID)
			require.NoError(t, err)
			require.Equal(t, domain.StatusApplied, appliedUpdate.Status)
			require.Equal(t, resourceID, appliedUpdate.ApplyResult.ResourceID)
			tc.assertUpdated(resourceID)
			tc.assertIsolated(resourceID)
		})
	}

	t.Run("expired confirmation persists event", func(t *testing.T) {
		proposal, err := service.CreateProposal(ctx, agentapp.CreateProposalInput{
			TenantID: tenantID, ActorID: actorID, Kind: domain.ResourceAgent,
			Operation: domain.OperationCreate,
			Payload:   json.RawMessage(`{"name":"E2E Expired","description":"expired","model":"e2e-model","maxIterations":2,"maxContextTokens":1024}`),
		})
		require.NoError(t, err)
		require.NoError(t, tenantdb.ExecTenant(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, `UPDATE resource_change_proposals SET expires_at=$2 WHERE id=$1`,
				proposal.ID, time.Now().UTC().Add(-time.Minute))
			return execErr
		}))

		_, err = service.ConfirmAndApply(ctx, tenantID, proposal.ID, actorID)
		require.ErrorIs(t, err, domain.ErrProposalExpired)
		stored, err := repo.Get(ctx, proposal.ID)
		require.NoError(t, err)
		require.Equal(t, domain.StatusExpired, stored.Status)
		events, err := repo.ListEvents(ctx, proposal.ID)
		require.NoError(t, err)
		require.Equal(t, domain.StatusExpired, events[len(events)-1].ToStatus)
	})

	t.Run("confirmed proposal resumes after service rebuild", func(t *testing.T) {
		proposal, err := service.CreateProposal(ctx, agentapp.CreateProposalInput{
			TenantID: tenantID, ActorID: actorID, Kind: domain.ResourceAgent,
			Operation: domain.OperationCreate,
			Payload:   json.RawMessage(`{"name":"E2E Resume","description":"restart","model":"e2e-model","maxIterations":2,"maxContextTokens":1024}`),
		})
		require.NoError(t, err)
		require.NoError(t, repo.Confirm(ctx, proposal.ID, actorID, time.Now().UTC()))
		rebuilt := agentapp.NewResourceChangeProposalService(
			repo,
			proposalE2EAuthorizer{roles: map[string]string{actorID: "admin"}},
			adapters,
			map[domain.ResourceKind]agentport.ResourceChangeApplier{domain.ResourceAgent: adapters},
			nil,
		)
		applied, err := rebuilt.ConfirmAndApply(ctx, tenantID, proposal.ID, actorID)
		require.NoError(t, err)
		require.Equal(t, domain.StatusApplied, applied.Status)
		got, err := agentSvc.Get(ctx, applied.ApplyResult.ResourceID)
		require.NoError(t, err)
		require.Equal(t, "E2E Resume", got.Name)
	})

	t.Run("stale applying becomes unknown without replay", func(t *testing.T) {
		proposal, err := service.CreateProposal(ctx, agentapp.CreateProposalInput{
			TenantID: tenantID, ActorID: actorID, Kind: domain.ResourceAgent,
			Operation: domain.OperationCreate,
			Payload:   json.RawMessage(`{"name":"E2E Interrupted","description":"restart","model":"e2e-model","maxIterations":2,"maxContextTokens":1024}`),
		})
		require.NoError(t, err)
		staleAt := time.Now().UTC().Add(-10 * time.Minute)
		require.NoError(t, repo.Confirm(ctx, proposal.ID, actorID, staleAt))
		_, err = repo.ClaimApplying(ctx, proposal.ID, actorID, staleAt)
		require.NoError(t, err)
		before, err := agentSvc.List(ctx)
		require.NoError(t, err)

		_, err = service.ConfirmAndApply(ctx, tenantID, proposal.ID, actorID)
		require.ErrorIs(t, err, domain.ErrProposalUnknownOutcome)
		after, err := agentSvc.List(ctx)
		require.NoError(t, err)
		require.Len(t, after, len(before))
		stored, err := repo.Get(ctx, proposal.ID)
		require.NoError(t, err)
		require.Equal(t, domain.StatusUnknownOutcome, stored.Status)
	})

	for _, outcome := range []struct {
		name       string
		applyErr   error
		wantErr    error
		wantStatus domain.ProposalStatus
	}{
		{
			name: "definite failure", wantErr: domain.ErrProposalApplyFailed, wantStatus: domain.StatusFailed,
			applyErr: &agentport.ResourceApplyError{Outcome: agentport.ResourceApplyDefiniteFailure, Err: errors.New("known")},
		},
		{
			name: "unknown outcome", wantErr: domain.ErrProposalUnknownOutcome, wantStatus: domain.StatusUnknownOutcome,
			applyErr: &agentport.ResourceApplyError{Outcome: agentport.ResourceApplyUnknownOutcome, Err: errors.New("uncertain")},
		},
	} {
		t.Run(outcome.name+" persists terminal status", func(t *testing.T) {
			applier := &classifiedProposalApplier{err: outcome.applyErr}
			faultService := agentapp.NewResourceChangeProposalService(
				repo,
				proposalE2EAuthorizer{roles: map[string]string{actorID: "admin"}},
				nil,
				map[domain.ResourceKind]agentport.ResourceChangeApplier{domain.ResourceAgent: applier},
				nil,
			)
			proposal, err := faultService.CreateProposal(ctx, agentapp.CreateProposalInput{
				TenantID: tenantID, ActorID: actorID, Kind: domain.ResourceAgent,
				Operation: domain.OperationCreate,
				Payload:   json.RawMessage(`{"name":"E2E Failure","description":"failure","model":"e2e-model","maxIterations":2,"maxContextTokens":1024}`),
			})
			require.NoError(t, err)
			_, err = faultService.ConfirmAndApply(ctx, tenantID, proposal.ID, actorID)
			require.ErrorIs(t, err, outcome.wantErr)
			require.Equal(t, 1, applier.calls)
			stored, err := repo.Get(ctx, proposal.ID)
			require.NoError(t, err)
			require.Equal(t, outcome.wantStatus, stored.Status)
		})
	}
}

// TestSystemAssistantProposalInProcessToolUsesRealMemberPermissions proves the
// in-process proposal tool resolves the actor's ACTUAL tenant role from the
// database at call time: an admin creates a real proposal through the agent
// tool path, while a member is denied by the same real authorization chain.
func TestSystemAssistantProposalInProcessToolUsesRealMemberPermissions(t *testing.T) {
	pool, tenants := setupSystemAssistantPostgres(t)
	tenantID := tenants[0]
	adminID, memberID := uuid.NewString(), uuid.NewString()
	insertRealMember(t, pool, tenantID, adminID, "admin")
	insertRealMember(t, pool, tenantID, memberID, "member")

	members := iamapp.NewTenantService(iampersistence.NewTenantRepo(pool), zap.NewNop())
	repo := agentpersist.NewPgResourceChangeProposalRepo(pool)
	proposalService := agentapp.NewResourceChangeProposalService(
		repo,
		realMemberProposalAuthorizer{members: members},
		nil,
		map[domain.ResourceKind]agentport.ResourceChangeApplier{
			domain.ResourceAgent: &proposalE2EApplier{},
		},
		nil,
	)

	agentRepo := agentpersist.NewPgAgentRepo(pool)
	ctx := assistantTenantContext(tenantID, adminID, tenantdb.RoleTenantAdmin)
	existing, found, err := agentRepo.GetSystemAssistant(ctx)
	require.NoError(t, err)
	require.True(t, found)
	existing.LLMModel = "deterministic-e2e-model"
	_, err = agentRepo.UpdateSystemAssistantModel(
		ctx, existing.LLMModel, existing.MemoryScope, existing.CheckpointEnabled,
		existing.MaxIterations, existing.MaxContextTokens, nil,
	)
	require.NoError(t, err)

	chat := agentpersist.NewPgChatStore(pool, zap.NewNop())
	gateway := &proposalToolGateway{}
	service := agentapp.NewAgentService(agentapp.AgentServiceDeps{
		Registry:             agentapp.NewRegistry(agentRepo, agentapp.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantResolver:       deterministicTenantResolver{gateway: gateway},
		TenantModelValidator: deterministicModelValidator{},
		TenantModelCatalog:   deterministicModelValidator{},
		ChatStore:            chat,
		OfficialDocsSearch:   officialdocs.Search,
		DiagnosticProvider:   roleAwareAssistantDiagnostics{role: "admin"},
		ProposalService:      proposalService,
		Logger:               zap.NewNop(),
	})

	conversation, err := chat.CreateConversation(ctx, tenantID, domain.SystemAssistantID, adminID, "真实权限提案")
	require.NoError(t, err)
	result, _, err := service.Execute(ctx, domain.SystemAssistantID, agentapp.ExecRequest{
		Query: "创建受治理的 Agent 提案", ConversationID: conversation.ID, UserID: adminID, MaxSteps: 5,
	}, agentapp.ExecMeta{TenantID: tenantID, TraceID: uuid.NewString()})
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, domain.SystemAssistantToolProposeResourceChange, result.ToolCalls[0].ToolName)
	require.Len(t, result.AssistantToolArtifacts, 1)
	require.NotNil(t, result.AssistantToolArtifacts[0].Proposal)
	require.NotEmpty(t, result.AssistantToolArtifacts[0].Proposal.ID)

	stored, err := repo.Get(ctx, result.AssistantToolArtifacts[0].Proposal.ID)
	require.NoError(t, err)
	require.Equal(t, tenantID, stored.TenantID)
	require.Equal(t, adminID, stored.ProposerID)
	require.Equal(t, domain.StatusReadyForReview, stored.Status)

	// 同租户 member 走同一条真实授权链 → 拒绝创建。
	memberCtx := assistantTenantContext(tenantID, memberID, tenantdb.RoleTenantUser)
	_, err = proposalService.CreateProposal(memberCtx, agentapp.CreateProposalInput{
		TenantID: tenantID, ActorID: memberID, Kind: domain.ResourceAgent,
		Operation: domain.OperationCreate,
		Payload: json.RawMessage(
			`{"name":"member-denied","description":"x","model":"m","maxIterations":2,"maxContextTokens":1024}`,
		),
	})
	require.ErrorIs(t, err, domain.ErrProposalForbidden)

	// member 通过真实执行路径尝试调用提案工具：工具未暴露（ProposalCreateFn
	// 未注入），调用以“tool unavailable”失败，且不会产生任何 DB 提案。
	memberService := agentapp.NewAgentService(agentapp.AgentServiceDeps{
		Registry:             agentapp.NewRegistry(agentRepo, agentapp.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantResolver:       deterministicTenantResolver{gateway: &proposalToolGateway{}},
		TenantModelValidator: deterministicModelValidator{},
		TenantModelCatalog:   deterministicModelValidator{},
		ChatStore:            chat,
		OfficialDocsSearch:   officialdocs.Search,
		DiagnosticProvider:   roleAwareAssistantDiagnostics{role: "member"},
		ProposalService:      proposalService,
		Logger:               zap.NewNop(),
	})
	memberConversation, err := chat.CreateConversation(ctx, tenantID, domain.SystemAssistantID, memberID, "member 提案")
	require.NoError(t, err)
	memberResult, _, err := memberService.Execute(ctx, domain.SystemAssistantID, agentapp.ExecRequest{
		Query: "创建提案", ConversationID: memberConversation.ID, UserID: memberID, MaxSteps: 5,
	}, agentapp.ExecMeta{TenantID: tenantID, TraceID: uuid.NewString()})
	require.NoError(t, err)
	for _, artifact := range memberResult.AssistantToolArtifacts {
		require.Nil(t, artifact.Proposal, "member must not produce a proposal artifact")
	}

	var memberProposals int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM "tenant_`+tenantID+`".resource_change_proposals WHERE proposer_id=$1`,
		memberID,
	).Scan(&memberProposals))
	require.Zero(t, memberProposals)
}
