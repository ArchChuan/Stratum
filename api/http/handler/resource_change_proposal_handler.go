package handler

import (
	"context"
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/gin-gonic/gin"
)

type resourceChangeProposalService interface {
	Get(context.Context, string, string, string) (domain.ResourceChangeProposal, error)
	ListEvents(context.Context, string, string, string) ([]domain.ProposalEvent, error)
	UpdateDraft(context.Context, agentapp.UpdateProposalInput) (domain.ResourceChangeProposal, error)
	Cancel(context.Context, string, string, string) error
	ConfirmAndApply(context.Context, string, string, string) (domain.ResourceChangeProposal, error)
}

type ResourceChangeProposalHandler struct {
	service resourceChangeProposalService
}

func NewResourceChangeProposalHandler(service resourceChangeProposalService) *ResourceChangeProposalHandler {
	return &ResourceChangeProposalHandler{service: service}
}

func (h *ResourceChangeProposalHandler) Get(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	proposal, err := h.service.Get(c.Request.Context(), tenantID, actorID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	events, err := h.service.ListEvents(c.Request.Context(), tenantID, actorID, proposal.ID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.NewResourceChangeProposalResponse(proposal, events))
}

func (h *ResourceChangeProposalHandler) Update(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	var request dto.UpdateResourceChangeProposalRequest
	if err := decodeClosedJSON(c, &request); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	proposal, err := h.service.UpdateDraft(c.Request.Context(), agentapp.UpdateProposalInput{
		TenantID: tenantID, ActorID: actorID, ProposalID: c.Param("id"), Payload: request.Payload,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.NewResourceChangeProposalResponse(proposal, nil))
}

func (h *ResourceChangeProposalHandler) Cancel(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	if err := h.service.Cancel(c.Request.Context(), tenantID, actorID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": domain.StatusCancelled})
}

func (h *ResourceChangeProposalHandler) Confirm(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	proposal, err := h.service.ConfirmAndApply(c.Request.Context(), tenantID, c.Param("id"), actorID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.NewResourceChangeProposalResponse(proposal, nil))
}

func proposalIdentity(c *gin.Context) (string, string, bool) {
	tenantID, tenantOK := tenantIDFromCtx(c)
	actorID, actorOK := userIDFromCtx(c)
	if !tenantOK || !actorOK || actorID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, domain.ErrProposalForbidden))
		return "", "", false
	}
	return tenantID, actorID, true
}
