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
	collabapp "github.com/byteBuilderX/stratum/internal/collab/application"
	collabdomain "github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type collabServiceFake struct {
	collab       collabdomain.Collaboration
	collabs      []collabdomain.Collaboration
	steps        []collabdomain.TaskStep
	err          error
	tenantID     string
	actor        collabapp.Actor
	lastLimit    int
	lastOffset   int
	participants []string
	strategy     collabdomain.CollabStrategy
}

var _ collaborationService = (*collabServiceFake)(nil)

func (f *collabServiceFake) Create(_ context.Context, tenantID string, actor collabapp.Actor, _ string, strategy collabdomain.CollabStrategy, participants []string) (*collabdomain.Collaboration, error) {
	f.tenantID, f.actor = tenantID, actor
	f.strategy, f.participants = strategy, participants
	if f.err != nil {
		return nil, f.err
	}
	return &f.collab, nil
}
func (f *collabServiceFake) Get(_ context.Context, tenantID, _ string, actor collabapp.Actor) (*collabdomain.Collaboration, error) {
	f.tenantID, f.actor = tenantID, actor
	if f.err != nil {
		return nil, f.err
	}
	return &f.collab, nil
}
func (f *collabServiceFake) List(_ context.Context, tenantID string, actor collabapp.Actor, limit, offset int) ([]collabdomain.Collaboration, error) {
	f.tenantID, f.actor = tenantID, actor
	f.lastLimit, f.lastOffset = limit, offset
	if f.err != nil {
		return nil, f.err
	}
	return f.collabs, nil
}
func (f *collabServiceFake) ReadyTasks(_ context.Context, tenantID, _ string, actor collabapp.Actor) ([]collabdomain.TaskStep, error) {
	f.tenantID, f.actor = tenantID, actor
	if f.err != nil {
		return nil, f.err
	}
	return f.steps, nil
}
func (f *collabServiceFake) Start(_ context.Context, tenantID, _ string, actor collabapp.Actor) (*collabdomain.Collaboration, error) {
	f.tenantID, f.actor = tenantID, actor
	if f.err != nil {
		return nil, f.err
	}
	return &f.collab, nil
}
func (f *collabServiceFake) Cancel(_ context.Context, tenantID, _ string, actor collabapp.Actor) error {
	f.tenantID, f.actor = tenantID, actor
	return f.err
}

func collabFixture() collabdomain.Collaboration {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	return collabdomain.Collaboration{
		ID: "collab-1", TenantID: "tenant-1", TaskDescription: "review release",
		Strategy: collabdomain.CollabSequential, Status: collabdomain.CollabCreated,
		CreatedBy: "member-1", Participants: []string{"agent-1", "agent-2"},
		CreatedAt: now,
	}
}

func collabStepFixture() collabdomain.TaskStep {
	return collabdomain.TaskStep{
		ID: "step-1", PlanID: "collab-1", AgentID: "agent-1",
		Dependencies: []string{}, Status: collabdomain.TaskPending,
		Input: map[string]any{"query": "review release", "step_index": 0, "total_steps": 2},
	}
}

func collabRouter(svc collaborationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Set(middleware.ContextKeySub, "member-1")
		c.Set(middleware.ContextKeyRole, "member")
		c.Next()
	})
	h := NewCollaborationHandler(svc)
	router.GET("/collaborations", h.List)
	router.POST("/collaborations", h.Create)
	router.GET("/collaborations/:id", h.Get)
	router.POST("/collaborations/:id/start", h.Start)
	router.POST("/collaborations/:id/cancel", h.Cancel)
	return router
}

func TestCollabHandlerCreate(t *testing.T) {
	svc := &collabServiceFake{collab: collabFixture()}
	router := collabRouter(svc)

	body := `{"task_description":"review release","strategy":"sequential","participants":["agent-1","agent-2"]}`
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/collaborations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusCreated, resp.Code)
	require.Equal(t, "member-1", svc.actor.UserID)
	require.Equal(t, "member", svc.actor.Role)
	require.Equal(t, "tenant-1", svc.tenantID)
	require.Equal(t, collabdomain.CollabSequential, svc.strategy)
	require.Equal(t, []string{"agent-1", "agent-2"}, svc.participants)

	var out struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		CreatedBy string `json:"createdBy"`
		Strategy  string `json:"strategy"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Equal(t, "collab-1", out.ID)
	require.Equal(t, "created", out.Status)
	require.Equal(t, "member-1", out.CreatedBy)
}

func TestCollabHandlerCreateRejectsMalformedBody(t *testing.T) {
	svc := &collabServiceFake{}
	router := collabRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/collaborations", strings.NewReader(`{"task_description":"only desc"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Empty(t, svc.tenantID, "service must not be called on malformed input")
}

func TestCollabHandlerList(t *testing.T) {
	svc := &collabServiceFake{collabs: []collabdomain.Collaboration{collabFixture()}}
	router := collabRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/collaborations?limit=10&offset=5", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	require.Equal(t, 10, svc.lastLimit)
	require.Equal(t, 5, svc.lastOffset)
	require.Equal(t, "member-1", svc.actor.UserID)

	var out struct {
		Collaborations []json.RawMessage `json:"collaborations"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Len(t, out.Collaborations, 1)
}

func TestCollabHandlerListRejectsInvalidLimit(t *testing.T) {
	svc := &collabServiceFake{}
	router := collabRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/collaborations?limit=abc", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Empty(t, svc.tenantID, "service must not be called on malformed pagination")
}

func TestCollabHandlerGetReturnsSteps(t *testing.T) {
	svc := &collabServiceFake{collab: collabFixture(), steps: []collabdomain.TaskStep{collabStepFixture()}}
	router := collabRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/collaborations/collab-1", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	var out struct {
		Collaboration json.RawMessage   `json:"collaboration"`
		Steps         []json.RawMessage `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.NotEmpty(t, out.Collaboration)
	require.Len(t, out.Steps, 1)
}

func TestCollabHandlerStartNotFoundHidesEnumeration(t *testing.T) {
	svc := &collabServiceFake{err: collabdomain.ErrCollabNotFound}
	router := collabRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/collaborations/other-1/start", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusNotFound, resp.Code)
}

func TestCollabHandlerCancel(t *testing.T) {
	svc := &collabServiceFake{}
	router := collabRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/collaborations/collab-1/cancel", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	require.Equal(t, "member-1", svc.actor.UserID)
	var out struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Equal(t, "canceled", out.Status)
}

func TestCollabHandlerMissingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	h := NewCollaborationHandler(&collabServiceFake{})
	router.GET("/collaborations", h.List)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/collaborations", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusUnauthorized, resp.Code)
}
