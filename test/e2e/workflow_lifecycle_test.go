//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	workflowapp "github.com/byteBuilderX/stratum/internal/workflow/application"
	workflowdomain "github.com/byteBuilderX/stratum/internal/workflow/domain"
	workflowport "github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	workflowpersist "github.com/byteBuilderX/stratum/internal/workflow/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type workflowE2EExecutor struct {
	mu    sync.Mutex
	calls []string
}

func (e *workflowE2EExecutor) Execute(_ context.Context, request workflowport.NodeExecutionRequest) (workflowport.NodeExecutionResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, request.Node.ID)
	e.mu.Unlock()
	return workflowport.NodeExecutionResult{Output: request.Node.ID + "-output", TraceID: "trace-" + request.Node.ID}, nil
}

func (e *workflowE2EExecutor) Calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

func newWorkflowE2ERouter(tenantID string, definitions *workflowapp.DefinitionService, runs *workflowapp.RunService, controls *workflowapp.ControlService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.Use(func(c *gin.Context) {
		userID := c.GetHeader("X-E2E-User")
		if userID == "" {
			userID = "workflow-e2e-admin"
		}
		role := c.GetHeader("X-E2E-Role")
		if role == "" {
			role = "admin"
		}
		ctx := reqctx.WithTenantID(c.Request.Context(), tenantID)
		ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID, UserID: userID, Role: postgres.RoleTenantAdmin})
		c.Request = c.Request.WithContext(ctx)
		c.Set("auth.sub", userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	})
	h := handler.NewWorkflowHandlerWithControl(definitions, runs, controls)
	r.POST("/workflows", h.CreateDefinition)
	r.PUT("/workflows/:id/draft", h.UpdateDefinition)
	r.POST("/workflows/:id/validate", h.ValidateDefinition)
	r.POST("/workflows/:id/publish", h.PublishDefinition)
	r.GET("/workflows/:id/versions/:versionID", h.GetVersion)
	r.POST("/workflow-runs", h.StartRun)
	r.GET("/workflow-runs", h.ListRuns)
	r.GET("/workflow-runs/:id", h.GetRun)
	r.GET("/workflow-runs/:id/events", h.GetEvents)
	r.GET("/workflow-runs/:id/events/stream", h.StreamEvents)
	r.POST("/workflow-runs/:id/cancel", h.CancelRun)
	r.POST("/workflow-runs/:id/resume", h.ResumeRun)
	r.POST("/workflow-approvals/:id/decision", h.DecideApproval)
	return r
}

func workflowRequest(t *testing.T, r http.Handler, method, path string, body any, want int) []byte {
	return workflowRequestAs(t, r, method, path, body, want, "workflow-e2e-admin", "admin")
}

func workflowRequestAs(t *testing.T, r http.Handler, method, path string, body any, want int, userID, role string) []byte {
	t.Helper()
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-E2E-User", userID)
	req.Header.Set("X-E2E-Role", role)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, want, w.Code, "response: %s", w.Body.String())
	return w.Body.Bytes()
}

