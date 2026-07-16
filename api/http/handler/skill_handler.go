package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type SkillHandler struct {
	service skillRevisionService
	logger  *zap.Logger
}

type skillRevisionService interface {
	CreateSkillDraft(context.Context, skillapp.CreateSkillDraftInput) (skillapp.SkillWorkspaceView, error)
	GetWorkspace(context.Context, string) (skillapp.SkillWorkspaceView, error)
	ListSkills(context.Context) ([]skillapp.SkillProduct, error)
	DeleteSkill(context.Context, string) error
	UpdateCapability(context.Context, string, skillapp.UpdateCapabilityInput) (skillapp.SkillRevision, error)
	UpdateActivation(context.Context, string, skillapp.UpdateActivationInput) (skillapp.SkillRevision, error)
	UpdateInstructionBundle(context.Context, string, skillapp.UpdateInstructionBundleInput) (skillapp.SkillRevision, error)
	PublishDraft(context.Context, string) (skillapp.SkillRevision, error)
}

func NewSkillHandler(service skillRevisionService, logger *zap.Logger) *SkillHandler {
	return &SkillHandler{service: service, logger: logger}
}

func (h *SkillHandler) CreateSkill(c *gin.Context) {
	var req dto.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid instruction Skill request", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	view, err := h.service.CreateSkillDraft(c.Request.Context(), skillapp.CreateSkillDraftInput{
		Name: req.Name, Goal: req.Goal, WhenToUse: req.WhenToUse,
		SampleInput: req.SampleInput, ExpectedOutput: req.ExpectedOutput,
		Instructions: req.Instructions, Requirements: requirementsFromDTO(req.Requirements),
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, workspaceToResponse(view))
}

func (h *SkillHandler) GetAllSkills(c *gin.Context) {
	items, err := h.service.ListSkills(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]dto.SkillProductResponse, 0, len(items))
	for _, item := range items {
		out = append(out, productToResponse(item))
	}
	c.JSON(http.StatusOK, gin.H{"skills": out})
}

func (h *SkillHandler) GetSkill(c *gin.Context) { h.GetSkillWorkspace(c) }

func (h *SkillHandler) GetSkillWorkspace(c *gin.Context) {
	view, err := h.service.GetWorkspace(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, workspaceToResponse(view))
}

func (h *SkillHandler) UpdateDraftCapability(c *gin.Context) {
	var req dto.UpdateSkillCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	revision, err := h.service.UpdateCapability(c.Request.Context(), c.Param("id"), skillapp.UpdateCapabilityInput{
		Goal: req.Goal, WhenToUse: req.WhenToUse, InputSpec: req.InputSpec, OutputSpec: req.OutputSpec,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revisionToResponse(revision))
}

func (h *SkillHandler) UpdateDraftActivation(c *gin.Context) {
	var req dto.UpdateSkillActivationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	revision, err := h.service.UpdateActivation(c.Request.Context(), c.Param("id"), skillapp.UpdateActivationInput{
		Name: req.Name, Description: req.Description, InputSchema: req.InputSchema,
		OutputSchema: req.OutputSchema, Confirmed: req.Confirmed,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revisionToResponse(revision))
}

func (h *SkillHandler) UpdateDraftInstructionBundle(c *gin.Context) {
	var req dto.UpdateSkillInstructionBundleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	revision, err := h.service.UpdateInstructionBundle(c.Request.Context(), c.Param("id"), skillapp.UpdateInstructionBundleInput{
		Instructions: req.Instructions, Requirements: requirementsFromDTO(req.Requirements),
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revisionToResponse(revision))
}

func (h *SkillHandler) PublishSkill(c *gin.Context) {
	revision, err := h.service.PublishDraft(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revisionToResponse(revision))
}

func (h *SkillHandler) DeleteSkill(c *gin.Context) {
	if err := h.service.DeleteSkill(c.Request.Context(), c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "skill deleted successfully"})
}

func requirementsFromDTO(value dto.SkillRequirements) skilldomain.Requirements {
	return skilldomain.Requirements{
		MCPToolIDs: value.MCPToolIDs, KnowledgeWorkspaceIDs: value.KnowledgeWorkspaceIDs,
		MemoryScopes: value.MemoryScopes,
	}
}

func productToResponse(value skillapp.SkillProduct) dto.SkillProductResponse {
	return dto.SkillProductResponse{
		ID: value.ID, Name: value.Name, Description: value.Description, Status: value.Status,
		ActiveRevisionID: value.ActiveRevisionID, DraftRevisionID: value.DraftRevisionID,
	}
}

func workspaceToResponse(value skillapp.SkillWorkspaceView) dto.SkillWorkspaceResponse {
	return dto.SkillWorkspaceResponse{Skill: productToResponse(value.Skill), Draft: revisionToResponse(value.Draft)}
}

func revisionToResponse(value skillapp.SkillRevision) dto.SkillRevisionResponse {
	return dto.SkillRevisionResponse{
		ID: value.ID, SkillID: value.SkillID, RevisionNo: value.RevisionNo, Status: string(value.Status),
		Capability: structToMap(value.Capability), ActivationContract: structToMap(value.ActivationContract),
		Instructions: value.Instructions, Requirements: structToMap(value.Requirements), PublishChecks: value.PublishChecks,
	}
}

func structToMap(value any) map[string]any {
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}
