package handler

import (
	"context"
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/gin-gonic/gin"
)

// operationProposalReviewService is the reviewer lifecycle the handler
// consumes; the wiring adapter is *application.OperationProposalService.
type operationProposalReviewService interface {
	ListPending(ctx context.Context, tenantID, userID string) ([]domain.OperationProposal, error)
	Get(ctx context.Context, tenantID, userID, id string) (*domain.OperationProposal, error)
	StartReview(ctx context.Context, tenantID, userID, id string) error
	Approve(ctx context.Context, tenantID, userID, id, note string) error
	Reject(ctx context.Context, tenantID, userID, id, note string) error
}

// agentSelfModifyService is the member-facing gated mutation channel.
type agentSelfModifyService interface {
	GatedSelfModify(ctx context.Context, tenantID, actorID, agentID string, req application.SelfModifyRequest) (application.GatedSelfModifyResult, error)
}

// OperationProposalHandler exposes the reviewer-facing operation approval
// lifecycle (list / get / review / approve / reject) and the member-facing
// gated self-modify entry point.
type OperationProposalHandler struct {
	service  operationProposalReviewService
	agentSvc agentSelfModifyService
}

func NewOperationProposalHandler(
	service operationProposalReviewService,
	agentSvc agentSelfModifyService,
) *OperationProposalHandler {
	return &OperationProposalHandler{service: service, agentSvc: agentSvc}
}

func (h *OperationProposalHandler) List(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	proposals, err := h.service.ListPending(c.Request.Context(), tenantID, actorID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]gen.OperationProposalResponse, 0, len(proposals))
	for _, p := range proposals {
		out = append(out, gen.ToOperationProposalResponse(p))
	}
	c.JSON(http.StatusOK, gin.H{"proposals": out})
}

func (h *OperationProposalHandler) Get(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	proposal, err := h.service.Get(c.Request.Context(), tenantID, actorID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gen.ToOperationProposalResponse(*proposal))
}

func (h *OperationProposalHandler) Review(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	if err := h.service.StartReview(c.Request.Context(), tenantID, actorID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": domain.OpReviewing})
}

func (h *OperationProposalHandler) Approve(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	if err := h.service.Approve(c.Request.Context(), tenantID, actorID, c.Param("id"), ""); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": domain.OpApproved})
}

func (h *OperationProposalHandler) Reject(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	var req gen.RejectOperationProposalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, domain.ErrProposalInvalid))
		return
	}
	if err := h.service.Reject(c.Request.Context(), tenantID, actorID, c.Param("id"), req.Note); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": domain.OpRejected})
}

// SelfModify is the member-controlled mutation channel. Without an approved
// replay the operation is only proposed: 202 with the pending proposal id,
// nothing mutated. With a single-use approval from the same proposer it
// lands the change and returns the updated agent, plus a usage warning when
// post-commit accounting failed (the mutation itself succeeded).
func (h *OperationProposalHandler) SelfModify(c *gin.Context) {
	tenantID, actorID, ok := proposalIdentity(c)
	if !ok {
		return
	}
	var req application.SelfModifyRequest
	if err := decodeClosedJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	result, err := h.agentSvc.GatedSelfModify(c.Request.Context(), tenantID, actorID, c.Param("id"), req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if !result.Decision.Allowed {
		c.JSON(http.StatusAccepted, gin.H{
			"status":     "pending_approval",
			"reason":     result.Decision.Reason,
			"proposalId": result.Decision.ProposalID,
		})
		return
	}
	body := gin.H{"status": "approved", "reason": result.Decision.Reason, "agent": dtoToResponse(result.DTO)}
	if result.UsageErr != nil {
		body["usageWarning"] = result.UsageErr.Error()
	}
	c.JSON(http.StatusOK, body)
}
