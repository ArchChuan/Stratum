package handler

import (
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/gin-gonic/gin"
)

// OAuthExchange consumes the short-lived code produced by the OAuth callback.
func (h *AuthHandler) OAuthExchange(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("code required")))
		return
	}
	if h.deps.OAuthExchangeStore == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("oauth exchange unavailable")))
		return
	}
	exchange, err := h.deps.OAuthExchangeStore.Consume(c.Request.Context(), req.Code)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, iamport.ErrOAuthExchangeInvalid) {
			status = http.StatusUnauthorized
		}
		_ = c.Error(middleware.NewHTTPError(status, errors.New("oauth exchange failed")))
		return
	}

	switch exchange.Kind {
	case iamport.OAuthExchangeLogin:
		c.JSON(http.StatusOK, gin.H{"kind": exchange.Kind, "access_token": exchange.AccessToken})
	case iamport.OAuthExchangeOnboarding:
		c.JSON(http.StatusOK, gin.H{
			"kind":             exchange.Kind,
			"onboarding_token": exchange.OnboardingToken,
			"github_login":     exchange.GitHubLogin,
			"avatar_url":       exchange.AvatarURL,
		})
	default:
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("oauth exchange failed")))
	}
}
