package handler_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	workflowapp "github.com/byteBuilderX/stratum/internal/workflow/application"
	workflowdomain "github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type streamingWorkflowRunFake struct {
	mu     sync.Mutex
	status workflowdomain.RunStatus
	events []workflowdomain.Event
}

func (*streamingWorkflowRunFake) StartAsync(context.Context, string, workflowapp.StartRunCommand) (*workflowdomain.Run, bool, error) {
	return nil, false, nil
}
func (f *streamingWorkflowRunFake) Get(context.Context, string, string) (*workflowdomain.Run, []workflowdomain.NodeAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &workflowdomain.Run{ID: "run-1", Status: f.status}, nil, nil
}
func (f *streamingWorkflowRunFake) Events(_ context.Context, _, _ string, after int64, limit int) ([]workflowdomain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []workflowdomain.Event
	for _, event := range f.events {
		if event.SequenceNo > after {
			out = append(out, event)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

type workflowDefinitionFake struct{ created *workflowdomain.Definition }

func (f *workflowDefinitionFake) Create(_ context.Context, _ string, cmd workflowapp.CreateDefinitionCommand) (*workflowdomain.Definition, error) {
	f.created = &workflowdomain.Definition{ID: "wf-1", Name: cmd.Name, Revision: 1, Spec: cmd.Spec}
	return f.created, nil
}
func (*workflowDefinitionFake) Update(context.Context, string, string, workflowapp.UpdateDefinitionCommand) (*workflowdomain.Definition, error) {
	return nil, nil
}
func (*workflowDefinitionFake) Validate(context.Context, string, string) error { return nil }
func (*workflowDefinitionFake) Publish(context.Context, string, string) (*workflowdomain.Version, error) {
	return nil, nil
}
func (f *workflowDefinitionFake) Get(context.Context, string, string) (*workflowdomain.Definition, error) {
	return f.created, nil
}
func (*workflowDefinitionFake) GetVersion(context.Context, string, string) (*workflowdomain.Version, error) {
	return &workflowdomain.Version{ID: "version-1", DefinitionID: "wf-1", Number: 1}, nil
}

type workflowRunFake struct{ events []workflowdomain.Event }

type workflowControlFake struct {
	canceled      bool
	decided       workflowapp.DecideApprovalCommand
	decisionCalls int
}

func (f *workflowControlFake) Cancel(_ context.Context, _, _ string, _ int64, actor, _ string) (*workflowdomain.Run, error) {
	f.canceled = actor == "admin-1"
	return &workflowdomain.Run{ID: "run-1", Status: workflowdomain.RunStatusCancelRequested, Generation: 3}, nil
}
func (*workflowControlFake) Pause(context.Context, string, string, int64, string, string) (*workflowdomain.Run, error) {
	return &workflowdomain.Run{ID: "run-1", Status: workflowdomain.RunStatusPauseRequested}, nil
}
func (*workflowControlFake) Resume(context.Context, string, string, int64, string) (*workflowdomain.Run, error) {
	return &workflowdomain.Run{ID: "run-1", Status: workflowdomain.RunStatusQueued}, nil
}
func (f *workflowControlFake) DecideApproval(_ context.Context, _ string, cmd workflowapp.DecideApprovalCommand) error {
	f.decisionCalls++
	f.decided = cmd
	return nil
}
func (*workflowControlFake) ResolveManual(context.Context, string, workflowapp.ResolveManualCommand) error {
	return nil
}
func (*workflowControlFake) AvailableActions(context.Context, string, string) ([]string, error) {
	return []string{"cancel"}, nil
}
func (*workflowControlFake) ListApprovals(context.Context, string, string, bool) ([]workflowdomain.Approval, error) {
	return []workflowdomain.Approval{{ID: "approval-1", Status: workflowdomain.ApprovalStatusPending}}, nil
}
func (*workflowControlFake) ListEffects(context.Context, string, string) ([]workflowdomain.EffectIntent, error) {
	return nil, nil
}

func (*workflowRunFake) StartAsync(context.Context, string, workflowapp.StartRunCommand) (*workflowdomain.Run, bool, error) {
	return &workflowdomain.Run{ID: "run-1", Status: workflowdomain.RunStatusQueued}, true, nil
}
func (*workflowRunFake) Get(context.Context, string, string) (*workflowdomain.Run, []workflowdomain.NodeAttempt, error) {
	return &workflowdomain.Run{ID: "run-1", Status: workflowdomain.RunStatusCompleted, Output: "done", Snapshot: workflowdomain.Spec{Nodes: []workflowdomain.Node{{ID: "one", Type: workflowdomain.NodeTypeAgent, AgentID: "a"}}}}, []workflowdomain.NodeAttempt{{NodeID: "one", Status: workflowdomain.AttemptStatusSucceeded}}, nil
}
func (f *workflowRunFake) Events(_ context.Context, _, _ string, after int64, _ int) ([]workflowdomain.Event, error) {
	var out []workflowdomain.Event
	for _, event := range f.events {
		if event.SequenceNo > after {
			out = append(out, event)
		}
	}
	return out, nil
}

func TestWorkflowHandlerCreateAndStart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	definitions := &workflowDefinitionFake{}
	h := handler.NewWorkflowHandler(definitions, &workflowRunFake{})
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Next()
	})
	r.POST("/workflows", h.CreateDefinition)
	r.POST("/workflow-runs", h.StartRun)

	createBody := `{"name":"Research","spec":{"nodes":[{"id":"one","type":"agent","agent_id":"agent-1"}],"edges":[]}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var created map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.Equal(t, "wf-1", created["id"])

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/workflow-runs", strings.NewReader(`{"version_id":"version-1","input":{"query":"hello"},"idempotency_key":"key-1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	var started map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &started))
	require.Equal(t, "run-1", started["run_id"])
}

func TestWorkflowHandlerEventsQueryAndSSEResumeCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	runs := &workflowRunFake{events: []workflowdomain.Event{{RunID: "run-1", SequenceNo: 1, Type: "workflow.run_started"}, {RunID: "run-1", SequenceNo: 2, Type: "workflow.node_completed"}}}
	h := handler.NewWorkflowHandler(&workflowDefinitionFake{}, runs)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Next()
	})
	r.GET("/workflow-runs/:id", h.GetRun)
	r.GET("/workflow-runs/:id/events", h.GetEvents)
	r.GET("/workflow-runs/:id/events/stream", h.StreamEvents)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workflow-runs/run-1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"progress":{"completed":1,"total":1}`)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workflow-runs/run-1/events?after_sequence=1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), `"sequence_no":1`)
	require.Contains(t, w.Body.String(), `"sequence_no":2`)

	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/workflow-runs/run-1/events/stream", nil)
	req.Header.Set("Last-Event-ID", "1")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	require.Contains(t, w.Body.String(), "id: 2\n")
	require.NotContains(t, w.Body.String(), "id: 1\n")
}

func TestWorkflowHandlerDefinitionAndVersionQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	definitions := &workflowDefinitionFake{created: &workflowdomain.Definition{ID: "wf-1", Name: "DAG", Revision: 2}}
	h := handler.NewWorkflowHandler(definitions, &workflowRunFake{})
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Next()
	})
	r.GET("/workflows/:id", h.GetDefinition)
	r.GET("/workflows/:id/versions/:versionID", h.GetVersion)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workflows/wf-1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"revision":2`)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workflows/wf-1/versions/version-1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"version":1`)
}

func TestWorkflowControlAndApprovalHandlersUseExpectedGenerationAndActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	control := &workflowControlFake{}
	h := handler.NewWorkflowHandlerWithControl(&workflowDefinitionFake{}, &workflowRunFake{}, control)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Set("auth.sub", "admin-1")
		c.Next()
	})
	r.POST("/workflow-runs/:id/cancel", h.CancelRun)
	r.GET("/workflow-approvals", h.ListApprovals)
	r.POST("/workflow-approvals/:id/decision", h.DecideApproval)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/workflow-runs/run-1/cancel", strings.NewReader(`{"expected_generation":2,"reason":"stop"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.True(t, control.canceled)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workflow-approvals?pending=true", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "approval-1")
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/workflow-approvals/approval-1/decision", strings.NewReader(`{"run_id":"run-1","attempt_id":"attempt-1","expected_generation":5,"decision":"approve","comment":"ok"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "admin-1", control.decided.ActorID)
	require.Equal(t, int64(5), control.decided.ExpectedGeneration)
}

