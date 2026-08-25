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
	proposals []domain.OperationProposal
	proposal  domain.OperationProposal
	err       error
	tenantID  string
	actorID   string
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
func (f *opProposalReviewFake) ProposeGrantEditor(_ context.Context, tenantID, actorID, _, _, _ string) error {
	f.tenantID, f.actorID = tenantID, actorID
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
	router.POST("/operation-proposals/:id/review", h.Review)
	router.POST("/operation-proposals/:id/approve", h.Approve)
	router.POST("/operation-proposals/:id/reject", h.Reject)
	router.POST("/agents/:id/self-modify", h.SelfModify)
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
