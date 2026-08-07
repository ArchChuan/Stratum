package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockAgentRepo is a scriptable AgentRepo stub for handler tests.
type mockAgentRepo struct {
	agents      []*domain.AgentConfig
	err         error
	removeErr   error
	registerErr error
	updateErr   error
}

func (m *mockAgentRepo) Register(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent) error {
	return m.registerErr
}
func (m *mockAgentRepo) Get(_ context.Context, id string) (*domain.AgentConfig, bool, error) {
	if m.err != nil {
		return nil, false, m.err
	}
	for _, a := range m.agents {
		if a.ID == id {
			return a, true, nil
		}
	}
	return nil, false, nil
}
func (m *mockAgentRepo) GetSystemAssistant(context.Context) (*domain.AgentConfig, bool, error) {
	if m.err != nil {
		return nil, false, m.err
	}
	for _, a := range m.agents {
		if a.IsSystem {
			return a, true, nil
		}
	}
	return nil, false, nil
}
func (m *mockAgentRepo) GetAll(context.Context) ([]*domain.AgentConfig, error) {
	return m.agents, m.err
}
func (m *mockAgentRepo) Remove(_ context.Context, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return m.removeErr
}
func (m *mockAgentRepo) Update(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent) error {
	return m.updateErr
}
func (m *mockAgentRepo) UpdateSystemAssistantModel(_ context.Context, _ string, _ string, _ bool, _ int, _ int, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	return nil, m.err
}
func (*mockAgentRepo) UpdateSystemAssistantAll(_ context.Context, _ string, _ string, _ bool, _ int, _ int, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	return nil, nil
}
func (m *mockAgentRepo) UpdateSystemAssistantBindings(context.Context, []string, []string, []string) (*domain.AgentConfig, error) {
	return nil, m.err
}

// fakeEvidence 是 TraceEvidenceProvider 的可脚本化 stub。
type fakeEvidence struct {
	records []domain.ExecutionRecord
	total   int64
	userID  string
	err     error
}

func (f *fakeEvidence) ListExecutions(context.Context, string, domain.ListOptions) ([]domain.ExecutionRecord, int64, error) {
	return f.records, f.total, f.err
}
func (f *fakeEvidence) ToolObservations(context.Context, string, string) ([]domain.ToolObservation, error) {
	return nil, f.err
}
func (f *fakeEvidence) TraceEvents(context.Context, string, string) ([]domain.AgentTraceEvent, error) {
	return nil, f.err
}
func (f *fakeEvidence) Resolve(context.Context, string, string) (domain.TraceEvidence, error) {
	if f.err != nil {
		return domain.TraceEvidence{}, f.err
	}
	return domain.TraceEvidence{UserID: f.userID}, nil
}
func (f *fakeEvidence) ResolveBatch(context.Context, string, []string) (map[string]domain.TraceEvidence, error) {
	return nil, f.err
}

// fakeCheckpointStore 让 Pause/Resume 的失败路径可测。
type fakeCheckpointStore struct {
	updateErr error
}

func (f fakeCheckpointStore) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	return nil
}
func (f fakeCheckpointStore) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, nil
}
func (f fakeCheckpointStore) MarkCompleted(context.Context, string, string) error { return nil }
func (f fakeCheckpointStore) UpdateStatus(context.Context, string, string, string) error {
	return f.updateErr
}
func (f fakeCheckpointStore) DeleteExpired(context.Context, string) (int64, error) { return 0, nil }

