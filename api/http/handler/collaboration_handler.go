package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	collabapp "github.com/byteBuilderX/stratum/internal/collab/application"
	collabdomain "github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/gin-gonic/gin"
)

// collaborationService is the plan lifecycle the handler consumes; wired as
// *collabapp.CollaborationService.
type collaborationService interface {
	Create(context.Context, string, collabapp.Actor, string, collabdomain.CollabStrategy, []string) (*collabdomain.Collaboration, error)
	Get(context.Context, string, string, collabapp.Actor) (*collabdomain.Collaboration, error)
	List(context.Context, string, collabapp.Actor, int, int) ([]collabdomain.Collaboration, error)
	ReadyTasks(context.Context, string, string, collabapp.Actor) ([]collabdomain.TaskStep, error)
	Start(context.Context, string, string, collabapp.Actor) (*collabdomain.Collaboration, error)
	Cancel(context.Context, string, string, collabapp.Actor) error
}

// CollaborationHandler exposes the member-facing plan lifecycle: create /
// list / get / start / cancel. Authorization (creator vs admin/owner) lives
// in the service; the handler only extracts identity.
type CollaborationHandler struct {
	service collaborationService
}

func NewCollaborationHandler(service collaborationService) *CollaborationHandler {
	return &CollaborationHandler{service: service}
}

// collabIdentity extracts the tenant plus the actor (user + tenant role).
func collabIdentity(c *gin.Context) (string, collabapp.Actor, bool) {
	tenantID, tenantOK := tenantIDFromCtx(c)
	actorID, actorOK := userIDFromCtx(c)
	if !tenantOK || !actorOK || actorID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("collab identity required")))
		return "", collabapp.Actor{}, false
	}
	return tenantID, collabapp.Actor{UserID: actorID, Role: c.GetString(middleware.ContextKeyRole)}, true
}

func (h *CollaborationHandler) Create(c *gin.Context) {
	tenantID, actor, ok := collabIdentity(c)
	if !ok {
		return
	}
	var req dto.CreateCollabRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, collabdomain.ErrCollabInvalidInput))
		return
	}
	collab, err := h.service.Create(c.Request.Context(), tenantID, actor, req.TaskDescription, req.Strategy, req.Participants)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, dto.ToCollabResponse(*collab))
}

func (h *CollaborationHandler) List(c *gin.Context) {
	tenantID, actor, ok := collabIdentity(c)
	if !ok {
		return
	}
	limit, offset, err := collabPagination(c)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	collabs, err := h.service.List(c.Request.Context(), tenantID, actor, limit, offset)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]dto.CollabResponse, 0, len(collabs))
	for _, collab := range collabs {
		out = append(out, dto.ToCollabResponse(collab))
	}
	c.JSON(http.StatusOK, gin.H{"collaborations": out})
}

func (h *CollaborationHandler) Get(c *gin.Context) {
	tenantID, actor, ok := collabIdentity(c)
	if !ok {
		return
	}
	collab, err := h.service.Get(c.Request.Context(), tenantID, c.Param("id"), actor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	steps, err := h.service.ReadyTasks(c.Request.Context(), tenantID, collab.ID, actor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	stepOut := make([]dto.TaskStepResponse, 0, len(steps))
	for _, s := range steps {
		stepOut = append(stepOut, dto.ToTaskStepResponse(s))
	}
	c.JSON(http.StatusOK, gin.H{
		"collaboration": dto.ToCollabResponse(*collab),
		"steps":         stepOut,
	})
}

func (h *CollaborationHandler) Start(c *gin.Context) {
	tenantID, actor, ok := collabIdentity(c)
	if !ok {
		return
	}
	collab, err := h.service.Start(c.Request.Context(), tenantID, c.Param("id"), actor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.ToCollabResponse(*collab))
}

func (h *CollaborationHandler) Cancel(c *gin.Context) {
	tenantID, actor, ok := collabIdentity(c)
	if !ok {
		return
	}
	if err := h.service.Cancel(c.Request.Context(), tenantID, c.Param("id"), actor); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": collabdomain.CollabCanceled})
}

// collabPagination parses limit/offset query params; omitted values defer to
// the service defaults.
func collabPagination(c *gin.Context) (int, int, error) {
	limit, offset := 0, 0
	var err error
	if raw := c.Query("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid limit")
		}
	}
	if raw := c.Query("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid offset")
		}
	}
	return limit, offset, nil
}
