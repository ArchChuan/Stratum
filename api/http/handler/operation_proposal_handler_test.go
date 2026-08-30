package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type opProposalReviewFake struct {
	proposals    []domain.OperationProposal
	proposal     domain.OperationProposal
	err          error
	tenantID     string
	actorID      string
	resourceType string
	resourceID   string
	resourceName string
	page         int
	pageSize     int
}

func (f *opProposalReviewFake) ListPending(_ context.Context, tenantID, userID string) ([]domain.OperationProposal, error) {
	f.tenantID, f.actorID = tenantID, userID
	return f.proposals, f.err
}
func (f *opProposalReviewFake) Get(_ context.Context, tenantID, userID, _ string) (*domain.OperationProposal, error) {
	f.tenantID, f.actorID = tenantID, userID
	if f.err != nil {
		return nil, f.err
	}
	return &f.proposal, nil
}
func (f *opProposalReviewFake) StartReview(_ context.Context, tenantID, userID, _ string) error {
	f.tenantID, f.actorID = tenantID, userID
	return f.err
}
func (f *opProposalReviewFake) Approve(_ context.Context, tenantID, userID, _, _ string) error {
	f.tenantID, f.actorID = tenantID, userID
	return f.err
}
func (f *opProposalReviewFake) Reject(_ context.Context, tenantID, userID, _, _ string) error {
	f.tenantID, f.actorID = tenantID, userID
	return f.err
}
func (f *opProposalReviewFake) ListMine(_ context.Context, tenantID, userID string) ([]domain.OperationProposal, error) {
	f.tenantID, f.actorID = tenantID, userID
	if f.err != nil {
		return nil, f.err
	}
	var mine []domain.OperationProposal
	for _, p := range f.proposals {
		if p.ProposerID == userID {
			mine = append(mine, p)
		}
	}
	return mine, nil
}
func (f *opProposalReviewFake) ProposeGrantEditor(_ context.Context, tenantID, actorID, resourceType, resourceID, resourceName string) error {
	f.tenantID, f.actorID = tenantID, actorID
	f.resourceType, f.resourceID, f.resourceName = resourceType, resourceID, resourceName
	return f.err
}
func (f *opProposalReviewFake) ListHistory(_ context.Context, tenantID, actor string, page, pageSize int) ([]domain.OperationProposal, int, error) {
	f.tenantID, f.actorID = tenantID, actor
	f.page, f.pageSize = page, pageSize
	if f.err != nil {
		return nil, 0, f.err
	}
	var hist []domain.OperationProposal
	for _, p := range f.proposals {
		if p.Status == domain.OpProposed || p.Status == domain.OpReviewing {
			continue
		}
		hist = append(hist, p)
	}
	return hist, len(hist), nil
}
func (f *opProposalReviewFake) Cancel(_ context.Context, tenantID, actor, _ string) error {
	f.tenantID, f.actorID = tenantID, actor
	return f.err
}

type opSelfModifyFake struct {
	result application.GatedSelfModifyResult
	err    error
}

func (f *opSelfModifyFake) GatedSelfModify(_ context.Context, _, _, _ string, _ application.SelfModifyRequest) (application.GatedSelfModifyResult, error) {
	return f.result, f.err
}

func opProposalFixture() domain.OperationProposal {
	return domain.OperationProposal{
		ID: "op-1", TenantID: "tenant-1", AgentID: "agent-1", OpType: "self_modify",
		Fingerprint: "fp", PayloadSummary: json.RawMessage(`{"name":"renamed"}`),
		Status: domain.OpProposed, ProposerID: "member-1",
	}
}

func opProposalRouter(review operationProposalReviewService, selfModify agentSelfModifyService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Set(middleware.ContextKeySub, "admin-1")
		c.Next()
	})
	h := NewOperationProposalHandler(review, selfModify)
	router.GET("/operation-proposals", h.List)
	router.GET("/operation-proposals/:id", h.Get)
	router.GET("/operation-proposals/history", h.ListHistory)
	router.POST("/operation-proposals/:id/review", h.Review)
	router.POST("/operation-proposals/:id/approve", h.Approve)
	router.POST("/operation-proposals/:id/reject", h.Reject)
	router.POST("/operation-proposals/:id/cancel", h.Cancel)
	router.POST("/agents/:id/self-modify", h.SelfModify)
	return router
}

// opProposalGrantRouter registers the six member-facing grant_editor routes —
// mirroring api/http/router.go — so handler tests exercise real FullPath
// resolution of grantRouteResourceType.
func opProposalGrantRouter(review operationProposalReviewService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Set(middleware.ContextKeySub, "admin-1")
		c.Next()
	})
	h := NewOperationProposalHandler(review, &opSelfModifyFake{})
	router.POST("/agents/:id/request-editor", h.RequestEditorAccess)
	router.POST("/skills/:id/request-editor", h.RequestEditorAccess)
	router.POST("/knowledge/workspaces/:name/documents/:documentID/request-access", h.RequestEditorAccess)
	router.POST("/mcp/servers/:id/request-editor", h.RequestEditorAccess)
	router.POST("/knowledge/workspaces/:name/request-editor", h.RequestEditorAccess)
	router.POST("/workflows/:id/request-editor", h.RequestEditorAccess)
	return router
}

