package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	workflowapp "github.com/byteBuilderX/stratum/internal/workflow/application"
	workflowdomain "github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/gin-gonic/gin"
)

type workflowDefinitionService interface {
	Create(context.Context, string, workflowapp.CreateDefinitionCommand) (*workflowdomain.Definition, error)
	Update(context.Context, string, string, workflowapp.UpdateDefinitionCommand) (*workflowdomain.Definition, error)
	Validate(context.Context, string, string) error
	Publish(context.Context, string, string) (*workflowdomain.Version, error)
	Get(context.Context, string, string) (*workflowdomain.Definition, error)
	GetVersion(context.Context, string, string) (*workflowdomain.Version, error)
}

func (h *WorkflowHandler) GetDefinition(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	definition, err := h.definitions.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, definition)
}

func (h *WorkflowHandler) GetVersion(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	version, err := h.definitions.GetVersion(c.Request.Context(), tenantID, c.Param("versionID"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	if version.DefinitionID != c.Param("id") {
		_ = c.Error(workflowdomain.ErrNotFound)
		return
	}
	c.JSON(http.StatusOK, version)
}

type workflowRunService interface {
	StartAsync(context.Context, string, workflowapp.StartRunCommand) (*workflowdomain.Run, bool, error)
	Get(context.Context, string, string, workflowapp.Actor) (*workflowdomain.Run, []workflowdomain.NodeAttempt, error)
	Events(context.Context, string, string, workflowapp.Actor, int64, int) ([]workflowdomain.Event, error)
}

type WorkflowHandler struct {
	definitions workflowDefinitionService
	runs        workflowRunService
	controls    workflowControlService
}

type workflowControlService interface {
	Cancel(context.Context, string, string, int64, workflowapp.Actor, string) (*workflowdomain.Run, error)
	Pause(context.Context, string, string, int64, workflowapp.Actor, string) (*workflowdomain.Run, error)
	Resume(context.Context, string, string, int64, workflowapp.Actor) (*workflowdomain.Run, error)
	DecideApproval(context.Context, string, workflowapp.DecideApprovalCommand) error
	ResolveManual(context.Context, string, workflowapp.ResolveManualCommand) error
	AvailableActions(context.Context, string, string, workflowapp.Actor) ([]string, error)
	ListApprovals(context.Context, string, string, workflowapp.Actor, bool) ([]workflowdomain.Approval, error)
	ListEffects(context.Context, string, string, workflowapp.Actor) ([]workflowdomain.EffectIntent, error)
}

func NewWorkflowHandler(definitions workflowDefinitionService, runs workflowRunService) *WorkflowHandler {
	return &WorkflowHandler{definitions: definitions, runs: runs}
}

func NewWorkflowHandlerWithControl(definitions workflowDefinitionService, runs workflowRunService, controls workflowControlService) *WorkflowHandler {
	return &WorkflowHandler{definitions: definitions, runs: runs, controls: controls}
}

func (h *WorkflowHandler) CreateDefinition(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req dto.CreateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	definition, err := h.definitions.Create(c.Request.Context(), tenantID, workflowapp.CreateDefinitionCommand{Name: req.Name, Description: req.Description, Spec: req.Spec})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, definition)
}

func (h *WorkflowHandler) UpdateDefinition(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req dto.UpdateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	definition, err := h.definitions.Update(c.Request.Context(), tenantID, c.Param("id"), workflowapp.UpdateDefinitionCommand{Name: req.Name, Description: req.Description, Spec: req.Spec, ExpectedRevision: req.ExpectedRevision})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, definition)
}

func (h *WorkflowHandler) ValidateDefinition(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if err := h.definitions.Validate(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"valid": true})
}

func (h *WorkflowHandler) PublishDefinition(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	version, err := h.definitions.Publish(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, version)
}

func (h *WorkflowHandler) StartRun(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, ok := workflowActor(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, fmt.Errorf("authenticated actor required")))
		return
	}
	var req dto.StartWorkflowRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	run, created, err := h.runs.StartAsync(c.Request.Context(), tenantID, workflowapp.StartRunCommand{VersionID: req.VersionID, Input: req.Input, IdempotencyKey: req.IdempotencyKey, CreatedBy: actor.UserID})
	if err != nil {
		_ = c.Error(err)
		return
	}
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{"run_id": run.ID, "status": run.Status})
}

func (h *WorkflowHandler) GetRun(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, ok := workflowActor(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, fmt.Errorf("authenticated actor required")))
		return
	}
	run, attempts, err := h.runs.Get(c.Request.Context(), tenantID, c.Param("id"), actor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	completed := 0
	for _, attempt := range attempts {
		if attempt.Status == workflowdomain.AttemptStatusSucceeded || attempt.Status == workflowdomain.AttemptStatusSkipped {
			completed++
		}
	}
	actions := []string{}
	approvals := []workflowdomain.Approval{}
	effects := []workflowdomain.EffectIntent{}
	if h.controls != nil {
		actions, _ = h.controls.AvailableActions(c.Request.Context(), tenantID, run.ID, actor)
		approvals, _ = h.controls.ListApprovals(c.Request.Context(), tenantID, run.ID, actor, false)
		effects, _ = h.controls.ListEffects(c.Request.Context(), tenantID, run.ID, actor)
	}
	c.JSON(http.StatusOK, gin.H{"run": run, "node_attempts": attempts, "approvals": approvals, "effect_intents": effects, "progress": gin.H{"completed": completed, "total": len(run.Snapshot.Nodes)}, "available_actions": actions})
}

