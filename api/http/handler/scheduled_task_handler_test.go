package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/middleware"
	schedapp "github.com/byteBuilderX/stratum/internal/scheduler/application"
	scheddomain "github.com/byteBuilderX/stratum/internal/scheduler/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

type schedulerServiceFake struct {
	task     scheddomain.ScheduledTask
	tasks    []scheddomain.ScheduledTask
	total    int
	err      error
	tenantID string
	actor    schedapp.Actor
	lastID   string
	lastPage int
}

var _ schedulerService = (*schedulerServiceFake)(nil)

func (f *schedulerServiceFake) Create(_ context.Context, tenantID string, _ schedapp.CreateCommand, actor schedapp.Actor) (*scheddomain.ScheduledTask, error) {
	f.tenantID, f.actor = tenantID, actor
	if f.err != nil {
		return nil, f.err
	}
	return &f.task, nil
}
func (f *schedulerServiceFake) Update(_ context.Context, tenantID, id string, _ schedapp.UpdateCommand, actor schedapp.Actor) (*scheddomain.ScheduledTask, error) {
	f.tenantID, f.actor, f.lastID = tenantID, actor, id
	if f.err != nil {
		return nil, f.err
	}
	return &f.task, nil
}
func (f *schedulerServiceFake) Delete(_ context.Context, tenantID, id string, actor schedapp.Actor) error {
	f.tenantID, f.actor, f.lastID = tenantID, actor, id
	return f.err
}
func (f *schedulerServiceFake) SetEnabled(_ context.Context, tenantID, id string, _ bool, actor schedapp.Actor) error {
	f.tenantID, f.actor, f.lastID = tenantID, actor, id
	return f.err
}
func (f *schedulerServiceFake) Get(_ context.Context, tenantID, id string) (*scheddomain.ScheduledTask, error) {
	f.tenantID, f.lastID = tenantID, id
	if f.err != nil {
		return nil, f.err
	}
	return &f.task, nil
}
func (f *schedulerServiceFake) List(_ context.Context, tenantID string, page, pageSize int) ([]scheddomain.ScheduledTask, int, error) {
	f.tenantID, f.lastPage = tenantID, page
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.tasks, f.total, nil
}

func schedulerTaskFixture() scheddomain.ScheduledTask {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	return scheddomain.ScheduledTask{
		ID: "task-1", Name: "nightly", WorkflowID: "wf-1", VersionID: "ver-1",
		InputTemplate: map[string]any{"task": "summarize"}, CronExpr: "0 9 * * *",
		Enabled: true, NextFireAt: now.Add(time.Hour), LastRunStatus: scheddomain.LastRunOK,
		CreatedBy: "admin-1", CreatedAt: now, UpdatedAt: now,
	}
}

func schedulerRouter(svc schedulerService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Set(middleware.ContextKeySub, "admin-1")
		c.Set(middleware.ContextKeyRole, "admin")
		c.Next()
	})
	h := NewScheduledTaskHandler(svc)
	router.GET("/scheduled-tasks", h.List)
	router.POST("/scheduled-tasks", h.Create)
	router.GET("/scheduled-tasks/:id", h.Get)
	router.PUT("/scheduled-tasks/:id", h.Update)
	router.DELETE("/scheduled-tasks/:id", h.Delete)
	router.PATCH("/scheduled-tasks/:id/enabled", h.SetEnabled)
	return router
}

