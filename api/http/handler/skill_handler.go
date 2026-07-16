// Package handler implements HTTP API request handlers.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SkillHandler is a thin HTTP adapter over skill application service.
type SkillHandler struct {
	svc        *skillapp.SkillService
	versionSvc skillVersionService
	logger     *zap.Logger
}

type skillVersionService interface {
	CreateSkillDraft(c context.Context, in skillapp.CreateSkillDraftInput) (skillapp.SkillWorkspaceView, error)
	GetWorkspace(c context.Context, skillID string) (skillapp.SkillWorkspaceView, error)
	UpdateCapability(c context.Context, skillID string, in skillapp.UpdateCapabilityInput) (skillapp.SkillVersion, error)
	UpdateContract(c context.Context, skillID string, in skillapp.UpdateContractInput) (skillapp.SkillVersion, error)
	UpdateImplementation(c context.Context, skillID string, in skillapp.UpdateImplementationInput) (skillapp.SkillVersion, error)
	PublishDraft(c context.Context, skillID string) (skillapp.SkillVersion, error)
}

// NewSkillHandler injects the skill application service.
func NewSkillHandler(svc *skillapp.SkillService, logger *zap.Logger, versionSvc ...skillVersionService) *SkillHandler {
	var vs skillVersionService
	if len(versionSvc) > 0 {
		vs = versionSvc[0]
	}
	return &SkillHandler{svc: svc, versionSvc: vs, logger: logger}
}

func (h *SkillHandler) CreateSkill(c *gin.Context) {
	var req dto.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if isCapabilityCreateReq(req) && h.versionSvc != nil {
		view, err := h.versionSvc.CreateSkillDraft(c.Request.Context(), skillapp.CreateSkillDraftInput{
			Name:           req.Name,
			Goal:           req.Goal,
			WhenToUse:      req.WhenToUse,
			SampleInput:    req.SampleInput,
			ExpectedOutput: req.ExpectedOutput,
		})
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusCreated, workspaceToResponse(view))
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

func (h *SkillHandler) PublishSkill(c *gin.Context) {
	if h.versionSvc == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("skill version service not configured")))
		return
	}
	version, err := h.versionSvc.PublishDraft(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, versionToResponse(version))
}

func (h *SkillHandler) GetSkill(c *gin.Context) {
	view, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, viewToResponse(view))
}

func (h *SkillHandler) GetSkillWorkspace(c *gin.Context) {
	if h.versionSvc == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("skill version service not configured")))
		return
	}
	view, err := h.versionSvc.GetWorkspace(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, workspaceToResponse(view))
}

func (h *SkillHandler) UpdateDraftCapability(c *gin.Context) {
	if h.versionSvc == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("skill version service not configured")))
		return
	}
	var req dto.UpdateSkillCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	version, err := h.versionSvc.UpdateCapability(c.Request.Context(), c.Param("id"), skillapp.UpdateCapabilityInput{
		Goal:       req.Goal,
		WhenToUse:  req.WhenToUse,
		InputSpec:  req.InputSpec,
		OutputSpec: req.OutputSpec,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, versionToResponse(version))
}

func (h *SkillHandler) UpdateDraftContract(c *gin.Context) {
	if h.versionSvc == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("skill version service not configured")))
		return
	}
	var req dto.UpdateSkillContractRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	version, err := h.versionSvc.UpdateContract(c.Request.Context(), c.Param("id"), skillapp.UpdateContractInput{
		ToolName:        req.ToolName,
		Description:     req.Description,
		InputSchema:     req.InputSchema,
		OutputSchema:    req.OutputSchema,
		CallingGuidance: req.CallingGuidance,
		Confirmed:       req.Confirmed,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, versionToResponse(version))
}

