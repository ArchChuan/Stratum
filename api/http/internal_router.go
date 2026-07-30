package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
)

type internalTokenExchanger interface {
	Exchange(context.Context, iamapp.MCPTokenExchangeRequest) (string, error)
}

type internalDelegationVerifier interface {
	VerifyAPIDelegation(string) (*platformmcp.APIDelegationClaims, error)
}

type InternalRouterDeps struct {
	Exchange     internalTokenExchanger
	Tokens       internalDelegationVerifier
	Capabilities handler.PlatformAssistantCapabilityDeps
	Logger       *zap.Logger
}

func NewInternalRouter(deps InternalRouterDeps) (*gin.Engine, error) {
	if deps.Exchange == nil {
		return nil, errors.New("internal router token exchange is not configured")
	}
	if deps.Tokens == nil {
		return nil, errors.New("internal router delegation verifier is not configured")
	}
	if deps.Logger == nil {
		return nil, errors.New("internal router logger is not configured")
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.BodyLimit(constants.MaxRequestBodyBytes))
	router.Use(otelgin.Middleware("stratum-internal-api"))
	router.Use(middleware.TraceMiddleware(deps.Logger))
	router.Use(middleware.ErrorHandler(deps.Logger))
	router.Use(middleware.SecurityHeaders())
	router.Use(middleware.RequirePlatformMCPIdentity())

	exchange := handler.NewMCPTokenExchangeHandler(deps.Exchange)
	capabilities, err := handler.NewPlatformAssistantCapabilityHandler(deps.Capabilities)
	if err != nil {
		return nil, err
	}
	router.POST("/internal/platform-mcp/token/exchange", exchange.Exchange)
	delegated := router.Group("", middleware.RequireDelegatedScope(deps.Tokens))
	delegated.POST("/internal/platform-assistant/docs/search", capabilities.SearchDocs)
	delegated.POST("/internal/platform-assistant/diagnostics", capabilities.DiagnoseTenant)
	delegated.POST("/internal/platform-assistant/proposals", capabilities.ProposeResourceChange)
	router.GET("/internal/livez", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	return router, nil
}
