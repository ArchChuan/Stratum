package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/officialdocs"
	agentpersist "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	pgstorage "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type systemAssistantRoleResolver struct {
	roles map[string]string
	err   error
}

type deterministicAssistantGateway struct {
	requests []agentport.CapabilityRequest
	call     int
}

func (g *deterministicAssistantGateway) Route(_ context.Context, request agentport.CapabilityRequest) (agentport.CapabilityResponse, error) {
	g.requests = append(g.requests, request)
	g.call++
	switch g.call {
	case 1:
		return agentport.CapabilityResponse{ToolCalls: []agentport.ToolCall{
			{ID: "official-1", Name: agentapp.ToolSearchOfficialDocs, Arguments: map[string]any{"query": "Agent 使用"}},
			{ID: "diagnose-1", Name: agentapp.ToolDiagnoseTenant, Arguments: map[string]any{"areas": []any{"agent", "mcp"}}},
		}}, nil
	case 2:
		return agentport.CapabilityResponse{Content: "已完成官方检索和租户诊断。", Usage: agentport.TokenUsage{Total: 12}}, nil
	default:
		return agentport.CapabilityResponse{}, errors.New("unexpected deterministic model call")
	}
}

type deterministicTenantResolver struct{ gateway agentport.CapabilityGateway }

func (r deterministicTenantResolver) Resolve(context.Context, string) (agentport.CapabilityGateway, map[string]string, bool) {
	return r.gateway, map[string]string{}, true
}

func (r deterministicTenantResolver) InjectCompleter(ctx context.Context, _ string) context.Context {
	return ctx
}

type deterministicDiagnostics struct{}

type deterministicModelValidator struct{}

func (deterministicModelValidator) ValidateTenantChatModel(_ context.Context, _ string, model string) error {
	if model != "deterministic-e2e-model" {
		return domain.ErrInvalidSystemAssistantModel
	}
	return nil
}

func (deterministicDiagnostics) Authorize(_ context.Context, request domain.DiagnosticRequest) (domain.DiagnosticAuthorization, error) {
	request.Scope = domain.DiagnosticScopeTenant
	return domain.DiagnosticAuthorization{Request: request, RoleClass: "admin"}, nil
}

func (deterministicDiagnostics) CollectAuthorized(_ context.Context, request domain.DiagnosticRequest) (domain.DiagnosticEvidence, error) {
	return domain.DiagnosticEvidence{
		Scope:       request.Scope,
		Facts:       []domain.DiagnosticFact{{Area: domain.DiagnosticAreaAgent, ObjectID: "agent-e2e", Statement: "Agent 状态可读取", Source: "agent_execution", ObservedAt: time.Now().UTC()}},
		Gaps:        []domain.EvidenceGap{{Area: domain.DiagnosticAreaMCP, Source: "mcp_status", Code: domain.DiagnosticGapUnavailable}},
		AreaResults: []domain.DiagnosticAreaResult{{Area: domain.DiagnosticAreaAgent, Outcome: "success", DurationMs: 1}, {Area: domain.DiagnosticAreaMCP, Outcome: "unavailable", DurationMs: 1}},
		CollectedAt: time.Now().UTC(),
	}, nil
}

func (r systemAssistantRoleResolver) ResolveTenantRole(_ context.Context, tenantID, userID string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.roles[tenantID+":"+userID], nil
}

func systemAssistantPostgresURL(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("STRATUM_TEST_POSTGRES_URL"); value != "" {
		return value
	}
	if os.Getenv("CI") != "" {
		t.Fatal("STRATUM_TEST_POSTGRES_URL is required for system assistant E2E in CI")
	}
	if value := os.Getenv("TEST_POSTGRES_URL"); value != "" {
		return value
	}
	return "postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"
}

func setupSystemAssistantPostgres(t *testing.T) (*pgxpool.Pool, []string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, systemAssistantPostgresURL(t))
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx), "PostgreSQL is required for system assistant E2E")
	require.NoError(t, pgstorage.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	tenants := []string{uuid.NewString(), uuid.NewString()}
	for _, tenantID := range tenants {
		require.NoError(t, pgstorage.ProvisionTenantSchema(ctx, pool, tenantID))
	}
	t.Cleanup(func() {
		for _, tenantID := range tenants {
			_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID))
		}
		pool.Close()
	})
	return pool, tenants
}

func assistantTenantContext(tenantID, userID string, role tenantdb.Role) context.Context {
	return tenantdb.WithTenant(context.Background(), &tenantdb.TenantContext{
		TenantID: tenantID, UserID: userID, Role: role,
	})
}