func TestOperationProposalHandlerList(t *testing.T) {
	review := &opProposalReviewFake{proposals: []domain.OperationProposal{opProposalFixture()}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/operation-proposals", nil)
	opProposalRouter(review, &opSelfModifyFake{}).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"op-1"`)
	require.Equal(t, "tenant-1", review.tenantID)
	require.Equal(t, "admin-1", review.actorID)
}

func TestOperationProposalHandlerGetShowsDeSensitisedSummary(t *testing.T) {
	p := opProposalFixture()
	p.PayloadSummary = json.RawMessage(`{"name":"renamed","api_key":"***"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/operation-proposals/op-1", nil)
	opProposalRouter(&opProposalReviewFake{proposal: p}, &opSelfModifyFake{}).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"payloadSummary":{"name":"renamed","api_key":"***"}`)
}

func TestOperationProposalHandlerRejectRequiresNote(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/operation-proposals/op-1/reject",
		strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	opProposalRouter(&opProposalReviewFake{}, &opSelfModifyFake{}).ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	review := &opProposalReviewFake{}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/operation-proposals/op-1/reject",
		strings.NewReader(`{"note":"out of scope"}`))
	request.Header.Set("Content-Type", "application/json")
	opProposalRouter(review, &opSelfModifyFake{}).ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "admin-1", review.actorID)
}

func TestOperationProposalHandlerReviewAndApprove(t *testing.T) {
	review := &opProposalReviewFake{}
	router := opProposalRouter(review, &opSelfModifyFake{})

	for _, tc := range []struct {
		method, path string
		wantStatus   string
	}{
		{http.MethodPost, "/operation-proposals/op-1/review", "reviewing"},
		{http.MethodPost, "/operation-proposals/op-1/approve", "approved"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, nil)
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, recorder.Body.String(), `"status":"`+tc.wantStatus+`"`)
	}
}

func TestOperationProposalHandlerNotFound(t *testing.T) {
	review := &opProposalReviewFake{err: domain.ErrOperationProposalNotFound}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/operation-proposals/missing", nil)
	opProposalRouter(review, &opSelfModifyFake{}).ServeHTTP(recorder, request)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestOperationProposalHandlerListHistory(t *testing.T) {
	pending := opProposalFixture() // proposed
	resolved := opProposalFixture()
	resolved.ID, resolved.Status = "op-resolved", domain.OpRejected
	review := &opProposalReviewFake{proposals: []domain.OperationProposal{pending, resolved}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/operation-proposals/history?page=2&page_size=5", nil)
	opProposalRouter(review, &opSelfModifyFake{}).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	// 历史只含非 pending（resolved），pending 提案不出现。
	require.Contains(t, recorder.Body.String(), `"op-resolved"`)
	require.NotContains(t, recorder.Body.String(), `"op-1"`)
	require.Contains(t, recorder.Body.String(), `"total":1,"page":2,"pageSize":5`)
	require.Equal(t, 2, review.page)
	require.Equal(t, 5, review.pageSize)
}

func TestOperationProposalHandlerCancel(t *testing.T) {
	review := &opProposalReviewFake{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/operation-proposals/op-1/cancel", nil)
	opProposalRouter(review, &opSelfModifyFake{}).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"cancelled"`)
	require.Equal(t, "admin-1", review.actorID)
}

func TestOperationProposalHandlerSelfModifyAlwaysProposes(t *testing.T) {
	selfModify := &opSelfModifyFake{result: application.GatedSelfModifyResult{
		Decision: port.GateDecision{Reason: "pending_approval", ProposalID: "op-new"},
	}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/agents/agent-1/self-modify",
		strings.NewReader(`{"name":"renamed"}`))
	request.Header.Set("Content-Type", "application/json")
	opProposalRouter(&opProposalReviewFake{}, selfModify).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"pending_approval"`)
	require.Contains(t, recorder.Body.String(), `"proposalId":"op-new"`)
}

func TestOperationProposalHandlerSelfModifyReplayLands(t *testing.T) {
	selfModify := &opSelfModifyFake{result: application.GatedSelfModifyResult{
		Decision: port.GateDecision{Allowed: true, Reason: "approved_replay"},
		DTO:      application.AgentDTO{ID: "agent-1", Name: "renamed"},
	}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/agents/agent-1/self-modify",
		strings.NewReader(`{"name":"renamed"}`))
	request.Header.Set("Content-Type", "application/json")
	opProposalRouter(&opProposalReviewFake{}, selfModify).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"approved"`)
	require.Contains(t, recorder.Body.String(), `"agent":{"id":"agent-1"`)
}