func TestWorkflowApprovalHandlerRejectsMalformedDecision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	control := &workflowControlFake{}
	h := handler.NewWorkflowHandlerWithControl(&workflowDefinitionFake{}, &workflowRunFake{}, control)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Set("auth.sub", "admin")
		c.Next()
	})
	r.POST("/workflow-approvals/:id/decision", h.DecideApproval)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/workflow-approvals/approval/decision", strings.NewReader(`{"run_id":"run-1","attempt_id":"attempt","expected_generation":5,"decision":"approve "}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, control.decisionCalls)
}

func TestWorkflowSSEWaitsForLaterEventsOnSameConnection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	runs := &streamingWorkflowRunFake{status: workflowdomain.RunStatusRunning}
	h := handler.NewWorkflowHandler(&workflowDefinitionFake{}, runs)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Next()
	})
	r.GET("/workflow-runs/:id/events/stream", h.StreamEvents)
	server := httptest.NewServer(r)
	defer server.Close()
	resp, err := http.Get(server.URL + "/workflow-runs/run-1/events/stream")
	require.NoError(t, err)
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	require.True(t, lines.Scan())
	require.Equal(t, ": connected", lines.Text())
	runs.mu.Lock()
	runs.events = append(runs.events, workflowdomain.Event{RunID: "run-1", SequenceNo: 1, Type: "workflow.node_started"})
	runs.status = workflowdomain.RunStatusCompleted
	runs.mu.Unlock()
	received := make(chan string, 1)
	go func() {
		for lines.Scan() {
			if strings.HasPrefix(lines.Text(), "id: ") {
				received <- lines.Text()
				return
			}
		}
	}()
	select {
	case line := <-received:
		require.Equal(t, "id: 1", line)
	case <-time.After(2 * time.Second):
		t.Fatal("SSE did not deliver event produced after connection")
	}
}
