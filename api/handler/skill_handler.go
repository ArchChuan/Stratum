// Package handler implements HTTP API request handlers.

package handler

import (
	"net/http"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type SkillHandler struct {
	registry *orchestrator.Registry
	logger   *zap.Logger
	gateway  *llmgateway.Gateway
}

func NewSkillHandler(registry *orchestrator.Registry, logger *zap.Logger, gateway *llmgateway.Gateway) *SkillHandler {
	return &SkillHandler{
		registry: registry,
		logger:   logger,
		gateway:  gateway,
	}
}

func (h *SkillHandler) CreateSkill(c *gin.Context) {
	var req model.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	id := uuid.New().String()
	var s skill.Skill

	switch req.Type {
	case "code":
		s = skill.NewCodeSkill(id, req.Name, req.Description, req.Code, req.Language)
	case "llm":
		s = skill.NewLLMSkill(id, req.Name, req.Description, h.gateway, h.logger)
	default:
		s = &skill.BaseSkill{
			ID:          id,
			Name:        req.Name,
			Description: req.Description,
			Type:        req.Type,
		}
	}

	h.registry.Register(c.Request.Context(), id, s)
	h.logger.Info("skill created", zap.String("id", id), zap.String("name", req.Name))

	c.JSON(http.StatusCreated, model.SkillResponse{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Type:        req.Type,
		CreatedAt:   time.Now().Format(time.RFC3339),
	})
}

func (h *SkillHandler) GetSkill(c *gin.Context) {
	id := c.Param("id")
	s, ok := h.registry.Get(id)
	if !ok {
		h.logger.Warn("skill not found", zap.String("id", id))
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "skill not found",
		})
		return
	}

	c.JSON(http.StatusOK, model.SkillResponse{
		ID:          s.GetID(),
		Name:        s.GetName(),
		Description: s.GetDescription(),
		Type:        s.GetType(),
		CreatedAt:   time.Now().Format(time.RFC3339),
	})
}

func (h *SkillHandler) GetAllSkills(c *gin.Context) {
	skills := h.registry.GetAll()
	responses := make([]model.SkillResponse, 0, len(skills))

	for _, s := range skills {
		responses = append(responses, model.SkillResponse{
			ID:          s.GetID(),
			Name:        s.GetName(),
			Description: s.GetDescription(),
			Type:        s.GetType(),
			CreatedAt:   time.Now().Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"skills": responses,
	})
}

// UpdateSkill updates an existing skill
func (h *SkillHandler) UpdateSkill(c *gin.Context) {
	id := c.Param("id")
	s, ok := h.registry.Get(id)
	if !ok {
		h.logger.Warn("skill not found", zap.String("id", id))
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "skill not found",
		})
		return
	}

	var req model.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	// Update skill properties
	switch s.GetType() {
	case "code":
		codeSkill, ok := s.(*skill.CodeSkill)
		if !ok {
			h.logger.Warn("skill is not a code skill", zap.String("id", id))
			c.JSON(http.StatusBadRequest, model.ErrorResponse{
				Code:    http.StatusBadRequest,
				Message: "skill is not a code skill",
			})
			return
		}
		codeSkill.BaseSkill.Name = req.Name
		codeSkill.BaseSkill.Description = req.Description
		codeSkill.Code = req.Code
		codeSkill.Language = req.Language

	case "llm":
		llmSkill, ok := s.(*skill.LLMSkill)
		if !ok {
			h.logger.Warn("skill is not an LLM skill", zap.String("id", id))
			c.JSON(http.StatusBadRequest, model.ErrorResponse{
				Code:    http.StatusBadRequest,
				Message: "skill is not an LLM skill",
			})
			return
		}
		llmSkill.BaseSkill.Name = req.Name
		llmSkill.BaseSkill.Description = req.Description

	default:
		h.logger.Warn("unsupported skill type", zap.String("type", s.GetType()))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "unsupported skill type",
		})
		return
	}

	h.logger.Info("skill updated", zap.String("id", id))
	c.JSON(http.StatusOK, model.SkillResponse{
		ID:          s.GetID(),
		Name:        s.GetName(),
		Description: s.GetDescription(),
		Type:        s.GetType(),
		CreatedAt:   time.Now().Format(time.RFC3339),
	})
}

// DeleteSkill removes a skill
func (h *SkillHandler) DeleteSkill(c *gin.Context) {
	id := c.Param("id")

	if err := h.registry.Remove(c.Request.Context(), id); err != nil {
		h.logger.Warn("skill not found or removal failed", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "skill not found",
		})
		return
	}

	h.logger.Info("skill deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{
		"message": "skill deleted successfully",
	})
}
