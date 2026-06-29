// Package handler implements HTTP API request handlers.
package handler

import (
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SkillHandler is a thin HTTP adapter over skill application service.
type SkillHandler struct {
	svc    *skillapp.SkillService
	logger *zap.Logger
}

// NewSkillHandler injects the skill application service.
func NewSkillHandler(svc *skillapp.SkillService, logger *zap.Logger) *SkillHandler {
	return &SkillHandler{svc: svc, logger: logger}
}

func (h *SkillHandler) CreateSkill(c *gin.Context) {
	var req dto.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	view, err := h.svc.Create(c.Request.Context(), inputFromCreateReq(req))
	if err != nil {
		if reasons, ok := skillapp.IsAnalysisError(err); ok {
			_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New(reasons[0])))
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, viewToResponse(view))
}

func (h *SkillHandler) GetSkill(c *gin.Context) {
	view, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, viewToResponse(view))
}

func (h *SkillHandler) GetAllSkills(c *gin.Context) {
	views, err := h.svc.List(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]dto.SkillResponse, 0, len(views))
	for _, v := range views {
		out = append(out, viewToResponse(v))
	}
	c.JSON(http.StatusOK, gin.H{"skills": out})
}

func (h *SkillHandler) UpdateSkill(c *gin.Context) {
	var req dto.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	view, err := h.svc.Update(c.Request.Context(), c.Param("id"), inputFromCreateReq(req))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, viewToResponse(view))
}

func (h *SkillHandler) DeleteSkill(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "skill deleted successfully"})
}

// RunSkill executes a code skill on demand. POST /skills/:id/run
func (h *SkillHandler) RunSkill(c *gin.Context) {
	var req dto.RunSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	tid, _ := c.Get("tenant_id")
	tenantID, _ := tid.(string)

	out, err := h.svc.Run(c.Request.Context(), c.Param("id"), tenantID, req.Input)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.RunSkillResponse{Output: out})
}

// inputFromCreateReq translates the HTTP DTO into the application input.
func inputFromCreateReq(r dto.CreateSkillRequest) skillapp.SkillInput {
	return skillapp.SkillInput{
		Name:           r.Name,
		Description:    r.Description,
		Type:           r.Type,
		Code:           r.Code,
		Language:       r.Language,
		SystemPrompt:   r.SystemPrompt,
		Model:          r.Model,
		Temperature:    r.Temperature,
		MaxTokens:      r.MaxTokens,
		URL:            r.URL,
		Method:         r.Method,
		Headers:        r.Headers,
		BodyTemplate:   r.BodyTemplate,
		TimeoutSec:     r.TimeoutSec,
		PromptTemplate: r.PromptTemplate,
	}
}

func viewToResponse(v skillapp.SkillView) dto.SkillResponse {
	return dto.SkillResponse{
		ID:          v.ID,
		Name:        v.Name,
		Description: v.Description,
		Type:        v.Type,
		Config:      v.Config,
		CreatedAt:   v.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
