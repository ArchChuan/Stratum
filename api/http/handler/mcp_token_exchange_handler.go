package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
)

type mcpTokenExchanger interface {
	Exchange(context.Context, iamapp.MCPTokenExchangeRequest) (string, error)
}

type MCPTokenExchangeHandler struct {
	exchange mcpTokenExchanger
	metrics  PlatformMCPExchangeMetrics
}

type PlatformMCPExchangeMetrics interface {
	IncPlatformMCPReplayDenial(statusClass string)
	IncPlatformMCPContractMismatch(toolClass string)
}

func NewMCPTokenExchangeHandler(exchange mcpTokenExchanger) *MCPTokenExchangeHandler {
	return NewObservedMCPTokenExchangeHandler(exchange, noopMCPTokenExchangeMetrics{})
}

func NewObservedMCPTokenExchangeHandler(
	exchange mcpTokenExchanger,
	metrics PlatformMCPExchangeMetrics,
) *MCPTokenExchangeHandler {
	if metrics == nil {
		metrics = noopMCPTokenExchangeMetrics{}
	}
	return &MCPTokenExchangeHandler{exchange: exchange, metrics: metrics}
}

func (h *MCPTokenExchangeHandler) Exchange(c *gin.Context) {
	var req struct {
		InvocationToken string `json:"invocation_token" binding:"required"`
		ResourceID      string `json:"resource_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	token, err := h.exchange.Exchange(c.Request.Context(), iamapp.MCPTokenExchangeRequest{
		InvocationToken: req.InvocationToken,
		ResourceID:      req.ResourceID,
	})
	if err != nil {
		h.recordDenial(err)
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(constants.PlatformMCPAPIDelegationTokenTTL.Seconds()),
	})
}

func (h *MCPTokenExchangeHandler) recordDenial(err error) {
	if errors.Is(err, iamapp.ErrPlatformMCPInvocationReplayed) {
		h.metrics.IncPlatformMCPReplayDenial("4xx")
	}
	if errors.Is(err, iamapp.ErrPlatformMCPContractInvalid) {
		h.metrics.IncPlatformMCPContractMismatch("unknown")
	}
}

type noopMCPTokenExchangeMetrics struct{}

func (noopMCPTokenExchangeMetrics) IncPlatformMCPReplayDenial(_ string)     {}
func (noopMCPTokenExchangeMetrics) IncPlatformMCPContractMismatch(_ string) {}
