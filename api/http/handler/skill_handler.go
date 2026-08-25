package handler

import (
	"context"
	"net/http"
	"strings"

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
	GetWorkspace(context.Context, string, string) (skillapp.SkillWorkspaceView, error)
	ListSkills(context.Context) ([]skillapp.SkillProduct, error)
	DeleteSkill(context.Context, string, string) error
	UpdateDraftBundle(context.Context, string, string, skillapp.UpdateDraftBundleInput) (skillapp.SkillWorkspaceView, error)
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
		Name: req.Name, Description: req.Description, Instructions: req.Instructions,
		ActorID: actorID, Editors: req.Editors,
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
	actorID, _ := userIDFromCtx(c)
	view, err := h.service.GetWorkspace(c.Request.Context(), c.Param("id"), actorID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, workspaceToResponse(view))
}

// UpdateDraft saves the draft's name/description/instructions bundle. The
// editor actor's qualification is re-validated inside the write transaction.
// Direct edits carry no baseline content hash (empty expectedContentHash).
func (h *SkillHandler) UpdateDraft(c *gin.Context) {
	var req gen.UpdateSkillDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	view, err := h.service.UpdateDraftBundle(c.Request.Context(), c.Param("id"), "",
		skillapp.UpdateDraftBundleInput{
			Name: req.Name, Description: req.Description, Instructions: req.Instructions,
			ActorID: actorID,
		})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revisionToResponse(view.Draft))
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
// (creator/owner only; any tenant member may be granted editor, whitelist).
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
		// builtin: 前缀即系统内置 skill;前端据此对普通 agent 的选择列过滤。
		IsSystem: strings.HasPrefix(value.ID, "builtin:"),
	}
}

func workspaceToResponse(value skillapp.SkillWorkspaceView) gen.SkillWorkspaceResponse {
	return gen.SkillWorkspaceResponse{Skill: productToResponse(value.Skill), Draft: revisionToResponse(value.Draft), Editors: value.Editors}
}

func revisionToResponse(value skillapp.SkillRevision) gen.SkillRevisionResponse {
	//nolint:gosec // 版本号不可能溢出 int32(proto 契约)
	return gen.SkillRevisionResponse{
		ID: value.ID, SkillID: value.SkillID, RevisionNo: int32(value.RevisionNo), Status: string(value.Status),
		Name: value.Name, Description: value.Description,
		Instructions: value.Instructions, PublishChecks: value.PublishChecks,
	}
}