func workflowSSERequestAs(t *testing.T, r http.Handler, path, userID, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-E2E-User", userID)
	req.Header.Set("X-E2E-Role", role)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestWorkflowHTTPPostgresWorkerApprovalRestartAndSSEE2E(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("STRATUM_TEST_POSTGRES_URL"))
	if url == "" {
		t.Fatal("STRATUM_TEST_POSTGRES_URL is required; workflow E2E must not skip")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()))

	tenantID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO tenants (id,name,slug,status) VALUES ($1,$2,$3,'active')`, tenantID, "Workflow E2E", "workflow-e2e-"+tenantID[:8])
	require.NoError(t, err)
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	store := workflowpersist.NewPgStore(pool)
	newID := uuid.NewString
	definitions := workflowapp.NewDefinitionService(store, store, newID)
	executorA := &workflowE2EExecutor{}
	runsA := workflowapp.NewRunServiceWithRegistry(store, store, executorA, newID)
	controls := workflowapp.NewControlService(store, newID)
	router := newWorkflowE2ERouter(tenantID, definitions, runsA, controls)

	spec := map[string]any{
		"nodes": []map[string]any{
			{"id": "approve", "type": "approval"},
			{"id": "left", "type": "agent", "agent_id": "agent-left"},
			{"id": "right", "type": "agent", "agent_id": "agent-right"},
			{"id": "join", "type": "agent", "agent_id": "agent-join"},
		},
		"edges": []map[string]any{
			{"from": "approve", "to": "left"}, {"from": "approve", "to": "right"},
			{"from": "left", "to": "join"}, {"from": "right", "to": "join"},
		},
		"max_concurrency": 2,
	}
	inputSchema := map[string]any{
		"task_label": "任务",
		"fields":     []map[string]any{{"key": "region", "label": "区域", "type": "short_text", "required": true}},
	}
	createdBody := workflowRequest(t, router, http.MethodPost, "/workflows", map[string]any{
		"name": "Approval diamond", "description": "draft", "spec": spec, "input_schema": inputSchema,
	}, http.StatusCreated)
	var definition workflowdomain.Definition
	require.NoError(t, json.Unmarshal(createdBody, &definition))
	require.Equal(t, int64(1), definition.Revision)

	updatedBody := workflowRequest(t, router, http.MethodPut, "/workflows/"+definition.ID+"/draft", map[string]any{
		"name": "Approval diamond", "description": "updated", "spec": spec,
		"input_schema": inputSchema, "expected_revision": 1,
	}, http.StatusOK)
	require.NoError(t, json.Unmarshal(updatedBody, &definition))
	require.Equal(t, int64(2), definition.Revision)
	workflowRequest(t, router, http.MethodPost, "/workflows/"+definition.ID+"/validate", nil, http.StatusOK)

	publishedBody := workflowRequest(t, router, http.MethodPost, "/workflows/"+definition.ID+"/publish", nil, http.StatusCreated)
	var version workflowdomain.Version
	require.NoError(t, json.Unmarshal(publishedBody, &version))
	require.NotEmpty(t, version.ID)
	versionBody := workflowRequest(t, router, http.MethodGet, "/workflows/"+definition.ID+"/versions/"+version.ID, nil, http.StatusOK)
	var immutable workflowdomain.Version
	require.NoError(t, json.Unmarshal(versionBody, &immutable))
	require.Equal(t, version.Spec, immutable.Spec)

	workflowRequestAs(t, router, http.MethodPost, "/workflow-runs", map[string]any{
		"version_id": version.ID, "task": "hello", "fields": map[string]any{}, "idempotency_key": "invalid-input",
	}, http.StatusBadRequest, "member-a", "member")
	var emptyRunPage struct {
		Runs  []workflowdomain.Run `json:"runs"`
		Total int                  `json:"total"`
	}
	emptyBody := workflowRequest(t, router, http.MethodGet, "/workflow-runs", nil, http.StatusOK)
	require.NoError(t, json.Unmarshal(emptyBody, &emptyRunPage))
	require.Zero(t, emptyRunPage.Total, "invalid input must not create a run")

	startRequest := map[string]any{
		"version_id": version.ID, "task": "hello", "fields": map[string]any{"region": "cn"},
		"idempotency_key": "workflow-http-e2e",
	}
	startedBody := workflowRequestAs(t, router, http.MethodPost, "/workflow-runs", startRequest, http.StatusAccepted, "member-a", "member")
	var started struct {
		RunID  string                   `json:"run_id"`
		Status workflowdomain.RunStatus `json:"status"`
	}
	require.NoError(t, json.Unmarshal(startedBody, &started))
	require.Equal(t, workflowdomain.RunStatusQueued, started.Status)
	workflowRequestAs(t, router, http.MethodGet, "/workflow-runs/"+started.RunID, nil, http.StatusNotFound, "member-b", "member")
	workflowRequestAs(t, router, http.MethodGet, "/workflow-runs/"+started.RunID+"/events", nil, http.StatusNotFound, "member-b", "member")
	workflowRequestAs(t, router, http.MethodPost, "/workflow-runs/"+started.RunID+"/cancel", map[string]any{
		"expected_generation": 1, "reason": "not mine",
	}, http.StatusNotFound, "member-b", "member")

	memberListBody := workflowRequestAs(t, router, http.MethodGet, "/workflow-runs", nil, http.StatusOK, "member-b", "member")
	var memberList struct {
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(memberListBody, &memberList))
	require.Zero(t, memberList.Total)
	adminListBody := workflowRequest(t, router, http.MethodGet, "/workflow-runs", nil, http.StatusOK)
	var adminList struct {
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(adminListBody, &adminList))
	require.Equal(t, 1, adminList.Total)

	workerA := workflowapp.NewWorker("workflow-e2e-worker-a", store, runsA, 10*time.Second)
	require.True(t, workerA.RunOnce(ctx))

	pausedBody := workflowRequestAs(t, router, http.MethodGet, "/workflow-runs/"+started.RunID, nil, http.StatusOK, "member-a", "member")
	var paused struct {
		Run       workflowdomain.Run           `json:"run"`
		Attempts  []workflowdomain.NodeAttempt `json:"node_attempts"`
		Approvals []workflowdomain.Approval    `json:"approvals"`
		Actions   []string                     `json:"available_actions"`
	}
	require.NoError(t, json.Unmarshal(pausedBody, &paused))
	require.Equal(t, workflowdomain.RunStatusPaused, paused.Run.Status)
	require.Len(t, paused.Attempts, 1)
	require.Len(t, paused.Approvals, 1)
	require.Contains(t, paused.Actions, "cancel")

	cancelBody := workflowRequestAs(t, router, http.MethodPost, "/workflow-runs/"+started.RunID+"/cancel", map[string]any{
		"expected_generation": paused.Run.Generation, "reason": "member requested cancellation",
	}, http.StatusAccepted, "member-a", "member")
	var cancelRequested workflowdomain.Run
	require.NoError(t, json.Unmarshal(cancelBody, &cancelRequested))
	require.Equal(t, workflowdomain.RunStatusCancelRequested, cancelRequested.Status)
	require.True(t, workerA.RunOnce(ctx))
	canceledBody := workflowRequestAs(t, router, http.MethodGet, "/workflow-runs/"+started.RunID, nil, http.StatusOK, "member-a", "member")
	var canceled struct {
		Run workflowdomain.Run `json:"run"`
	}
	require.NoError(t, json.Unmarshal(canceledBody, &canceled))
	require.Equal(t, workflowdomain.RunStatusCanceled, canceled.Run.Status)

	// Start a second run so the admin control path can continue through approval and restart.
	startRequest["idempotency_key"] = "workflow-http-e2e-admin-control"
	startedBody = workflowRequestAs(t, router, http.MethodPost, "/workflow-runs", startRequest, http.StatusAccepted, "member-a", "member")
	require.NoError(t, json.Unmarshal(startedBody, &started))
	require.True(t, workerA.RunOnce(ctx))
	pausedBody = workflowRequestAs(t, router, http.MethodGet, "/workflow-runs/"+started.RunID, nil, http.StatusOK, "member-a", "member")
	require.NoError(t, json.Unmarshal(pausedBody, &paused))
	require.Equal(t, workflowdomain.RunStatusPaused, paused.Run.Status)

	approval := paused.Approvals[0]
	workflowRequest(t, router, http.MethodPost, "/workflow-approvals/"+approval.ID+"/decision", map[string]any{
		"run_id": started.RunID, "attempt_id": approval.AttemptID, "expected_generation": paused.Run.Generation,
		"decision": "approve", "comment": "approved by E2E",
	}, http.StatusOK)
	workflowRequest(t, router, http.MethodPost, "/workflow-runs/"+started.RunID+"/resume", map[string]any{"expected_generation": paused.Run.Generation}, http.StatusAccepted)

	// Rebuild the run service and worker from PostgreSQL only to simulate restart.
	executorB := &workflowE2EExecutor{}
	runsB := workflowapp.NewRunServiceWithRegistry(store, store, executorB, newID)
	workerB := workflowapp.NewWorker("workflow-e2e-worker-b", store, runsB, 10*time.Second)
	require.True(t, workerB.RunOnce(ctx))

	completedBody := workflowRequest(t, router, http.MethodGet, "/workflow-runs/"+started.RunID, nil, http.StatusOK)
	var completed struct {
		Run      workflowdomain.Run           `json:"run"`
		Attempts []workflowdomain.NodeAttempt `json:"node_attempts"`
		Progress struct {
			Completed int `json:"completed"`
			Total     int `json:"total"`
		} `json:"progress"`
	}
	require.NoError(t, json.Unmarshal(completedBody, &completed))
	require.Equal(t, workflowdomain.RunStatusCompleted, completed.Run.Status)
	require.Equal(t, 4, completed.Progress.Completed)
	require.Equal(t, 4, completed.Progress.Total)
	require.Len(t, completed.Attempts, 4)
	require.Empty(t, executorA.Calls())
	require.ElementsMatch(t, []string{"left", "right", "join"}, executorB.Calls())

	eventsBody := workflowRequest(t, router, http.MethodGet, "/workflow-runs/"+started.RunID+"/events", nil, http.StatusOK)
	var page struct {
		Events []workflowdomain.Event `json:"events"`
	}
	require.NoError(t, json.Unmarshal(eventsBody, &page))
	require.Greater(t, len(page.Events), 8)
	for i, event := range page.Events {
		require.Equal(t, int64(i+1), event.SequenceNo)
	}
	cursor := page.Events[len(page.Events)/2].SequenceNo
	w := workflowSSERequestAs(t, router, "/workflow-runs/"+started.RunID+"/events/stream?after_sequence="+fmt.Sprint(cursor), "member-a", "member")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	require.NotContains(t, w.Body.String(), fmt.Sprintf("id: %d\n", cursor))
	require.Contains(t, w.Body.String(), fmt.Sprintf("id: %d\n", cursor+1))
	var resumedIDs []int64
	for _, line := range strings.Split(w.Body.String(), "\n") {
		var id int64
		if _, scanErr := fmt.Sscanf(line, "id: %d", &id); scanErr == nil {
			resumedIDs = append(resumedIDs, id)
		}
	}
	require.NotEmpty(t, resumedIDs)
	for index, id := range resumedIDs {
		require.Greater(t, id, cursor)
		if index > 0 {
			require.Greater(t, id, resumedIDs[index-1])
		}
	}

	replayed := workflowRequestAs(t, router, http.MethodPost, "/workflow-runs", startRequest, http.StatusOK, "member-a", "member")
	var replay struct {
		RunID string `json:"run_id"`
	}
	require.NoError(t, json.Unmarshal(replayed, &replay))
	require.Equal(t, started.RunID, replay.RunID)
	workflowRequestAs(t, router, http.MethodPost, "/workflow-runs", map[string]any{
		"version_id": version.ID, "task": "different", "fields": map[string]any{"region": "cn"},
		"idempotency_key": "workflow-http-e2e-admin-control",
	}, http.StatusConflict, "member-a", "member")
}
