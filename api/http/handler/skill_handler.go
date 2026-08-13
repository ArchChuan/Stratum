package handler

import (
	"context"
	"encoding/json"
	"net/http"

	gen "github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
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
	DeleteSkill(context.Context, string, string) error
	UpdateCapability(context.Context, string, skillapp.UpdateCapabilityInput) (skillapp.SkillRevision, error)
	UpdateActivation(context.Context, string, skillapp.UpdateActivationInput) (skillapp.SkillRevision, error)
	UpdateInstructionBundle(context.Context, string, skillapp.UpdateInstructionBundleInput) (skillapp.SkillRevision, error)
	PublishDraft(context.Context, string, string) (skillapp.SkillRevision, error)
	SetEditors(context.Context, string, string, []string) error
}

func NewSkillHandler(service skillRevisionService, logger *zap.Logger) *SkillHandler {
	return &SkillHandler{service: service, logger: logger}
}

func (h *SkillHandler) CreateSkill(c *gin.Context) {
	var req gen.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid instruction Skill request", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	view, err := h.service.CreateSkillDraft(c.Request.Context(), skillapp.CreateSkillDraftInput{
		Name: req.Name, Goal: req.Goal, WhenToUse: req.WhenToUse,
		SampleInput: req.SampleInput, ExpectedOutput: req.ExpectedOutput,
		Instructions: req.Instructions,
		ActorID:      actorID, Editors: req.Editors,
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
	out := make([]gen.SkillProductResponse, 0, len(items))
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
	var req gen.UpdateSkillCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	revision, err := h.service.UpdateCapability(c.Request.Context(), c.Param("id"), skillapp.UpdateCapabilityInput{
		Goal: req.Goal, WhenToUse: req.WhenToUse, InputSpec: req.InputSpec, OutputSpec: req.OutputSpec,
		ActorID: actorID,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revisionToResponse(revision))
}

func (h *SkillHandler) UpdateDraftActivation(c *gin.Context) {
	var req gen.UpdateSkillActivationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	revision, err := h.service.UpdateActivation(c.Request.Context(), c.Param("id"), skillapp.UpdateActivationInput{
		Name: req.Name, Description: req.Description, InputSchema: req.InputSchema,
		OutputSchema: req.OutputSchema, Confirmed: req.Confirmed, ActorID: actorID,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revisionToResponse(revision))
}

func (h *SkillHandler) UpdateDraftInstructionBundle(c *gin.Context) {
	var req gen.UpdateSkillInstructionBundleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	revision, err := h.service.UpdateInstructionBundle(c.Request.Context(), c.Param("id"), skillapp.UpdateInstructionBundleInput{
		Instructions: req.Instructions, ActorID: actorID,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revisionToResponse(revision))
}

func (h *SkillHandler) PublishSkill(c *gin.Context) {
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	revision, err := h.service.PublishDraft(c.Request.Context(), c.Param("id"), actorID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revisionToResponse(revision))
}

func (h *SkillHandler) DeleteSkill(c *gin.Context) {
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	if err := h.service.DeleteSkill(c.Request.Context(), c.Param("id"), actorID); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "skill deleted successfully"})
}

// SetSkillEditors replaces the granted editor set of a skill resource
// (creator/owner only, editor ids must hold role admin/owner).
func (h *SkillHandler) SetSkillEditors(c *gin.Context) {
	var req struct {
		EditorIDs []string `json:"editorIds" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	if err := h.service.SetEditors(c.Request.Context(), c.Param("id"), actorID, req.EditorIDs); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "editors updated"})
}

func productToResponse(value skillapp.SkillProduct) gen.SkillProductResponse {
	return gen.SkillProductResponse{
		ID: value.ID, Name: value.Name, Description: value.Description, Status: value.Status,
		ActiveRevisionID: value.ActiveRevisionID, DraftRevisionID: value.DraftRevisionID,
	}
}

func workspaceToResponse(value skillapp.SkillWorkspaceView) gen.SkillWorkspaceResponse {
	return gen.SkillWorkspaceResponse{Skill: productToResponse(value.Skill), Draft: revisionToResponse(value.Draft), Editors: value.Editors}
}

func revisionToResponse(value skillapp.SkillRevision) gen.SkillRevisionResponse {
	//nolint:gosec // 版本号不可能溢出 int32(proto 契约)
	return gen.SkillRevisionResponse{
		ID: value.ID, SkillID: value.SkillID, RevisionNo: int32(value.RevisionNo), Status: string(value.Status),
		Capability: structToMap(value.Capability), ActivationContract: structToMap(value.ActivationContract),
		Instructions: value.Instructions, PublishChecks: value.PublishChecks,
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
