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
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
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
			{ID: "official-1", Name: domain.SystemAssistantToolSearchOfficialDocs, Arguments: map[string]any{"query": "Agent 使用"}},
			{ID: "diagnose-1", Name: domain.SystemAssistantToolDiagnoseTenant, Arguments: map[string]any{"areas": []any{"agent", "mcp"}}},
		}}, nil
	case 2:
		return agentport.CapabilityResponse{Content: "已完成官方检索和租户诊断。", Usage: agentport.TokenUsage{Total: 12}}, nil
	default:
		return agentport.CapabilityResponse{}, errors.New("unexpected deterministic model call")
	}
}

type deterministicTenantResolver struct{ gateway agentport.CapabilityGateway }

func (r deterministicTenantResolver) Resolve(context.Context, string) (agentport.CapabilityGateway, bool) {
	return r.gateway, true
}

func (r deterministicTenantResolver) InjectCompleter(ctx context.Context, _ string) context.Context {
	return ctx
}

type deterministicDiagnostics struct{}

type allowPlatformToolScope struct{}

func (allowPlatformToolScope) ResolveToolUserScope(
	context.Context,
	string, string, string, string,
) (agentport.ToolUserScope, error) {
	return agentport.ToolUserScope{UserActive: true, AllowsTool: true}, nil
}

func deterministicDiagnosticEvidence() domain.DiagnosticEvidence {
	return domain.DiagnosticEvidence{
		Scope: domain.DiagnosticScopeTenant,
		Facts: []domain.DiagnosticFact{{Area: domain.DiagnosticAreaAgent, ObjectID: "agent-e2e",
			Statement: "Agent 状态可读取", Source: "agent_execution", ObservedAt: time.Now().UTC()}},
		Gaps: []domain.EvidenceGap{{Area: domain.DiagnosticAreaMCP, Source: "mcp_status",
			Code: domain.DiagnosticGapUnavailable}},
		AreaResults: []domain.DiagnosticAreaResult{
			{Area: domain.DiagnosticAreaAgent, Outcome: "success", DurationMs: 1},
			{Area: domain.DiagnosticAreaMCP, Outcome: "unavailable", DurationMs: 1},
		},
		CollectedAt: time.Now().UTC(),
	}
}

type deterministicModelValidator struct{}

func (deterministicModelValidator) ValidateTenantChatModel(_ context.Context, _ string, model string) error {
	if model != "deterministic-e2e-model" {
		return domain.ErrInvalidAgentModel
	}
	return nil
}

func (deterministicModelValidator) ListTenantChatModels(context.Context, string) ([]string, error) {
	return []string{"deterministic-e2e-model"}, nil
}

func (deterministicDiagnostics) Authorize(_ context.Context, request domain.DiagnosticRequest) (domain.DiagnosticAuthorization, error) {
	request.Scope = domain.DiagnosticScopeTenant
	return domain.DiagnosticAuthorization{Request: request, RoleClass: "admin"}, nil
}

func (deterministicDiagnostics) CollectAuthorized(_ context.Context, request domain.DiagnosticRequest) (domain.DiagnosticEvidence, error) {
	evidence := deterministicDiagnosticEvidence()
	evidence.Scope = request.Scope
	return evidence, nil
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
	if value := os.Getenv("TEST_POSTGRES_URL"); value != "" {
		return value
	}
	if os.Getenv("REQUIRE_PLATFORM_ASSISTANT_E2E") == "1" {
		t.Fatal("platform assistant PostgreSQL E2E requires STRATUM_TEST_POSTGRES_URL or TEST_POSTGRES_URL")
	}
	if os.Getenv("CI") != "" {
		t.Skip("system assistant PostgreSQL E2E runs in the dedicated memory E2E job")
	}
	return "postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"
}

func TestSystemAssistantPostgresURLAcceptsMemoryE2EDSNUnderCI(t *testing.T) {
	t.Setenv("STRATUM_TEST_POSTGRES_URL", "")
	t.Setenv("TEST_POSTGRES_URL", "postgres://ci-memory-e2e")
	t.Setenv("CI", "true")

	require.Equal(t, "postgres://ci-memory-e2e", systemAssistantPostgresURL(t))
}

