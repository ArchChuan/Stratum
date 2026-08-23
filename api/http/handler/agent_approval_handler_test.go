package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// toolApprovalRepoFake 覆盖 ToolApprovalRepo 全接口，记录执行副作用供 handler 测试断言。
type toolApprovalRepoFake struct {
	row           agentdomain.ToolApproval
	listHistory   []agentdomain.ToolApproval
	total         int
	getErr        error
	lastAssignee  string
	markExecuted  int
	claimExecuted int
}

func (f *toolApprovalRepoFake) Create(_ context.Context, _ string, row agentdomain.ToolApproval) (string, error) {
	f.row = row
	return "approval-1", nil
}
func (f *toolApprovalRepoFake) Get(_ context.Context, _, _ string) (agentdomain.ToolApproval, error) {
	return f.row, f.getErr
}
func (*toolApprovalRepoFake) Decide(context.Context, string, string, string, string, string, time.Time) error {
	return nil
}
func (f *toolApprovalRepoFake) ClaimExecution(_ context.Context, _, _ string) error {
	f.claimExecuted++
	return nil
}
func (*toolApprovalRepoFake) ReleaseExecution(context.Context, string, string) error { return nil }
func (*toolApprovalRepoFake) MarkOutcomeUnknown(context.Context, string, string) error {
	return nil
}
func (f *toolApprovalRepoFake) MarkExecuted(_ context.Context, _, _ string) error {
	f.markExecuted++
	return nil
}
func (*toolApprovalRepoFake) ListPending(context.Context, string, string) ([]agentdomain.ToolApproval, error) {
	return nil, nil
}
func (f *toolApprovalRepoFake) ListHistory(_ context.Context, _ string, _ string, _, _ int) ([]agentdomain.ToolApproval, int, error) {
	return f.listHistory, f.total, nil
}
func (*toolApprovalRepoFake) Invalidate(context.Context, string, string, string) error { return nil }
func (*toolApprovalRepoFake) Void(context.Context, string, string, string) error       { return nil }
func (f *toolApprovalRepoFake) UpdateAssignee(_ context.Context, _, _, assignee string) error {
	f.lastAssignee = assignee
	return nil
}
func (*toolApprovalRepoFake) CascadeByConversation(context.Context, string, string) error {
	return nil
}

// fakeApprovalActionExecutor 记录调用并返回固定输出，验证 executor 与 handler 的装配。
type fakeApprovalActionExecutor struct {
	out    map[string]any
	err    error
	called bool
}

func (e *fakeApprovalActionExecutor) ExecuteApprovalAction(_ context.Context, _ port.ApprovalActionRequest) (map[string]any, error) {
	e.called = true
	return e.out, e.err
}

// newApprovalTestHandler 装配带审批 service 的 AgentHandler。resolverRole 决定
// service 内角色现查结果（member 场景验证 fail closed）。
func newApprovalTestHandler(t *testing.T, resolverRole string, executor port.ApprovalActionExecutor) (*AgentHandler, *toolApprovalRepoFake) {
	t.Helper()
	repo := &toolApprovalRepoFake{}
	approvalSvc := agentapp.NewToolApprovalService(repo, nil, crypto.DeriveAESKey("handler-test-key"))
	approvalSvc.SetTenantRoleResolver(fixedTenantRole{role: resolverRole})
	deps := agentapp.AgentServiceDeps{ApprovalService: approvalSvc, Logger: zap.NewNop()}
	h := NewAgentHandler(agentapp.NewAgentService(deps), zap.NewNop())
	if executor != nil {
		h = h.WithActionExecutor(executor)
	}
	return h, repo
}

// approvalRoutes 注册 D4 审批工作台 4 个端点（与 router.go agents 组一致）。
func approvalRoutes(h *AgentHandler, auth ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	for _, a := range auth {
		r.Use(a)
	}
	g := r.Group("/tool-approvals")
	g.GET("/history", h.ListApprovalHistory)
	g.GET("/:approvalID", h.GetApprovalDetail)
	g.POST("/:approvalID/execute", h.ExecuteApproval)
	g.PUT("/:approvalID/assignee", h.SetApprovalAssignee)
	return r
}

// withApprovalContext 注入 tenant + user + role 三种上下文（ExecuteApproval 读 role claim，
// 其余端点走 service 内 resolver 现查）。
func withApprovalContext(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "t1"))
		c.Set(middleware.ContextKeySub, "u1")
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
}

func doApprovalReq(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
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

// makeApprovalRow 通过 Request 生成带加密 payload + 匹配 digest 的审批行，
// 再标记为 approved 未过期——复现服务层单次消费成功路径的前置状态。
func makeApprovalRow(t *testing.T, repo *toolApprovalRepoFake) {
	t.Helper()
	svc := agentapp.NewToolApprovalService(repo, nil, crypto.DeriveAESKey("handler-test-key"))
	if _, err := svc.Request(context.Background(), agentapp.ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "u1",
		ToolCallID: "call-1", ServerID: "evaluation", ToolName: "evaluation_action",
		RiskLevel: port.ToolRiskUnclassified, SubjectKind: agentdomain.SubjectKindEvaluationAction,
		PolicyVersion: "action-v1", Arguments: map[string]any{"operation": "pause_experiment", "experimentID": "exp-1"},
	}); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	repo.row.ID = "approval-1"
	repo.row.Status = "approved"
	repo.row.ExpiresAt = time.Now().Add(time.Minute)
}

