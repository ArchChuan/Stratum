package handler

import (
	"context"
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
}

func NewMCPTokenExchangeHandler(exchange mcpTokenExchanger) *MCPTokenExchangeHandler {
	return &MCPTokenExchangeHandler{exchange: exchange}
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
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(constants.PlatformMCPAPIDelegationTokenTTL.Seconds()),
	})
}