func TestSystemAssistantPostgresURLRequireModeFailsClosed(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestSystemAssistantPostgresURLFailClosedProbe$")
	cmd.Env = append(os.Environ(),
		"PLATFORM_ASSISTANT_E2E_FAILURE_PROBE=1",
		"REQUIRE_PLATFORM_ASSISTANT_E2E=1",
		"STRATUM_TEST_POSTGRES_URL=",
		"TEST_POSTGRES_URL=",
	)
	require.Error(t, cmd.Run(), "require mode accepted a missing PostgreSQL DSN")
}

func TestSystemAssistantPostgresURLFailClosedProbe(t *testing.T) {
	if os.Getenv("PLATFORM_ASSISTANT_E2E_FAILURE_PROBE") != "1" {
		t.Skip("subprocess probe only")
	}
	_ = systemAssistantPostgresURL(t)
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
			}
		}
		require.Equal(t, 1, managed, "each tenant must contain exactly one managed assistant")
	}

	ctxA := assistantTenantContext(tenants[0], users[tenants[0]][0], tenantdb.RoleTenantAdmin)
	ctxB := assistantTenantContext(tenants[1], users[tenants[1]][0], tenantdb.RoleTenantAdmin)
	existing, found, err := repo.Get(ctxA, domain.SystemAssistantID)
	require.NoError(t, err)
	require.True(t, found)
	existing.LLMModel = "deterministic-e2e-model"
	err = repo.Update(ctxA, existing, nil, "", false)
	require.NoError(t, err)
	updated, found, err := repo.Get(ctxA, domain.SystemAssistantID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "deterministic-e2e-model", updated.LLMModel)
	other, found, err := repo.Get(ctxB, domain.SystemAssistantID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "glm-5.2", other.LLMModel, "tenant A model selection must not cross into tenant B")

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

	_, _, err = repo.Get(assistantTenantContext("bad tenant!", users[tenants[0]][0], tenantdb.RoleTenantAdmin), domain.SystemAssistantID)
	require.Error(t, err)
	require.NoError(t, repo.Update(ctxA, &domain.AgentConfig{ID: domain.SystemAssistantID}, nil, "", false))
	require.NoError(t, repo.Remove(ctxA, domain.SystemAssistantID, nil))
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
		{Tool: domain.SystemAssistantToolSearchOfficialDocs, Outcome: "success", Citations: citations, LatencyMs: 2},
		{Tool: domain.SystemAssistantToolDiagnoseTenant, Outcome: "gap", Evidence: &domain.DiagnosticEvidence{
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
	require.Len(t, tools, 8)
	for _, tool := range tools {
		require.Equal(t, domain.ProviderTypeInternal, tool.ProviderType)
		require.NotEmpty(t, tool.InputSchema)
	}
}

func TestSystemAssistantDeterministicAgentLoopPersistsTypedArtifacts(t *testing.T) {
	pool, tenants := setupSystemAssistantPostgres(t)
	tenantID, userID := tenants[0], uuid.NewString()
	ctx := assistantTenantContext(tenantID, userID, tenantdb.RoleTenantAdmin)
	repo := agentpersist.NewPgAgentRepo(pool)
	existing, found, err := repo.Get(ctx, domain.SystemAssistantID)
	require.NoError(t, err)
	require.True(t, found)
	existing.LLMModel = "deterministic-e2e-model"
	err = repo.Update(ctx, existing, nil, "", false)
	require.NoError(t, err)

	gateway := &deterministicAssistantGateway{}
	chat := agentpersist.NewPgChatStore(pool, zap.NewNop())
	registry := agentapp.NewRegistry(repo, zap.NewNop())
	service := agentapp.NewAgentService(agentapp.AgentServiceDeps{
		Registry: registry, TenantResolver: deterministicTenantResolver{gateway: gateway},
		TenantModelValidator: deterministicModelValidator{}, TenantModelCatalog: deterministicModelValidator{}, ChatStore: chat,
		OfficialDocsSearch: officialdocs.Search,
		DiagnosticProvider: deterministicDiagnostics{}, Logger: zap.NewNop(),
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
	require.Equal(t, domain.SystemAssistantToolSearchOfficialDocs, result.ToolCalls[0].ToolName)
	require.Equal(t, domain.SystemAssistantToolDiagnoseTenant, result.ToolCalls[1].ToolName)
	require.Len(t, result.AssistantToolArtifacts, 2, "typed MCP evidence must survive the shared tool loop")
	require.Equal(t, domain.SystemAssistantToolSearchOfficialDocs, result.AssistantToolArtifacts[0].Tool)
	require.Equal(t, "success", result.AssistantToolArtifacts[0].Outcome)
	require.Empty(t, result.AssistantToolArtifacts[0].ErrorCode)
	require.NotEmpty(t, result.AssistantToolArtifacts[0].Citations)
	require.Len(t, result.Artifacts, 2)
	require.Equal(t, "citations", result.Artifacts[0].Type)
	require.Equal(t, "diagnostic_report", result.Artifacts[1].Type)
	for _, artifact := range result.Artifacts {
		require.Equal(t, domain.CurrentSystemAssistantProfileVersion, artifact.ProfileVersion)
	}
	require.Len(t, gateway.requests, 2)
	// D6：工具全量暴露（不再按角色裁剪），确定性模型只选用 search/diagnose。
	// plan 工具前置追加（修复：governed 系统助手同样暴露 plan 工具面），
	// 后随 8 个内部工具；位置断言需偏移 plan 工具数。
	planTools := graph.PlanToolDefinitions()
	require.Len(t, gateway.requests[0].LLM.Tools, len(agentapp.SystemAssistantToolDefinitions())+len(planTools)+1)
	for i, tool := range planTools {
		require.Equal(t, tool.Name, gateway.requests[0].LLM.Tools[i].Name)
	}
	for i, name := range []string{domain.SystemAssistantToolSearchOfficialDocs, domain.SystemAssistantToolDiagnoseTenant} {
		require.Equal(t, name, gateway.requests[0].LLM.Tools[len(planTools)+1+i].Name)
	}

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
		Registry:             agentapp.NewRegistry(repo, zap.NewNop()),
		TenantModelValidator: deterministicModelValidator{}, TenantModelCatalog: deterministicModelValidator{},
		// 等同化后平台助手走普通权限矩阵：owner 恒放行（seed created_by=''，
		// admin 因 created_by 不匹配被拒），验证 owner 可普通管理平台助手。
		TenantRoleResolver: systemAssistantRoleResolver{roles: map[string]string{
			tenantID + ":" + userID: "owner",
		}},
		Logger: zap.NewNop(),
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
	require.Contains(t, list.Body.String(), domain.SystemAssistantID)
	bindings := request(http.MethodPut, "/agents/"+domain.SystemAssistantID, "admin",
		`{"name":"tampered","llmModel":"deterministic-e2e-model",`+
			`"memoryScope":"user","allowedSkills":[],"mcpToolIds":[],`+
			`"knowledgeWorkspaceIds":[]}`)
	require.Equal(t, http.StatusOK, bindings.Code)
	require.Contains(t, bindings.Body.String(), `"id":"stratum-platform-assistant"`)
	persisted, found, err := repo.Get(
		assistantTenantContext(tenantID, userID, tenantdb.RoleTenantAdmin), domain.SystemAssistantID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, domain.SystemAssistantID, persisted.ID)
	require.Equal(t, domain.SystemAssistantKey, persisted.SystemKey)
	require.Equal(t, "tampered", persisted.Name, "普通字段经普通 Update 路径落库")
	// 等同化后无专属保留：传入空数组按普通 replace 语义清空 seed 资源。
	require.Empty(t, persisted.AllowedSkills)
	require.Empty(t, persisted.MCPToolIDs)
	require.Empty(t, persisted.KnowledgeWorkspaceIDs)
	// 等同化后平台助手可普通删除：owner 恒放行（seed created_by=''）。
	require.Equal(t, http.StatusOK,
		request(http.MethodDelete, "/agents/"+domain.SystemAssistantID, "admin", "").Code)
}

var _ agentport.TenantRoleResolver = systemAssistantRoleResolver{}
var _ agentport.TenantCapabilityResolver = deterministicTenantResolver{}
var _ agentport.DiagnosticEvidenceProvider = deterministicDiagnostics{}
var _ agentport.TenantChatModelValidator = deterministicModelValidator{}
var _ agentport.ToolUserScopeResolver = allowPlatformToolScope{}