func TestAgentHandlerListApprovalHistory(t *testing.T) {
	h, repo := newApprovalTestHandler(t, "admin", nil)
	repo.listHistory = []agentdomain.ToolApproval{{ID: "approval-1", Status: "executed"}}
	repo.total = 5

	w := doApprovalReq(t, approvalRoutes(h, withApprovalContext("admin")),
		http.MethodGet, "/tool-approvals/history?page=2&page_size=10", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"approvals":[`)
	require.Contains(t, w.Body.String(), `"approval-1"`)
	require.Contains(t, w.Body.String(), `"total":5`)
	require.Contains(t, w.Body.String(), `"page":2`)
	require.Contains(t, w.Body.String(), `"page_size":10`)

	// member 角色（M5/D4 放宽）：service 内 resolver 现查后仅返回本人发起的，200 而非 403。
	hMember, repoMember := newApprovalTestHandler(t, "member", nil)
	repoMember.listHistory = []agentdomain.ToolApproval{{ID: "approval-m", Status: "executed"}}
	repoMember.total = 1
	w = doApprovalReq(t, approvalRoutes(hMember, withApprovalContext("member")),
		http.MethodGet, "/tool-approvals/history", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"approval-m"`)
}

func TestAgentHandlerGetApprovalDetail(t *testing.T) {
	h, repo := newApprovalTestHandler(t, "admin", nil)
	makeApprovalRow(t, repo)

	w := doApprovalReq(t, approvalRoutes(h, withApprovalContext("admin")),
		http.MethodGet, "/tool-approvals/approval-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"id":"approval-1"`)
	require.Contains(t, w.Body.String(), `"subject_kind":"evaluation_action"`)
	// payload 解密脱敏后下发（不含 EncryptedPayload 原文）。
	require.Contains(t, w.Body.String(), `"operation":"pause_experiment"`)
	require.NotContains(t, w.Body.String(), "EncryptedPayload")

	// member 归属自己发起的审批（makeApprovalRow 的 row.UserID="u1"==actor）：可看详情（M5/D4），200。
	hMemberOwned, repoMember := newApprovalTestHandler(t, "member", nil)
	makeApprovalRow(t, repoMember)
	w = doApprovalReq(t, approvalRoutes(hMemberOwned, withApprovalContext("member")),
		http.MethodGet, "/tool-approvals/approval-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"id":"approval-1"`)

	// member 非归属审批（row.UserID 改为他人）：统一 404，关闭存在性 oracle。
	repoMember.row.UserID = "other-user"
	w = doApprovalReq(t, approvalRoutes(hMemberOwned, withApprovalContext("member")),
		http.MethodGet, "/tool-approvals/approval-1", "")
	require.Equal(t, http.StatusNotFound, w.Code)

	// 缺 tenant 上下文 → 401。
	w = doApprovalReq(t, approvalRoutes(h),
		http.MethodGet, "/tool-approvals/approval-1", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAgentHandlerExecuteApproval(t *testing.T) {
	// 即使 JWT role claim 声称 admin（72h 陈旧窗口），service resolver 现查 member
	// 仍拒绝——live role gate 单事实源，fail closed。
	t.Run("member denied despite stale admin claim", func(t *testing.T) {
		h, _ := newApprovalTestHandler(t, "member", &fakeApprovalActionExecutor{out: map[string]any{"status": "promoted"}})
		w := doApprovalReq(t, approvalRoutes(h, withApprovalContext("admin")),
			http.MethodPost, "/tool-approvals/approval-1/execute", "")
		require.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("executor not configured fails closed", func(t *testing.T) {
		h, _ := newApprovalTestHandler(t, "admin", nil)
		w := doApprovalReq(t, approvalRoutes(h, withApprovalContext("admin")),
			http.MethodPost, "/tool-approvals/approval-1/execute", "")
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("executes approved action once", func(t *testing.T) {
		h, repo := newApprovalTestHandler(t, "admin", &fakeApprovalActionExecutor{out: map[string]any{"status": "promoted"}})
		makeApprovalRow(t, repo)

		w := doApprovalReq(t, approvalRoutes(h, withApprovalContext("admin")),
			http.MethodPost, "/tool-approvals/approval-1/execute", "")
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"status":"executed"`)
		require.Contains(t, w.Body.String(), `"output":{"status":"promoted"}`)
		// 单次消费：claim 一次 + mark executed 一次。
		require.Equal(t, 1, repo.claimExecuted)
		require.Equal(t, 1, repo.markExecuted)
	})

	t.Run("missing tenant unauthorized", func(t *testing.T) {
		h, _ := newApprovalTestHandler(t, "admin", &fakeApprovalActionExecutor{})
		w := doApprovalReq(t, approvalRoutes(h),
			http.MethodPost, "/tool-approvals/approval-1/execute", "")
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestAgentHandlerSetApprovalAssignee(t *testing.T) {
	t.Run("admin updates assignee", func(t *testing.T) {
		h, repo := newApprovalTestHandler(t, "admin", nil)
		makeApprovalRow(t, repo)
		w := doApprovalReq(t, approvalRoutes(h, withApprovalContext("admin")),
			http.MethodPut, "/tool-approvals/approval-1/assignee", `{"assignedApprover":"u2"}`)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"status":"updated"`)
		require.Equal(t, "u2", repo.lastAssignee)
	})

	t.Run("missing body binding error", func(t *testing.T) {
		h, _ := newApprovalTestHandler(t, "admin", nil)
		w := doApprovalReq(t, approvalRoutes(h, withApprovalContext("admin")),
			http.MethodPut, "/tool-approvals/approval-1/assignee", `{}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("member denied", func(t *testing.T) {
		h, _ := newApprovalTestHandler(t, "member", nil)
		w := doApprovalReq(t, approvalRoutes(h, withApprovalContext("member")),
			http.MethodPut, "/tool-approvals/approval-1/assignee", `{"assignedApprover":"u2"}`)
		require.Equal(t, http.StatusForbidden, w.Code)
	})
}
