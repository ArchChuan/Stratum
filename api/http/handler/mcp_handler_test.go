package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// scriptedRoleResolver 按预设角色/错误解析 actor，覆盖 currentRole 的 DB resolver
// 现查分支（admin→直接执行、member→审批、解析失败/未知角色→403 fail closed）。
type scriptedRoleResolver struct {
	role string
	err  error
}

func (r scriptedRoleResolver) ResolveTenantRole(context.Context, string, string) (string, error) {
	return r.role, r.err
}

func newTestMCPHandler(t *testing.T) *MCPHandler {
	t.Helper()
	svc, _ := newMCPServiceForWriteTests(t)
	return NewMCPHandler(svc, zap.NewNop())
}

// fakeToolPolicyRepo 记录 SetToolPolicy Upsert，模拟 ToolPolicyRepo（admin 直通路径验证）。
type fakeToolPolicyRepo struct {
	upserts []mcpdomain.ToolPolicy
}

func (f *fakeToolPolicyRepo) Get(context.Context, string, string) (mcpdomain.ToolPolicy, bool, error) {
	return mcpdomain.ToolPolicy{}, false, nil
}
func (f *fakeToolPolicyRepo) List(context.Context) ([]mcpdomain.ToolPolicy, error) { return nil, nil }
func (f *fakeToolPolicyRepo) Upsert(_ context.Context, p mcpdomain.ToolPolicy) error {
	f.upserts = append(f.upserts, p)
	return nil
}

// newMCPServiceForWriteTests 构造可直通写方法（注入 fake ToolPolicyRepo）的 MCPService。
func newMCPServiceForWriteTests(t *testing.T) (*mcpapp.MCPService, *fakeToolPolicyRepo) {
	t.Helper()
	logger := zap.NewNop()
	manager := mcp.NewClientManager(logger, nil, nil)
	registry := mcp.NewMCPToolRegistry(manager, logger)
	svc := mcpapp.NewMCPService(mcp.ToolRegistryAsPort(registry), mcp.ServerManagerAsPort(manager), logger)
	repo := &fakeToolPolicyRepo{}
	svc.SetToolPolicyRepo(repo)
	return svc, repo
}

func TestMCPHandlerListServers(t *testing.T) {
	h := newTestMCPHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/servers", h.ListServers)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/servers", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestMCPHandlerGetServer(t *testing.T) {
	h := newTestMCPHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.GET("/servers/:id", h.GetServer)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/servers/test-server", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Errorf("expected status 200 or 404, got %d", w.Code)
	}
}

func TestMCPHandlerListTools(t *testing.T) {
	h := newTestMCPHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/servers/:id/tools", h.ListTools)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/servers/test-server/tools", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	// server 不存在时返回 500（client not found），这是预期行为
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound && w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 200, 404, or 500, got %d", w.Code)
	}
}

func TestMCPHandlerGetServerStatus(t *testing.T) {
	h := newTestMCPHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/status", h.GetServerStatus)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/status", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestMCPConfigRouteRequiresTenantAdmin(t *testing.T) {
	t.Parallel()

	h := newTestMCPHandler(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth.role", c.GetHeader("X-Test-Role"))
		c.Next()
	})
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	h.RegisterRoutes(router, nil, nil, []gin.HandlerFunc{middleware.RequireTenantRole("admin")})

	tests := []struct {
		name       string
		role       string
		wantStatus int
	}{
		{name: "member forbidden", role: "member", wantStatus: http.StatusForbidden},
		{name: "admin reaches handler", role: "admin", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/servers/missing/config", nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("X-Test-Role", tt.role)
			router.ServeHTTP(recorder, req)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
		})
	}
}