func newTestAgentHandler(t *testing.T, repo *mockAgentRepo, evidence port.TraceEvidenceProvider, mut func(*agent.AgentServiceDeps)) *AgentHandler {
	t.Helper()
	deps := agent.AgentServiceDeps{
		Registry:         agent.NewRegistry(repo, agent.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		EvidenceProvider: evidence,
		Logger:           zap.NewNop(),
	}
	if mut != nil {
		mut(&deps)
	}
	svc := agent.NewAgentService(deps)
	svc.SetTenantRoleResolver(fixedTenantRole{role: "owner"})
	return NewAgentHandler(svc, zap.NewNop())
}

// agentRoutes 注册本文件被测的全部路由，统一挂 ErrorHandler；
// auth 中间件可选（传 withAuth(...) 注入 tenant/user）。
func agentRoutes(h *AgentHandler, auth ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	for _, a := range auth {
		r.Use(a)
	}
	g := r.Group("/agents")
	g.GET("", h.GetAllAgents)
	g.GET("/:id", h.GetAgent)
	g.POST("", h.CreateAgent)
	g.PUT("/:id", h.UpdateAgent)
	g.DELETE("/:id", h.DeleteAgent)
	g.GET("/:id/executions", h.ListExecutions)
	g.GET("/:id/executions/:traceID/tool-traces", h.ListExecutionToolTraces)
	g.GET("/:id/executions/:traceID/trace-events", h.ListExecutionTraceEvents)
	g.GET("/:id/approvals", h.ListToolApprovals)
	g.POST("/:id/approvals/:approvalID/decide", h.DecideToolApproval)
	g.POST("/:id/approvals/:approvalID/resume", h.ResumeToolApproval)
	g.POST("/:id/pause/:executionID", h.PauseExecution)
	g.POST("/:id/resume/:executionID", h.ResumeExecution)
	return r
}

func withAuth(tenantID, userID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := reqctx.WithTenantID(c.Request.Context(), tenantID)
		c.Request = c.Request.WithContext(ctx)
		c.Set(middleware.ContextKeySub, userID)
		c.Next()
	}
}

