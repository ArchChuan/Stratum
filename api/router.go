package api

import (
	"github.com/byteBuilderX/ClawHermes-AI-Go/api/handler"
	"github.com/byteBuilderX/ClawHermes-AI-Go/api/middleware"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/config"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func NewRouter(cfg *config.Config, registry *orchestrator.Registry, logger *zap.Logger, gateway *llmgateway.Gateway) *gin.Engine {
	router := gin.Default()

	router.Use(middleware.CORSMiddleware())
	router.Use(middleware.ErrorHandler(logger))

	skillHandler := handler.NewSkillHandler(registry, logger, gateway)
	
	// Initialize agent registry
	agentRegistry := agent.NewRegistry(registry, gateway, logger)
	agentHandler := handler.NewAgentHandler(agentRegistry, logger, gateway)

	skills := router.Group("/skills")
	{
		skills.POST("", skillHandler.CreateSkill)
		skills.GET("/:id", skillHandler.GetSkill)
		// 移除了直接执行技能的接口 skills.POST("/:id/execute", skillHandler.ExecuteSkill)
	}

	// Agent routes
	agents := router.Group("/agents")
	{
		agents.POST("", agentHandler.CreateAgent)
		agents.GET("", agentHandler.ListAgents)
		agents.GET("/:id", agentHandler.GetAgent)
		agents.POST("/:id/execute", agentHandler.ExecuteAgent)
	}

	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	return router
}