// D5：member 发起 MCP 配置写操作 → 创建审批并返回 202 pending_approval，不直接执行。
// 覆盖 5 个审批集合内写方法（spec D5：SetToolPolicy/Connect/Update/SetEditors/Delete）。
func TestMCPWriteConfigMemberGetsPendingApproval(t *testing.T) {
	gin.SetMode(gin.TestMode)
	approvals := &fakeApprovalRequests{}
	svc, _ := newMCPServiceForWriteTests(t)
	h := NewMCPHandler(svc, zap.NewNop()).
		WithApprovalService(approvals).
		WithRoleResolver(scriptedRoleResolver{role: "member"})
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	register := func(method, path string, hf gin.HandlerFunc) {
		r.Handle(method, path, withTenantAndUser("tenant-1", "member-1"), hf)
	}
	register(http.MethodPut, "/mcp/tool-policies/:serverId/:toolName", h.SetToolPolicy)
	register(http.MethodPost, "/mcp/servers", h.ConnectServer)
	register(http.MethodPut, "/mcp/servers/:id", h.UpdateServer)
	register(http.MethodPut, "/mcp/servers/:id/editors", h.SetMCPServerEditors)
	register(http.MethodDelete, "/mcp/servers/:id/config", h.DeleteServerConfig)

	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		wantSubject string
		wantTool    string
		wantOp      string
	}{
		{name: "set_tool_policy", method: http.MethodPut, path: "/mcp/tool-policies/srv/tool", body: `{"riskLevel":"write_reversible"}`,
			wantSubject: agentdomain.SubjectKindMCPPolicy, wantTool: "mcp.set_tool_policy", wantOp: "set_tool_policy"},
		{name: "connect_server", method: http.MethodPost, path: "/mcp/servers", body: `{"name":"svr","transport":"streamable-http","url":"http://localhost:9000","editors":["e1"]}`,
			wantSubject: agentdomain.SubjectKindMCPServer, wantTool: "mcp.connect_server", wantOp: "connect_server"},
		{name: "update_server", method: http.MethodPut, path: "/mcp/servers/srv-1", body: `{"name":"svr","transport":"streamable-http"}`,
			wantSubject: agentdomain.SubjectKindMCPServer, wantTool: "mcp.update_server", wantOp: "update_server"},
		{name: "set_editors", method: http.MethodPut, path: "/mcp/servers/srv-1/editors", body: `{"editorIds":["e1"]}`,
			wantSubject: agentdomain.SubjectKindMCPServer, wantTool: "mcp.set_editors", wantOp: "set_editors"},
		{name: "delete_server", method: http.MethodDelete, path: "/mcp/servers/srv-1/config", body: ``,
			wantSubject: agentdomain.SubjectKindMCPServer, wantTool: "mcp.delete_server", wantOp: "delete_server"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			approvals.called = 0
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusAccepted {
				t.Fatalf("expected 202 pending_approval, got status=%d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"status":"pending_approval"`) {
				t.Fatalf("missing pending_approval marker: %s", rec.Body.String())
			}
			if approvals.called != 1 {
				t.Fatalf("approval Request called %d times, want 1", approvals.called)
			}
			if approvals.subjectKind != tc.wantSubject {
				t.Fatalf("subject kind=%q, want %q", approvals.subjectKind, tc.wantSubject)
			}
			if approvals.toolName != tc.wantTool {
				t.Fatalf("tool name=%q, want %q", approvals.toolName, tc.wantTool)
			}
			if approvals.args["operation"] != tc.wantOp {
				t.Fatalf("operation=%v, want %s", approvals.args["operation"], tc.wantOp)
			}
		})
	}
}

// D5：admin 直接执行 SetToolPolicy，不创建审批；policy 落到 repo（与直接路径一致）。
func TestMCPSetToolPolicyAdminExecutesDirectly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	approvals := &fakeApprovalRequests{}
	svc, repo := newMCPServiceForWriteTests(t)
	h := NewMCPHandler(svc, zap.NewNop()).
		WithApprovalService(approvals).
		WithRoleResolver(scriptedRoleResolver{role: "admin"})
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.PUT("/mcp/tool-policies/:serverId/:toolName", withTenantAndUser("tenant-1", "admin-1"), h.SetToolPolicy)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/mcp/tool-policies/srv/tool", strings.NewReader(`{"riskLevel":"write_reversible"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	if approvals.called != 0 {
		t.Fatalf("approval Request called %d times, want 0 for admin", approvals.called)
	}
	if len(repo.upserts) != 1 {
		t.Fatalf("SetToolPolicy executed %d times, want 1", len(repo.upserts))
	}
	if repo.upserts[0].RiskLevel != mcpdomain.ToolRiskWriteReversible {
		t.Fatalf("risk level=%q, want write_reversible", repo.upserts[0].RiskLevel)
	}
}

