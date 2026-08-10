package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	schedapp "github.com/byteBuilderX/stratum/internal/scheduler/application"
	scheddomain "github.com/byteBuilderX/stratum/internal/scheduler/domain"
)

// schedulerService is the scheduled-task lifecycle the handler consumes;
// wired as *schedapp.Service. Authorization (admin-only writes, member
// reads) is enforced by the service.
type schedulerService interface {
	Create(context.Context, string, schedapp.CreateCommand, schedapp.Actor) (*scheddomain.ScheduledTask, error)
	Update(context.Context, string, string, schedapp.UpdateCommand, schedapp.Actor) (*scheddomain.ScheduledTask, error)
	Delete(context.Context, string, string, schedapp.Actor) error
	SetEnabled(context.Context, string, string, bool, schedapp.Actor) error
	Get(context.Context, string, string) (*scheddomain.ScheduledTask, error)
	List(context.Context, string, int, int) ([]scheddomain.ScheduledTask, int, error)
}

// ScheduledTaskHandler exposes the tenant-facing scheduled-task surface.
type ScheduledTaskHandler struct {
	service schedulerService
}

// NewScheduledTaskHandler constructs the handler.
func NewScheduledTaskHandler(service schedulerService) *ScheduledTaskHandler {
	return &ScheduledTaskHandler{service: service}
}

// schedulerIdentity extracts the tenant plus the actor (user + tenant role).
func schedulerIdentity(c *gin.Context) (string, schedapp.Actor, bool) {
	tenantID, tenantOK := tenantIDFromCtx(c)
	actorID, actorOK := userIDFromCtx(c)
	if !tenantOK || !actorOK || actorID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("scheduler identity required")))
		return "", schedapp.Actor{}, false
	}
	return tenantID, schedapp.Actor{UserID: actorID, Role: c.GetString(middleware.ContextKeyRole)}, true
}

func (h *ScheduledTaskHandler) Create(c *gin.Context) {
	tenantID, actor, ok := schedulerIdentity(c)
	if !ok {
		return
	}
	var req gen.CreateScheduledTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	task, err := h.service.Create(c.Request.Context(), tenantID, schedapp.CreateCommand{
		Name:          req.Name,
		WorkflowID:    req.WorkflowID,
		VersionID:     req.VersionID,
		InputTemplate: req.InputTemplate,
		CronExpr:      req.CronExpr,
	}, actor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gen.ToScheduledTaskResponse(*task))
}

func (h *ScheduledTaskHandler) Update(c *gin.Context) {
	tenantID, actor, ok := schedulerIdentity(c)
	if !ok {
		return
	}
	var req gen.UpdateScheduledTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	task, err := h.service.Update(c.Request.Context(), tenantID, c.Param("id"), schedapp.UpdateCommand{
		Name:          req.Name,
		WorkflowID:    req.WorkflowID,
		VersionID:     req.VersionID,
		InputTemplate: req.InputTemplate,
		CronExpr:      req.CronExpr,
	}, actor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gen.ToScheduledTaskResponse(*task))
}

func (h *ScheduledTaskHandler) Delete(c *gin.Context) {
	tenantID, actor, ok := schedulerIdentity(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), tenantID, c.Param("id"), actor); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "scheduled task deleted successfully"})
}

func (h *ScheduledTaskHandler) SetEnabled(c *gin.Context) {
	tenantID, actor, ok := schedulerIdentity(c)
	if !ok {
		return
	}
	var req gen.SetScheduledTaskEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := h.service.SetEnabled(c.Request.Context(), tenantID, c.Param("id"), req.Enabled, actor); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "scheduled task updated successfully"})
}

func (h *ScheduledTaskHandler) Get(c *gin.Context) {
	tenantID, _, ok := schedulerIdentity(c)
	if !ok {
		return
	}
	task, err := h.service.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gen.ToScheduledTaskResponse(*task))
}

func (h *ScheduledTaskHandler) List(c *gin.Context) {
	tenantID, _, ok := schedulerIdentity(c)
	if !ok {
		return
	}
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 32)
	pageSize, _ := strconv.ParseInt(c.DefaultQuery("page_size", "20"), 10, 32)
	tasks, total, err := h.service.List(c.Request.Context(), tenantID, int(page), int(pageSize))
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]gen.ScheduledTaskResponse, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, gen.ToScheduledTaskResponse(t))
	}
	c.JSON(http.StatusOK, gen.ScheduledTaskPageResponse{
		Tasks: out,
		//nolint:gosec // total 是 COUNT(*) 结果,任务数不可能溢出 int32(proto 契约)
		Total:    int32(total),
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
}