func workflowActor(c *gin.Context) (workflowapp.Actor, bool) {
	userID, ok := userIDFromCtx(c)
	if !ok {
		return workflowapp.Actor{}, false
	}
	roleValue, ok := c.Get(middleware.ContextKeyRole)
	role, roleOK := roleValue.(string)
	if !ok || !roleOK || role == "" {
		return workflowapp.Actor{}, false
	}
	return workflowapp.Actor{UserID: userID, Role: role}, true
}
func (h *WorkflowHandler) controlRun(c *gin.Context, action string) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, ok := workflowActor(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, fmt.Errorf("authenticated actor required")))
		return
	}
	var req dto.WorkflowControlRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	var run *workflowdomain.Run
	var err error
	switch action {
	case "cancel":
		run, err = h.controls.Cancel(c.Request.Context(), tenantID, c.Param("id"), req.ExpectedGeneration, actor, req.Reason)
	case "pause":
		run, err = h.controls.Pause(c.Request.Context(), tenantID, c.Param("id"), req.ExpectedGeneration, actor, req.Reason)
	case "resume":
		run, err = h.controls.Resume(c.Request.Context(), tenantID, c.Param("id"), req.ExpectedGeneration, actor)
	}
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusAccepted, run)
}
func (h *WorkflowHandler) CancelRun(c *gin.Context) { h.controlRun(c, "cancel") }
func (h *WorkflowHandler) PauseRun(c *gin.Context)  { h.controlRun(c, "pause") }
func (h *WorkflowHandler) ResumeRun(c *gin.Context) { h.controlRun(c, "resume") }
func (h *WorkflowHandler) ListApprovals(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, ok := workflowActor(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, fmt.Errorf("authenticated actor required")))
		return
	}
	pending := c.Query("pending") != "false"
	rows, err := h.controls.ListApprovals(c.Request.Context(), tenantID, c.Query("run_id"), actor, pending)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"approvals": rows})
}
func (h *WorkflowHandler) DecideApproval(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, ok := workflowActor(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, fmt.Errorf("authenticated actor required")))
		return
	}
	var req dto.WorkflowApprovalDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	err := h.controls.DecideApproval(c.Request.Context(), tenantID, workflowapp.DecideApprovalCommand{ApprovalID: c.Param("id"), RunID: req.RunID, AttemptID: req.AttemptID, ExpectedGeneration: req.ExpectedGeneration, Decision: req.Decision, ActorID: actor.UserID, ActorRole: actor.Role, Comment: req.Comment})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "decided"})
}
func (h *WorkflowHandler) ResolveManual(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, ok := workflowActor(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, fmt.Errorf("authenticated actor required")))
		return
	}
	var req dto.WorkflowManualResolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	err := h.controls.ResolveManual(c.Request.Context(), tenantID, workflowapp.ResolveManualCommand{RunID: c.Param("id"), EffectIntentID: c.Param("effectID"), ExpectedGeneration: req.ExpectedGeneration, Action: req.Action, OutputSummary: req.OutputSummary, ActorID: actor.UserID, ActorRole: actor.Role})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "resolved"})
}

func (h *WorkflowHandler) GetEvents(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, ok := workflowActor(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, fmt.Errorf("authenticated actor required")))
		return
	}
	after, err := parseSequence(c.Query("after_sequence"))
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	events, err := h.runs.Events(c.Request.Context(), tenantID, c.Param("id"), actor, after, 500)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "after_sequence": after})
}

func (h *WorkflowHandler) StreamEvents(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, ok := workflowActor(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, fmt.Errorf("authenticated actor required")))
		return
	}
	cursor := c.GetHeader("Last-Event-ID")
	if cursor == "" {
		cursor = c.Query("after_sequence")
	}
	after, err := parseSequence(cursor)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(c.Writer, ": connected\n\n")
	c.Writer.Flush()
	eventCursor := after
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		events, queryErr := h.runs.Events(c.Request.Context(), tenantID, c.Param("id"), actor, eventCursor, 200)
		if queryErr != nil {
			return
		}
		for _, event := range events {
			payload, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return
			}
			if _, writeErr := fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", event.SequenceNo, event.Type, payload); writeErr != nil {
				return
			}
			eventCursor = event.SequenceNo
		}
		if len(events) > 0 {
			c.Writer.Flush()
		}
		if len(events) == 200 {
			continue
		}
		run, _, getErr := h.runs.Get(c.Request.Context(), tenantID, c.Param("id"), actor)
		if getErr != nil {
			return
		}
		if workflowRunTerminal(run.Status) {
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			if _, writeErr := fmt.Fprint(c.Writer, ": heartbeat\n\n"); writeErr != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

func workflowRunTerminal(status workflowdomain.RunStatus) bool {
	return status == workflowdomain.RunStatusCompleted || status == workflowdomain.RunStatusFailed || status == workflowdomain.RunStatusCanceled
}

func parseSequence(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	sequence, err := strconv.ParseInt(value, 10, 64)
	if err != nil || sequence < 0 {
		return 0, fmt.Errorf("invalid event sequence")
	}
	return sequence, nil
}