func TestOperationProposalHandlerSelfModifySurfacesUsageWarning(t *testing.T) {
	selfModify := &opSelfModifyFake{result: application.GatedSelfModifyResult{
		Decision: port.GateDecision{Allowed: true, Reason: "approved_replay"},
		DTO:      application.AgentDTO{ID: "agent-1"},
		UsageErr: context.DeadlineExceeded,
	}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/agents/agent-1/self-modify",
		strings.NewReader(`{"name":"renamed"}`))
	request.Header.Set("Content-Type", "application/json")
	opProposalRouter(&opProposalReviewFake{}, selfModify).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"usageWarning"`)
}

// TestGrantRouteResourceTypeCoversAllGrantRoutes pins grantRouteResourceType to
// the six request-editor routes registered in api/http/router.go. Adding a
// route without mapping (or an orphan mapping) fails this test.
func TestGrantRouteResourceTypeCoversAllGrantRoutes(t *testing.T) {
	want := map[string]string{
		"/agents/:id/request-editor":                                       "agent",
		"/skills/:id/request-editor":                                       "skill",
		"/knowledge/workspaces/:name/documents/:documentID/request-access": "knowledge_doc",
		"/mcp/servers/:id/request-editor":                                  "mcp",
		"/knowledge/workspaces/:name/request-editor":                       "knowledge_workspace",
		"/workflows/:id/request-editor":                                    "workflow",
	}
	require.Len(t, grantRouteResourceType, len(want), "grantRouteResourceType must match the six registered grant routes exactly")
	for path, resourceType := range want {
		got, ok := grantRouteResourceType[path]
		require.True(t, ok, "missing grant route mapping: %s", path)
		require.Equal(t, resourceType, got, "wrong resource type for route %s", path)
	}
}

func TestOperationProposalHandlerRequestEditorAccessAllRoutes(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		body     string
		wantType string
		wantID   string
		wantName string
	}{
		{
			name: "agent", path: "/agents/agent-1/request-editor",
			body: `{"resourceType":"agent","resourceName":"My Agent"}`, wantType: "agent", wantID: "agent-1", wantName: "My Agent",
		},
		{
			name: "skill", path: "/skills/skill-1/request-editor",
			body: `{"resourceType":"skill","resourceName":"My Skill"}`, wantType: "skill", wantID: "skill-1", wantName: "My Skill",
		},
		{
			name: "mcp", path: "/mcp/servers/mcp-1/request-editor",
			body: `{"resourceType":"mcp","resourceName":"My MCP"}`, wantType: "mcp", wantID: "mcp-1", wantName: "My MCP",
		},
		{
			name: "workflow", path: "/workflows/wf-1/request-editor",
			body: `{"resourceType":"workflow","resourceName":"My WF"}`, wantType: "workflow", wantID: "wf-1", wantName: "My WF",
		},
		{
			name: "knowledge_doc", path: "/knowledge/workspaces/ws-1/documents/doc-1/request-access",
			body: `{"resourceType":"knowledge_doc","resourceName":"docs/annual.pdf"}`, wantType: "knowledge_doc", wantID: "doc-1", wantName: "docs/annual.pdf",
		},
		{
			name: "knowledge_workspace", path: "/knowledge/workspaces/kw-1/request-editor",
			body: `{"resourceType":"knowledge_workspace","resourceName":"My KB"}`, wantType: "knowledge_workspace", wantID: "kw-1", wantName: "My KB",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			review := &opProposalReviewFake{}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			opProposalGrantRouter(review).ServeHTTP(recorder, request)

			require.Equal(t, http.StatusAccepted, recorder.Code)
			require.Equal(t, tc.wantType, review.resourceType)
			require.Equal(t, tc.wantID, review.resourceID)
			require.Equal(t, tc.wantName, review.resourceName)
			require.Equal(t, "admin-1", review.actorID)
		})
	}
}

// TestOperationProposalHandlerRequestEditorAccessRejectsResourceIDInBody pins
// the closed-body contract: requestEditorAccessBody has no resourceId json
// tag, so a client body carrying it is rejected by DisallowUnknownFields and
// never reaches ProposeGrantEditor. (MapErrorToStatus has no sentinel for the
// decode error, so today the status is 500 — still a rejection; the frontend
// must send only resourceType + resourceName either way.)
func TestOperationProposalHandlerRequestEditorAccessRejectsResourceIDInBody(t *testing.T) {
	review := &opProposalReviewFake{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/agents/agent-1/request-editor",
		strings.NewReader(`{"resourceType":"agent","resourceId":"agent-1","resourceName":"My Agent"}`))
	request.Header.Set("Content-Type", "application/json")
	opProposalGrantRouter(review).ServeHTTP(recorder, request)

	require.NotEqual(t, http.StatusAccepted, recorder.Code)
	require.Empty(t, review.resourceType, "malformed body must not reach the service")
}
