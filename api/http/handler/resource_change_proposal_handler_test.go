package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestResourceChangeProposalHandlerGetReturnsSafeReview(t *testing.T) {
	service := &proposalHandlerServiceFake{proposal: proposalHandlerFixture()}
	router := proposalHandlerRouter(service, true)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/resource-change-proposals/proposal-1", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret")
	require.Contains(t, recorder.Body.String(), `"events":[]`)
	require.Equal(t, "tenant-1", service.tenantID)
	require.Equal(t, "admin-1", service.actorID)
}

func TestResourceChangeProposalHandlerRejectsUnknownPatchFields(t *testing.T) {
	router := proposalHandlerRouter(&proposalHandlerServiceFake{proposal: proposalHandlerFixture()}, true)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/resource-change-proposals/proposal-1",
		strings.NewReader(`{"payload":{"name":"agent"},"status":"applied"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestResourceChangeProposalHandlerRequiresTenantIdentity(t *testing.T) {
	router := proposalHandlerRouter(&proposalHandlerServiceFake{}, false)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/resource-change-proposals/proposal-1/confirm", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestResourceChangeProposalHandlerUnknownOutcomeIsConflict(t *testing.T) {
	service := &proposalHandlerServiceFake{err: domain.ErrProposalUnknownOutcome}
	router := proposalHandlerRouter(service, true)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/resource-change-proposals/proposal-1/confirm", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.JSONEq(t, `{"error":"proposal outcome unknown"}`, recorder.Body.String())
}

func proposalHandlerRouter(service resourceChangeProposalService, identity bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	if identity {
		router.Use(func(c *gin.Context) {
			c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
			c.Set(middleware.ContextKeySub, "admin-1")
			c.Next()
		})
	}
	h := NewResourceChangeProposalHandler(service)
	router.GET("/resource-change-proposals/:id", h.Get)
	router.PATCH("/resource-change-proposals/:id", h.Update)
	router.POST("/resource-change-proposals/:id/cancel", h.Cancel)
	router.POST("/resource-change-proposals/:id/confirm", h.Confirm)
	return router
}

func proposalHandlerFixture() domain.ResourceChangeProposal {
	now := time.Now().UTC()
	return domain.ResourceChangeProposal{
		ID: "proposal-1", TenantID: "tenant-1", ProposerID: "admin-1",
		ResourceKind: domain.ResourceAgent, Operation: domain.OperationCreate,
		Payload: json.RawMessage(`{"name":"agent"}`), Status: domain.StatusReadyForReview,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
}

type proposalHandlerServiceFake struct {
	proposal          domain.ResourceChangeProposal
	err               error
	tenantID, actorID string
}

func (f *proposalHandlerServiceFake) Get(_ context.Context, tenantID, actorID, _ string) (domain.ResourceChangeProposal, error) {
	f.tenantID, f.actorID = tenantID, actorID
	return f.proposal, f.err
}
func (f *proposalHandlerServiceFake) ListEvents(context.Context, string, string, string) ([]domain.ProposalEvent, error) {
	return nil, f.err
}
func (f *proposalHandlerServiceFake) UpdateDraft(_ context.Context, _ agentapp.UpdateProposalInput) (domain.ResourceChangeProposal, error) {
	return f.proposal, f.err
}
func (f *proposalHandlerServiceFake) Cancel(context.Context, string, string, string) error {
	return f.err
}
func (f *proposalHandlerServiceFake) ConfirmAndApply(context.Context, string, string, string) (domain.ResourceChangeProposal, error) {
	return f.proposal, f.err
}