func TestSystemAssistantTenantIsolationAndRoleScope(t *testing.T) {
	pool, tenants := setupSystemAssistantPostgres(t)
	repo := agentpersist.NewPgAgentRepo(pool)
	users := map[string][]string{
		tenants[0]: {uuid.NewString(), uuid.NewString()},
		tenants[1]: {uuid.NewString(), uuid.NewString()},
	}

	for _, tenantID := range tenants {
		ctx := assistantTenantContext(tenantID, users[tenantID][0], tenantdb.RoleTenantAdmin)
		agents, err := repo.GetAll(ctx)
		require.NoError(t, err)
		managed := 0
		for _, item := range agents {
			if item.SystemKey == domain.SystemAssistantKey {
				managed++
				require.Equal(t, domain.SystemAssistantID, item.ID)
				require.True(t, item.IsSystem)
				require.Equal(t, "platform", item.ManagementMode)
			}
		}
		require.Equal(t, 1, managed, "each tenant must contain exactly one managed assistant")
	}

	ctxA := assistantTenantContext(tenants[0], users[tenants[0]][0], tenantdb.RoleTenantAdmin)
	ctxB := assistantTenantContext(tenants[1], users[tenants[1]][0], tenantdb.RoleTenantAdmin)
	updated, err := repo.UpdateSystemAssistantModel(ctxA, "deterministic-e2e-model")
	require.NoError(t, err)
	require.Equal(t, "deterministic-e2e-model", updated.LLMModel)
	other, found, err := repo.GetSystemAssistant(ctxB)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, other.LLMModel, "tenant A model selection must not cross into tenant B")

	profile, err := agentapp.ComposeSystemAssistantProfile(updated, agentapp.BuiltinSystemAssistantProfile())
	require.NoError(t, err)
	require.Equal(t, domain.CurrentSystemAssistantProfileVersion,
		agentapp.BuiltinSystemAssistantProfileSource().Version())
	require.Empty(t, profile.AllowedSkills)
	require.Empty(t, profile.MCPToolIDs)
	require.Empty(t, profile.KnowledgeWorkspaceIDs)

	roles := systemAssistantRoleResolver{roles: map[string]string{
		tenants[0] + ":" + users[tenants[0]][0]: "admin",
		tenants[0] + ":" + users[tenants[0]][1]: "member",
		tenants[1] + ":" + users[tenants[1]][0]: "owner",
		tenants[1] + ":" + users[tenants[1]][1]: "member",
	}}
	adminAuth, err := agentapp.AuthorizeDiagnosticRequest(context.Background(), roles, domain.DiagnosticRequest{
		TenantID: tenants[0], UserID: users[tenants[0]][0], Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent},
	})
	require.NoError(t, err)
	require.Equal(t, domain.DiagnosticScopeTenant, adminAuth.Scope)
	memberAuth, err := agentapp.AuthorizeDiagnosticRequest(context.Background(), roles, domain.DiagnosticRequest{
		TenantID: tenants[0], UserID: users[tenants[0]][1], Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent},
	})
	require.NoError(t, err)
	require.Equal(t, domain.DiagnosticScopeSelf, memberAuth.Scope)

	_, err = agentapp.AuthorizeDiagnosticRequest(context.Background(),
		systemAssistantRoleResolver{err: errors.New("membership backend unavailable with sensitive detail")},
		domain.DiagnosticRequest{TenantID: tenants[0], UserID: users[tenants[0]][1], Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sensitive detail")

	_, _, err = repo.GetSystemAssistant(assistantTenantContext("bad tenant!", users[tenants[0]][0], tenantdb.RoleTenantAdmin))
	require.Error(t, err)
	require.ErrorIs(t, repo.Remove(ctxA, domain.SystemAssistantID), domain.ErrSystemAssistantManaged)
	require.ErrorIs(t, repo.Update(ctxA, &domain.AgentConfig{ID: domain.SystemAssistantID}), domain.ErrSystemAssistantManaged)
}

func TestSystemAssistantOfficialDocsArtifactsAndAreaGap(t *testing.T) {
	pool, tenants := setupSystemAssistantPostgres(t)
	tenantA, tenantB := tenants[0], tenants[1]
	userA, userB := uuid.NewString(), uuid.NewString()

	citations, err := officialdocs.Search(context.Background(), "Agent 使用")
	require.NoError(t, err)
	require.NotEmpty(t, citations)
	for _, citation := range citations {
		require.NotEmpty(t, citation.ProductVersion)
		require.True(t, strings.HasPrefix(citation.URL, "/docs/"))
	}
	_, err = officialdocs.Search(context.Background(), "不存在词条zzzz-no-match")
	require.ErrorIs(t, err, domain.ErrOfficialEvidenceNotFound)

	report := domain.BuildDiagnosticReport([]domain.SystemAssistantToolArtifact{
		{Tool: agentapp.ToolSearchOfficialDocs, Outcome: "success", Citations: citations, LatencyMs: 2},
		{Tool: agentapp.ToolDiagnoseTenant, Outcome: "gap", Evidence: &domain.DiagnosticEvidence{
			Scope: domain.DiagnosticScopeTenant,
			Facts: []domain.DiagnosticFact{{Area: domain.DiagnosticAreaAgent, ObjectID: "tenant-a-agent",
				Statement: "Agent 状态可读取", Source: "agent_execution", ObservedAt: time.Now().UTC()}},
			Gaps: []domain.EvidenceGap{{Area: domain.DiagnosticAreaMCP, Source: "mcp_status", Code: domain.DiagnosticGapUnavailable}},
			AreaResults: []domain.DiagnosticAreaResult{
				{Area: domain.DiagnosticAreaAgent, Outcome: "success", DurationMs: 3},
				{Area: domain.DiagnosticAreaMCP, Outcome: "unavailable", DurationMs: 4},
			}, CollectedAt: time.Now().UTC(),
		}, LatencyMs: 7},
	})
	require.Len(t, report.EvidenceGaps, 1)
	require.Empty(t, report.Inferences)

	chat := agentpersist.NewPgChatStore(pool, zap.NewNop())
	conversation, err := chat.CreateConversation(context.Background(), tenantA, domain.SystemAssistantID, userA, "系统助手 E2E")
	require.NoError(t, err)
	message := &domain.ChatMessage{ConversationID: conversation.ID, Role: "assistant", Content: "诊断完成",
		Artifacts: []domain.ExecutionArtifact{
			{Type: "citations", ProfileVersion: domain.CurrentSystemAssistantProfileVersion, Citations: citations},
			{Type: "diagnostic_report", ProfileVersion: domain.CurrentSystemAssistantProfileVersion, DiagnosticReport: report},
		},
		SkipOutbox: true,
	}
	require.NoError(t, chat.AddMessage(context.Background(), tenantA, message))
	messages, err := chat.ListMessages(context.Background(), tenantA, conversation.ID, userA)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	wantJSON, err := json.Marshal(message.Artifacts)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(messages[0].Artifacts)
	require.NoError(t, err)
	require.JSONEq(t, string(wantJSON), string(gotJSON))

	_, err = chat.ListMessages(context.Background(), tenantB, conversation.ID, userB)
	require.NoError(t, err)
	foreign, err := chat.ListConversations(context.Background(), tenantB, domain.SystemAssistantID, userB)
	require.NoError(t, err)
	require.Empty(t, foreign)
	require.NotContains(t, string(gotJSON), tenantB)
	require.NotContains(t, string(gotJSON), userB)

	tools := agentapp.SystemAssistantToolDefinitions()
	require.Equal(t, []string{agentapp.ToolSearchOfficialDocs, agentapp.ToolDiagnoseTenant},
		[]string{tools[0].Name, tools[1].Name})
	for _, tool := range tools {
		require.Equal(t, domain.ProviderTypeInternal, tool.ProviderType)
	}
}

func TestSystemAssistantDeterministicAgentLoopPersistsTypedArtifacts(t *testing.T) {
	pool, tenants := setupSystemAssistantPostgres(t)
	tenantID, userID := tenants[0], uuid.NewString()
	ctx := assistantTenantContext(tenantID, userID, tenantdb.RoleTenantAdmin)
	repo := agentpersist.NewPgAgentRepo(pool)
	_, err := repo.UpdateSystemAssistantModel(ctx, "deterministic-e2e-model")
	require.NoError(t, err)

	gateway := &deterministicAssistantGateway{}
	chat := agentpersist.NewPgChatStore(pool, zap.NewNop())
	registry := agentapp.NewRegistry(repo, agentapp.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	service := agentapp.NewAgentService(agentapp.AgentServiceDeps{
		Registry: registry, TenantResolver: deterministicTenantResolver{gateway: gateway},
		TenantModelValidator: deterministicModelValidator{}, ChatStore: chat,
		OfficialDocsSearch: officialdocs.Search, DiagnosticProvider: deterministicDiagnostics{}, Logger: zap.NewNop(),
	})
	conversation, err := chat.CreateConversation(ctx, tenantID, domain.SystemAssistantID, userID, "确定性 Agent Loop")
	require.NoError(t, err)
	result, _, err := service.Execute(ctx, domain.SystemAssistantID, agentapp.ExecRequest{
		Query: "请检索官方 Agent 使用说明并诊断 Agent 与 MCP 状态", ConversationID: conversation.ID,
		UserID: userID, MaxSteps: 5,
	}, agentapp.ExecMeta{TenantID: tenantID, TraceID: uuid.NewString()})
	require.NoError(t, err)
	require.Equal(t, "已完成官方检索和租户诊断。", result.Output)
	require.Equal(t, 2, result.Steps)
	require.Len(t, result.ToolCalls, 2)
	require.Equal(t, agentapp.ToolSearchOfficialDocs, result.ToolCalls[0].ToolName)
	require.Equal(t, agentapp.ToolDiagnoseTenant, result.ToolCalls[1].ToolName)
	require.Len(t, result.Artifacts, 2)
	require.Equal(t, "citations", result.Artifacts[0].Type)
	require.Equal(t, "diagnostic_report", result.Artifacts[1].Type)
	for _, artifact := range result.Artifacts {
		require.Equal(t, domain.CurrentSystemAssistantProfileVersion, artifact.ProfileVersion)
	}
	require.Len(t, gateway.requests, 2)
	require.Len(t, gateway.requests[0].LLM.Tools, 2)
	require.Equal(t, []string{agentapp.ToolSearchOfficialDocs, agentapp.ToolDiagnoseTenant},
		[]string{gateway.requests[0].LLM.Tools[0].Name, gateway.requests[0].LLM.Tools[1].Name})

	messages, err := chat.ListMessages(ctx, tenantID, conversation.ID, userID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(messages), 2)
	var persisted *domain.ChatMessage
	for _, message := range messages {
		if message.Content == result.Output {
			persisted = message
			break
		}
	}
	require.NotNil(t, persisted)
	require.Equal(t, result.Output, persisted.Content)
	require.Equal(t, result.Artifacts, persisted.Artifacts)
}

func TestSystemAssistantHTTPContractsUseRealHandlerServiceAndPostgres(t *testing.T) {
	pool, tenants := setupSystemAssistantPostgres(t)
	tenantID, userID := tenants[0], uuid.NewString()
	repo := agentpersist.NewPgAgentRepo(pool)
	service := agentapp.NewAgentService(agentapp.AgentServiceDeps{
		Registry:             agentapp.NewRegistry(repo, agentapp.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: deterministicModelValidator{}, Logger: zap.NewNop(),
	})
	h := handler.NewAgentHandler(service, zap.NewNop())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		role := c.GetHeader("X-E2E-Role")
		c.Set("auth.tenant_id", tenantID)
		c.Set("auth.sub", userID)
		c.Set("auth.role", role)
		c.Next()
	}, middleware.InjectTenantContext())
	router.GET("/agents", middleware.RequireTenantRole("member"), h.GetAllAgents)
	router.GET("/agents/system/settings", middleware.RequireTenantRole("member"), h.GetSettings)
	router.PUT("/agents/system/settings", middleware.RequireTenantRole("admin"), h.UpdateModel)
	router.PUT("/agents/:id", middleware.RequireTenantRole("admin"), h.UpdateAgent)
	router.DELETE("/agents/:id", middleware.RequireTenantRole("admin"), h.DeleteAgent)

	request := func(method, path, role, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("X-E2E-Role", role)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}

	list := request(http.MethodGet, "/agents", "member", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.Contains(t, list.Body.String(), `"isSystem":true`)
	require.Contains(t, list.Body.String(), `"managementMode":"platform"`)
	settings := request(http.MethodGet, "/agents/system/settings", "member", "")
	require.Equal(t, http.StatusOK, settings.Code)
	require.JSONEq(t, `{"agentId":"stratum-platform-assistant","llmModel":"","ready":false}`, settings.Body.String())
	require.Equal(t, http.StatusForbidden,
		request(http.MethodPut, "/agents/system/settings", "member", `{"llmModel":"deterministic-e2e-model"}`).Code)
	updated := request(http.MethodPut, "/agents/system/settings", "admin", `{"llmModel":"deterministic-e2e-model"}`)
	require.Equal(t, http.StatusOK, updated.Code)
	require.JSONEq(t, `{"agentId":"stratum-platform-assistant","llmModel":"deterministic-e2e-model","ready":true}`, updated.Body.String())
	readback := request(http.MethodGet, "/agents/system/settings", "member", "")
	require.Equal(t, http.StatusOK, readback.Code)
	require.Contains(t, readback.Body.String(), `"llmModel":"deterministic-e2e-model"`)
	require.Equal(t, http.StatusConflict,
		request(http.MethodPut, "/agents/"+domain.SystemAssistantID, "admin",
			`{"name":"tampered","llmModel":"deterministic-e2e-model"}`).Code)
	require.Equal(t, http.StatusConflict,
		request(http.MethodDelete, "/agents/"+domain.SystemAssistantID, "admin", "").Code)
}

var _ agentport.TenantRoleResolver = systemAssistantRoleResolver{}
var _ agentport.TenantCapabilityResolver = deterministicTenantResolver{}
var _ agentport.DiagnosticEvidenceProvider = deterministicDiagnostics{}
var _ agentport.TenantChatModelValidator = deterministicModelValidator{}