// D5：connect 审批 args 携带完整 config 与 editors（敏感字段在审批内 AES 加密存储，
// 执行时由审批人权限重放；详情视图脱敏）。config 必须可反序列化为 gen 请求形态。
func TestMCPConnectServerMemberApprovalArgsCarryConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	approvals := &fakeApprovalRequests{}
	svc, _ := newMCPServiceForWriteTests(t)
	h := NewMCPHandler(svc, zap.NewNop()).
		WithApprovalService(approvals).
		WithRoleResolver(scriptedRoleResolver{role: "member"})
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/mcp/servers", withTenantAndUser("tenant-1", "member-1"), h.ConnectServer)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp/servers",
		strings.NewReader(`{"name":"svr","transport":"streamable-http","url":"http://localhost:9000","env":{"X-KEY":"secret"},"editors":["e1"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	raw, err := json.Marshal(approvals.args["config"])
	if err != nil {
		t.Fatalf("marshal config arg: %v", err)
	}
	var cfg gen.MCPServerConfigRequest
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal config arg: %v", err)
	}
	if cfg.Name != "svr" || cfg.Transport != "streamable-http" {
		t.Fatalf("config lost fields: name=%q transport=%q", cfg.Name, cfg.Transport)
	}
	editors, ok := approvals.args["editors"].([]string)
	if !ok || len(editors) != 1 || editors[0] != "e1" {
		t.Fatalf("editors arg=%v, want [e1]", approvals.args["editors"])
	}
}

// D5：注入 DB-backed resolver 时 currentRole 现查分流——admin 直接执行、member 走审批、
// 解析失败/未知角色 fail closed 403。JWT claim 不再参与分类。
func TestMCPCurrentRoleWithResolver(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		role       string
		err        error
		wantStatus int
		wantCalled int
	}{
		{name: "admin executes directly", role: "admin", wantStatus: http.StatusOK, wantCalled: 0},
		{name: "owner executes directly", role: "owner", wantStatus: http.StatusOK, wantCalled: 0},
		{name: "member creates approval", role: "member", wantStatus: http.StatusAccepted, wantCalled: 1},
		{name: "resolver error fails closed", err: errors.New("role lookup failed"), wantStatus: http.StatusForbidden, wantCalled: 0},
		{name: "unknown role fails closed", role: "viewer", wantStatus: http.StatusForbidden, wantCalled: 0},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			approvals := &fakeApprovalRequests{}
			svc, _ := newMCPServiceForWriteTests(t)
			h := NewMCPHandler(svc, zap.NewNop()).
				WithApprovalService(approvals).
				WithRoleResolver(scriptedRoleResolver{role: tc.role, err: tc.err})
			r := gin.New()
			r.Use(middleware.ErrorHandler(zap.NewNop()))
			r.PUT("/mcp/tool-policies/:serverId/:toolName", withTenantAndUser("tenant-1", "actor-1"), h.SetToolPolicy)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/mcp/tool-policies/srv/tool",
				strings.NewReader(`{"riskLevel":"write_reversible"}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if approvals.called != tc.wantCalled {
				t.Fatalf("approval Request called %d times, want %d", approvals.called, tc.wantCalled)
			}
		})
	}
}

// D5：非法 riskLevel 在 handler 层拒绝（400），不落入审批 payload——否则审批通过后
// 执行器校验失败会终态 unknown_outcome（不可重试），把合法审批烧掉。
func TestMCPSetToolPolicyInvalidRiskLevelRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	approvals := &fakeApprovalRequests{}
	svc, repo := newMCPServiceForWriteTests(t)
	h := NewMCPHandler(svc, zap.NewNop()).
		WithApprovalService(approvals).
		WithRoleResolver(scriptedRoleResolver{role: "member"})
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.PUT("/mcp/tool-policies/:serverId/:toolName", withTenantAndUser("tenant-1", "member-1"), h.SetToolPolicy)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/mcp/tool-policies/srv/tool",
		strings.NewReader(`{"riskLevel":"not_a_real_level"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	if approvals.called != 0 {
		t.Fatalf("approval Request called %d times, want 0", approvals.called)
	}
	if len(repo.upserts) != 0 {
		t.Fatalf("SetToolPolicy executed %d times, want 0", len(repo.upserts))
	}
}