// withTenantOnly 只注入 tenant、不设置 user，用于缺 user 的 401 用例。
func withTenantOnly(tenantID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := reqctx.WithTenantID(c.Request.Context(), tenantID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// authedRoutes 注入 t1/u1 的默认认证 router。
func authedRoutes(h *AgentHandler) *gin.Engine {
	return agentRoutes(h, withAuth("t1", "u1"))
}

func doAgentReq(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil) //nolint:noctx
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body)) //nolint:noctx
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAgentHandlerGetAllAgents(t *testing.T) {
	repo := &mockAgentRepo{agents: []*domain.AgentConfig{{ID: "a1", Name: "Alpha"}, {ID: "a2", Name: "Beta"}}}
	h := newTestAgentHandler(t, repo, nil, nil)

	w := doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"agents"`)
	require.Contains(t, w.Body.String(), "Alpha")

	// 极端情况：缺 tenant → 401。
	w = doAgentReq(t, agentRoutes(h), http.MethodGet, "/agents", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)

	// 极端情况：repo 失败 → 500。
	h = newTestAgentHandler(t, &mockAgentRepo{err: errors.New("db down")}, nil, nil)
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAgentHandlerGetAgent(t *testing.T) {
	repo := &mockAgentRepo{agents: []*domain.AgentConfig{{ID: "a1", Name: "Alpha"}}}
	h := newTestAgentHandler(t, repo, nil, nil)

	w := doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Alpha")

	// 极端情况：不存在 → 404。
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/missing", "")
	require.Equal(t, http.StatusNotFound, w.Code)

	// 极端情况：repo 失败 → 500。
	h = newTestAgentHandler(t, &mockAgentRepo{err: errors.New("db down")}, nil, nil)
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAgentHandlerCreateAgent(t *testing.T) {
	h := newTestAgentHandler(t, &mockAgentRepo{}, nil, nil)
	valid := `{"name":"N","llmModel":"qwen-max","maxIterations":5}`

	// 缺 tenant → 401。
	w := doAgentReq(t, agentRoutes(h), http.MethodPost, "/agents", valid)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	// 极端情况：非法 JSON → 400。
	w = doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents", "{")
	require.Equal(t, http.StatusBadRequest, w.Code)

	// 极端情况：缺少 required 字段 → 400。
	w = doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents", `{}`)
	require.Equal(t, http.StatusBadRequest, w.Code)

	// 成功 → 201。
	w = doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents", valid)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Contains(t, w.Body.String(), `"id"`)

	// 极端情况：持久化失败 → 500。
	h = newTestAgentHandler(t, &mockAgentRepo{registerErr: errors.New("write failed")}, nil, nil)
	w = doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents", valid)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAgentHandlerUpdateAgent(t *testing.T) {
	repo := &mockAgentRepo{agents: []*domain.AgentConfig{{ID: "a1", Name: "Alpha", LLMModel: "qwen-max"}}}
	h := newTestAgentHandler(t, repo, nil, nil)
	valid := `{"name":"Renamed","llmModel":"qwen-max","maxIterations":3}`

	// 极端情况：不存在 → 404。
	w := doAgentReq(t, authedRoutes(h), http.MethodPut, "/agents/missing", valid)
	require.Equal(t, http.StatusNotFound, w.Code)

	// 极端情况：非法 JSON → 400。
	w = doAgentReq(t, authedRoutes(h), http.MethodPut, "/agents/a1", "{")
	require.Equal(t, http.StatusBadRequest, w.Code)

	// 成功 → 200。
	w = doAgentReq(t, authedRoutes(h), http.MethodPut, "/agents/a1", valid)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Renamed")

	// 极端情况：持久化失败 → 500。
	repo.updateErr = errors.New("write failed")
	w = doAgentReq(t, authedRoutes(h), http.MethodPut, "/agents/a1", valid)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAgentHandlerDeleteAgent(t *testing.T) {
	repo := &mockAgentRepo{agents: []*domain.AgentConfig{{ID: "a1", Name: "Alpha"}}}
	h := newTestAgentHandler(t, repo, nil, nil)

	// 成功 → 200。
	w := doAgentReq(t, authedRoutes(h), http.MethodDelete, "/agents/a1", "")
	require.Equal(t, http.StatusOK, w.Code)

	// 极端情况：不存在 → 404。
	w = doAgentReq(t, authedRoutes(h), http.MethodDelete, "/agents/missing", "")
	require.Equal(t, http.StatusNotFound, w.Code)

	// 极端情况：system assistant 禁止删除 → 409。
	sys := &mockAgentRepo{agents: []*domain.AgentConfig{{ID: "sys-1", SystemKey: "system_assistant"}}}
	h = newTestAgentHandler(t, sys, nil, nil)
	w = doAgentReq(t, authedRoutes(h), http.MethodDelete, "/agents/sys-1", "")
	require.Equal(t, http.StatusConflict, w.Code)

	// 极端情况：删除失败 → 500。
	h = newTestAgentHandler(t, repo, nil, nil)
	repo.removeErr = errors.New("delete failed")
	w = doAgentReq(t, authedRoutes(h), http.MethodDelete, "/agents/a1", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAgentHandlerPauseResumeExecution(t *testing.T) {
	// 极端情况：checkpoint store 未配置 → Pause 500。
	h := newTestAgentHandler(t, &mockAgentRepo{}, nil, nil)
	w := doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents/a1/pause/exec-1", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)

	// 极端情况：checkpoint 状态写失败 → Pause/Resume 均 500。
	h = newTestAgentHandler(t, &mockAgentRepo{}, nil, func(deps *agent.AgentServiceDeps) {
		deps.CheckpointStore = fakeCheckpointStore{updateErr: errors.New("db down")}
	})
	w = doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents/a1/pause/exec-1", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	w = doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents/a1/resume/exec-1", `{"query":"q"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)

	// 极端情况：缺 tenant → 401。
	w = doAgentReq(t, agentRoutes(h), http.MethodPost, "/agents/a1/pause/exec-1", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAgentHandlerListExecutions(t *testing.T) {
	created := time.Now()
	evidence := &fakeEvidence{
		records: []domain.ExecutionRecord{{
			ID: "e1", TraceID: "t1", AgentID: "a1", AgentName: "Research", UserID: "u1",
			Status: "completed", InputPreview: "in", OutputPreview: "out", TotalTokens: 42,
			DurationMs: 100, CreatedAt: created,
		}},
		total: 1,
	}
	h := newTestAgentHandler(t, &mockAgentRepo{}, evidence, nil)

	w := doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/executions", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Research")
	require.Contains(t, w.Body.String(), `"total":1`)

	// 极端情况：非法 page/page_size → 解析为 0 但请求成功。
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/executions?page=abc&page_size=-1", "")
	require.Equal(t, http.StatusOK, w.Code)

	// 极端情况：空记录 → 200 + 空数组。
	h = newTestAgentHandler(t, &mockAgentRepo{}, &fakeEvidence{}, nil)
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/executions", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"executions":[]`)

	// 极端情况：provider 失败 → 500。
	h = newTestAgentHandler(t, &mockAgentRepo{}, &fakeEvidence{err: errors.New("db down")}, nil)
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/executions", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)

	// 极端情况：缺 user → 401。
	w = doAgentReq(t, agentRoutes(h, withTenantOnly("t1")), http.MethodGet, "/agents/a1/executions", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAgentHandlerListExecutionToolTracesAndEvents(t *testing.T) {
	// 本人 → 200。
	evidence := &fakeEvidence{userID: "u1"}
	h := newTestAgentHandler(t, &mockAgentRepo{}, evidence, nil)
	w := doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/executions/trace-1/tool-traces", "")
	require.Equal(t, http.StatusOK, w.Code)
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/executions/trace-1/trace-events", "")
	require.Equal(t, http.StatusOK, w.Code)

	// 极端情况：非本人（偷窥）→ 404。
	h = newTestAgentHandler(t, &mockAgentRepo{}, &fakeEvidence{userID: "other"}, nil)
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/executions/trace-1/tool-traces", "")
	require.Equal(t, http.StatusNotFound, w.Code)
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/executions/trace-1/trace-events", "")
	require.Equal(t, http.StatusNotFound, w.Code)

	// 极端情况：provider 失败 → 500。
	h = newTestAgentHandler(t, &mockAgentRepo{}, &fakeEvidence{userID: "u1", err: errors.New("db down")}, nil)
	w = doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/executions/trace-1/tool-traces", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)

	// 极端情况：缺 user → 401。
	w = doAgentReq(t, agentRoutes(h, withTenantOnly("t1")), http.MethodGet, "/agents/a1/executions/trace-1/tool-traces", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAgentHandlerListToolApprovals(t *testing.T) {
	// ApprovalService 未配置 → fail closed：200 + 空列表。
	h := newTestAgentHandler(t, &mockAgentRepo{}, nil, nil)
	w := doAgentReq(t, authedRoutes(h), http.MethodGet, "/agents/a1/approvals", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"approvals":[]`)

	// 极端情况：缺 tenant → 401。
	w = doAgentReq(t, agentRoutes(h), http.MethodGet, "/agents/a1/approvals", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAgentHandlerDecideToolApproval(t *testing.T) {
	h := newTestAgentHandler(t, &mockAgentRepo{}, nil, nil)

	// 极端情况：缺 tenant → 401。
	w := doAgentReq(t, agentRoutes(h), http.MethodPost, "/agents/a1/approvals/app-1/decide", `{"decision":"approve"}`)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	// 极端情况：非法 JSON → 400。
	w = doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents/a1/approvals/app-1/decide", "{")
	require.Equal(t, http.StatusBadRequest, w.Code)

	// ApprovalService 未配置 → 500。
	w = doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents/a1/approvals/app-1/decide", `{"decision":"approve"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAgentHandlerResumeToolApproval(t *testing.T) {
	h := newTestAgentHandler(t, &mockAgentRepo{}, nil, nil)

	// 极端情况：缺 tenant → 401。
	w := doAgentReq(t, agentRoutes(h), http.MethodPost, "/agents/a1/approvals/app-1/resume", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)

	// Approval runtime 未配置 → 500。
	w = doAgentReq(t, authedRoutes(h), http.MethodPost, "/agents/a1/approvals/app-1/resume", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// fixedTenantRole resolves every actor as owner so handler tests reach the
// service write path; ownership specifics are covered in application tests.
type fixedTenantRole struct{ role string }

func (r fixedTenantRole) ResolveTenantRole(context.Context, string, string) (string, error) {
	return r.role, nil
}