func (h *SkillHandler) UpdateDraftImplementation(c *gin.Context) {
	if h.versionSvc == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("skill version service not configured")))
		return
	}
	var req dto.UpdateSkillImplementationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	version, err := h.versionSvc.UpdateImplementation(c.Request.Context(), c.Param("id"), skillapp.UpdateImplementationInput{
		Mode:        req.Mode,
		Source:      req.Source,
		Runtime:     req.Runtime,
		Permissions: req.Permissions,
		SecretRefs:  req.SecretRefs,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, versionToResponse(version))
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

func (h *SkillHandler) ExecuteSkill(c *gin.Context) {
	var req dto.ExecuteSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	result, err := h.svc.RunSkillTest(c.Request.Context(), c.Param("id"), req.Input, req.TraceID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.ExecuteSkillResponse{
		Result:     result.Output,
		TraceID:    result.TraceID,
		DurationMs: result.Duration.Milliseconds(),
	})
}

func (h *SkillHandler) ExecuteDraftSkill(c *gin.Context) {
	var req dto.ExecuteDraftSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	result, err := h.svc.RunDraftSkill(c.Request.Context(), inputFromCreateReq(req.Skill), req.Input, req.TraceID)
	if err != nil {
		if reasons, ok := skillapp.IsAnalysisError(err); ok {
			_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New(reasons[0])))
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.ExecuteSkillResponse{
		Result:     result.Output,
		TraceID:    result.TraceID,
		DurationMs: result.Duration.Milliseconds(),
	})
}

// inputFromCreateReq translates the HTTP DTO into the application input.
func inputFromCreateReq(r dto.CreateSkillRequest) skillapp.SkillInput {
	typ := r.Type
	promptTemplate := r.PromptTemplate
	if typ == "" {
		typ = "prompt"
		promptTemplate = unifiedPromptTemplate(r)
	}
	return skillapp.SkillInput{
		Name:           r.Name,
		Description:    r.Description,
		Type:           typ,
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
		PromptTemplate: promptTemplate,
	}
}

func isCapabilityCreateReq(r dto.CreateSkillRequest) bool {
	return strings.TrimSpace(r.Goal) != "" || strings.TrimSpace(r.WhenToUse) != ""
}

func workspaceToResponse(v skillapp.SkillWorkspaceView) dto.SkillWorkspaceResponse {
	return dto.SkillWorkspaceResponse{
		Skill: dto.SkillProductResponse{
			ID:              v.Skill.ID,
			Name:            v.Skill.Name,
			Description:     v.Skill.Description,
			Status:          v.Skill.Status,
			ActiveVersionID: v.Skill.ActiveVersionID,
			DraftVersionID:  v.Skill.DraftVersionID,
		},
		Draft: versionToResponse(v.Draft),
	}
}

func versionToResponse(v skillapp.SkillVersion) dto.SkillVersionResponse {
	return dto.SkillVersionResponse{
		ID:             v.ID,
		SkillID:        v.SkillID,
		VersionNo:      v.VersionNo,
		Status:         string(v.Status),
		Capability:     structToMap(v.Capability),
		ToolContract:   structToMap(v.ToolContract),
		Implementation: structToMap(v.Implementation),
		TestBaseline:   v.TestBaseline,
		PublishChecks:  v.PublishChecks,
	}
}

func structToMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func unifiedPromptTemplate(r dto.CreateSkillRequest) string {
	parts := []string{
		"你是一个可被 Agent 调用的业务技能。",
	}
	if desc := strings.TrimSpace(r.Description); desc != "" {
		parts = append(parts, "能力目标："+desc)
	}
	if expectedInput := strings.TrimSpace(r.ExpectedInput); expectedInput != "" {
		parts = append(parts, "期望输入："+expectedInput)
	}
	if expectedOutput := strings.TrimSpace(r.ExpectedOutput); expectedOutput != "" {
		parts = append(parts, "期望输出："+expectedOutput)
	}
	if sampleCases := strings.TrimSpace(r.SampleCases); sampleCases != "" {
		parts = append(parts, "测试样例："+sampleCases)
	}
	parts = append(parts,
		"请根据调用输入完成该能力，并输出清晰、可复用的结果。",
		"调用输入：{{.input}}",
	)
	return strings.Join(parts, "\n\n")
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