func TestScheduledTaskHandlerCreate(t *testing.T) {
	svc := &schedulerServiceFake{task: schedulerTaskFixture()}
	router := schedulerRouter(svc)

	body := `{"name":"nightly","workflowId":"wf-1","versionId":"ver-1","inputTemplate":{"task":"summarize"},"cronExpr":"0 9 * * *"}`
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/scheduled-tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusCreated, resp.Code)
	require.Equal(t, "tenant-1", svc.tenantID)
	require.Equal(t, "admin-1", svc.actor.UserID)
	require.Equal(t, "admin", svc.actor.Role)

	var out struct {
		ID         string         `json:"id"`
		Name       string         `json:"name"`
		CronExpr   string         `json:"cronExpr"`
		Enabled    bool           `json:"enabled"`
		Input      map[string]any `json:"inputTemplate"`
		CreatedBy  string         `json:"createdBy"`
		NextFireAt string         `json:"nextFireAt"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Equal(t, "task-1", out.ID)
	require.Equal(t, "nightly", out.Name)
	require.Equal(t, "0 9 * * *", out.CronExpr)
	require.True(t, out.Enabled)
	require.Equal(t, map[string]any{"task": "summarize"}, out.Input)
	require.Equal(t, "admin-1", out.CreatedBy)
	require.Equal(t, "2026-08-09T13:00:00Z", out.NextFireAt)
}

func TestScheduledTaskHandlerCreateRejectsMalformedBody(t *testing.T) {
	svc := &schedulerServiceFake{}
	router := schedulerRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/scheduled-tasks", strings.NewReader(`{"name":"only name"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Empty(t, svc.tenantID, "service must not be called on malformed input")
}

func TestScheduledTaskHandlerUpdate(t *testing.T) {
	svc := &schedulerServiceFake{task: schedulerTaskFixture()}
	router := schedulerRouter(svc)

	body := `{"name":"renamed","workflowId":"wf-1","versionId":"ver-1","cronExpr":"0 8 * * *"}`
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/scheduled-tasks/task-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	require.Equal(t, "task-1", svc.lastID)
	require.Equal(t, "admin-1", svc.actor.UserID)
	var out struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Equal(t, "nightly", out.Name)
}

func TestScheduledTaskHandlerDelete(t *testing.T) {
	svc := &schedulerServiceFake{}
	router := schedulerRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/scheduled-tasks/task-1", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	require.Equal(t, "task-1", svc.lastID)
	require.Equal(t, "admin-1", svc.actor.UserID)
}

func TestScheduledTaskHandlerDeleteNotFound(t *testing.T) {
	svc := &schedulerServiceFake{err: scheddomain.ErrScheduledTaskNotFound}
	router := schedulerRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/scheduled-tasks/missing", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusNotFound, resp.Code)
}

func TestScheduledTaskHandlerSetEnabled(t *testing.T) {
	svc := &schedulerServiceFake{}
	router := schedulerRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/scheduled-tasks/task-1/enabled", strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	require.Equal(t, "task-1", svc.lastID)
	var out struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Equal(t, "scheduled task updated successfully", out.Message)
}

func TestScheduledTaskHandlerGet(t *testing.T) {
	svc := &schedulerServiceFake{task: schedulerTaskFixture()}
	router := schedulerRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/scheduled-tasks/task-1", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	require.Equal(t, "task-1", svc.lastID)
	var out struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Equal(t, "task-1", out.ID)
	require.True(t, out.Enabled)
}

func TestScheduledTaskHandlerGetForbiddenPropagates(t *testing.T) {
	svc := &schedulerServiceFake{err: scheddomain.ErrScheduledTaskForbidden}
	router := schedulerRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/scheduled-tasks/task-1", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusForbidden, resp.Code)
}

func TestScheduledTaskHandlerList(t *testing.T) {
	svc := &schedulerServiceFake{
		tasks: []scheddomain.ScheduledTask{schedulerTaskFixture()},
		total: 1,
	}
	router := schedulerRouter(svc)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/scheduled-tasks?page=2&page_size=10", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	require.Equal(t, 2, svc.lastPage)

	var out struct {
		Tasks    []json.RawMessage `json:"tasks"`
		Total    int               `json:"total"`
		Page     int               `json:"page"`
		PageSize int               `json:"pageSize"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Len(t, out.Tasks, 1)
	require.Equal(t, 1, out.Total)
	require.Equal(t, 2, out.Page)
	require.Equal(t, 10, out.PageSize)
}

func TestScheduledTaskHandlerMissingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	h := NewScheduledTaskHandler(&schedulerServiceFake{})
	router.GET("/scheduled-tasks", h.List)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/scheduled-tasks", nil)
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusUnauthorized, resp.Code)
}
