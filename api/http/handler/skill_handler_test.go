package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type fakeSkillRevisionService struct {
	created    skillapp.CreateSkillDraftInput
	activation skillapp.UpdateActivationInput
	bundle     skillapp.UpdateInstructionBundleInput
}

func (f *fakeSkillRevisionService) CreateSkillDraft(_ context.Context, input skillapp.CreateSkillDraftInput) (skillapp.SkillWorkspaceView, error) {
	f.created = input
	return skillapp.SkillWorkspaceView{
		Skill: skillapp.SkillProduct{ID: "skill-1", Name: input.Name, Description: input.Goal, Status: "draft", DraftRevisionID: "revision-1"},
		Draft: domain.SkillRevision{
			ID: "revision-1", SkillID: "skill-1", Status: domain.VersionStatusDraft,
			Capability:         domain.Capability{Goal: input.Goal, WhenToUse: input.WhenToUse},
			ActivationContract: domain.ActivationContract{Name: "complaint", Description: input.Goal},
			Instructions:       input.Instructions, Requirements: input.Requirements,
		},
	}, nil
}
func (f *fakeSkillRevisionService) GetWorkspace(ctx context.Context, _ string) (skillapp.SkillWorkspaceView, error) {
	return f.CreateSkillDraft(ctx, skillapp.CreateSkillDraftInput{Name: "complaint", Goal: "分类", WhenToUse: "投诉时", Instructions: "分类投诉"})
}
func (f *fakeSkillRevisionService) ListSkills(context.Context) ([]skillapp.SkillProduct, error) {
	return []skillapp.SkillProduct{{ID: "skill-1", Name: "complaint", Status: "draft"}}, nil
}
func (f *fakeSkillRevisionService) DeleteSkill(context.Context, string) error { return nil }
func (f *fakeSkillRevisionService) UpdateCapability(_ context.Context, _ string, input skillapp.UpdateCapabilityInput) (skillapp.SkillRevision, error) {
	return skillapp.SkillRevision{ID: "revision-1", Capability: domain.Capability{Goal: input.Goal}}, nil
}
func (f *fakeSkillRevisionService) UpdateActivation(_ context.Context, _ string, input skillapp.UpdateActivationInput) (skillapp.SkillRevision, error) {
	f.activation = input
	return skillapp.SkillRevision{ID: "revision-1", ActivationContract: domain.ActivationContract{
		Name: input.Name, Description: input.Description, InputSchema: input.InputSchema,
		OutputSchema: input.OutputSchema, Confirmed: input.Confirmed,
	}}, nil
}
func (f *fakeSkillRevisionService) UpdateInstructionBundle(_ context.Context, _ string, input skillapp.UpdateInstructionBundleInput) (skillapp.SkillRevision, error) {
	f.bundle = input
	return skillapp.SkillRevision{ID: "revision-1", Instructions: input.Instructions, Requirements: input.Requirements}, nil
}
func (f *fakeSkillRevisionService) PublishDraft(context.Context, string) (skillapp.SkillRevision, error) {
	return skillapp.SkillRevision{ID: "revision-1", RevisionNo: 1, Status: domain.VersionStatusPublished}, nil
}

func newSkillTestRouter(method, path string, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	switch method {
	case http.MethodPost:
		router.POST(path, handler)
	case http.MethodPatch:
		router.PATCH(path, handler)
	}
	return router
}

func TestSkillHandlerCreateInstructionBundle(t *testing.T) {
	service := &fakeSkillRevisionService{}
	handler := NewSkillHandler(service, zap.NewNop())
	router := newSkillTestRouter(http.MethodPost, "/skills", handler.CreateSkill)
	body, _ := json.Marshal(dto.CreateSkillRequest{
		Name: "投诉分类", Goal: "分类", WhenToUse: "用户投诉时", Instructions: "根据规则分类",
		Requirements: dto.SkillRequirements{MCPToolIDs: []string{"mcp:orders:get_order"}},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if service.created.Instructions != "根据规则分类" || len(service.created.Requirements.MCPToolIDs) != 1 {
		t.Fatalf("instruction bundle not forwarded: %#v", service.created)
	}
}

func TestSkillHandlerRejectsLegacyExecutablePayload(t *testing.T) {
	service := &fakeSkillRevisionService{}
	handler := NewSkillHandler(service, zap.NewNop())
	router := newSkillTestRouter(http.MethodPost, "/skills", handler.CreateSkill)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/skills", bytes.NewBufferString(`{"name":"legacy","type":"http","url":"https://example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected missing instruction contract to return 400, got %d", w.Code)
	}
}

func TestSkillHandlerUpdatesActivationAndInstructions(t *testing.T) {
	service := &fakeSkillRevisionService{}
	handler := NewSkillHandler(service, zap.NewNop())

	activationRouter := newSkillTestRouter(http.MethodPatch, "/skills/:id/draft/activation", handler.UpdateDraftActivation)
	body, _ := json.Marshal(dto.UpdateSkillActivationRequest{
		Name: "classify_complaint", Description: "分类", InputSchema: map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"}, Confirmed: true,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/skills/skill-1/draft/activation", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	activationRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !service.activation.Confirmed {
		t.Fatalf("activation update failed: status=%d input=%#v", w.Code, service.activation)
	}

	bundleRouter := newSkillTestRouter(http.MethodPatch, "/skills/:id/draft/instructions", handler.UpdateDraftInstructionBundle)
	body, _ = json.Marshal(dto.UpdateSkillInstructionBundleRequest{
		Instructions: "新方法", Requirements: dto.SkillRequirements{MemoryScopes: []string{"user"}},
	})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/skills/skill-1/draft/instructions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	bundleRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK || service.bundle.Instructions != "新方法" {
		t.Fatalf("instruction update failed: status=%d input=%#v", w.Code, service.bundle)
	}
}
