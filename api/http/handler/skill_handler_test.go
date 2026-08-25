package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gen "github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type fakeSkillRevisionService struct {
	created skillapp.CreateSkillDraftInput
	draft   skillapp.UpdateDraftBundleInput
}

func (f *fakeSkillRevisionService) CreateSkillDraft(_ context.Context, input skillapp.CreateSkillDraftInput) (skillapp.SkillWorkspaceView, error) {
	f.created = input
	return skillapp.SkillWorkspaceView{
		Skill: skillapp.SkillProduct{ID: "skill-1", Name: input.Name, Description: input.Description, Status: "draft", DraftRevisionID: "revision-1"},
		Draft: domain.SkillRevision{
			ID: "revision-1", SkillID: "skill-1", Status: domain.VersionStatusDraft,
			Name: input.Name, Description: input.Description, Instructions: input.Instructions,
		},
	}, nil
}
func (f *fakeSkillRevisionService) GetWorkspace(ctx context.Context, _, _ string) (skillapp.SkillWorkspaceView, error) {
	return f.CreateSkillDraft(ctx, skillapp.CreateSkillDraftInput{Name: "complaint", Description: "分类", Instructions: "分类投诉"})
}
func (f *fakeSkillRevisionService) ListSkills(context.Context) ([]skillapp.SkillProduct, error) {
	return []skillapp.SkillProduct{{ID: "skill-1", Name: "complaint", Status: "draft"}}, nil
}
func (f *fakeSkillRevisionService) DeleteSkill(context.Context, string, string) error { return nil }
func (f *fakeSkillRevisionService) UpdateDraftBundle(_ context.Context, _ string, _ string, input skillapp.UpdateDraftBundleInput) (skillapp.SkillWorkspaceView, error) {
	f.draft = input
	return skillapp.SkillWorkspaceView{
		Skill: skillapp.SkillProduct{ID: "skill-1", Name: input.Name, Description: input.Description, Status: "draft", DraftRevisionID: "revision-1"},
		Draft: domain.SkillRevision{
			ID: "revision-1", SkillID: "skill-1", Status: domain.VersionStatusDraft,
			Name: input.Name, Description: input.Description, Instructions: input.Instructions,
		},
	}, nil
}
func (f *fakeSkillRevisionService) PublishDraft(context.Context, string, string) (skillapp.SkillRevision, error) {
	return skillapp.SkillRevision{ID: "revision-1", RevisionNo: 1, Status: domain.VersionStatusPublished}, nil
}
func (f *fakeSkillRevisionService) SetEditors(context.Context, string, string, []string) error {
	return nil
}

func newSkillTestRouter(method, path string, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	// Write handlers resolve the actor via ContextKeySub; without it they
	// would all 401 before reaching the service fake.
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeySub, "user-1")
		c.Next()
	})
	switch method {
	case http.MethodPost:
		router.POST(path, handler)
	case http.MethodPut:
		router.PUT(path, handler)
	case http.MethodPatch:
		router.PATCH(path, handler)
	}
	return router
}

func TestSkillHandlerCreateSkill(t *testing.T) {
	service := &fakeSkillRevisionService{}
	handler := NewSkillHandler(service, zap.NewNop())
	router := newSkillTestRouter(http.MethodPost, "/skills", handler.CreateSkill)
	body, _ := json.Marshal(gen.CreateSkillRequest{
		Name: "投诉分类", Description: "分类用户投诉", Instructions: "根据规则分类",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if service.created.Instructions != "根据规则分类" || service.created.Description != "分类用户投诉" {
		t.Fatalf("create input not forwarded: %#v", service.created)
	}
}

func TestSkillHandlerRejectsIncompleteCreate(t *testing.T) {
	service := &fakeSkillRevisionService{}
	handler := NewSkillHandler(service, zap.NewNop())
	router := newSkillTestRouter(http.MethodPost, "/skills", handler.CreateSkill)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/skills", bytes.NewBufferString(`{"name":"legacy"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected missing required fields to return 400, got %d", w.Code)
	}
}

func TestSkillHandlerSetEditors(t *testing.T) {
	service := &fakeSkillRevisionService{}
	handler := NewSkillHandler(service, zap.NewNop())
	router := newSkillTestRouter(http.MethodPut, "/skills/:id/editors", handler.SetSkillEditors)
	body := bytes.NewBufferString(`{"editorIds":["user-2","user-3"]}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/skills/skill-1/editors", body)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSkillHandlerUpdateDraft(t *testing.T) {
	service := &fakeSkillRevisionService{}
	handler := NewSkillHandler(service, zap.NewNop())
	router := newSkillTestRouter(http.MethodPatch, "/skills/:id/draft", handler.UpdateDraft)
	body, _ := json.Marshal(gen.UpdateSkillDraftRequest{
		Name: "投诉分类", Description: "分类用户投诉", Instructions: "新方法",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/skills/skill-1/draft", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if service.draft.Instructions != "新方法" || service.draft.Description != "分类用户投诉" {
		t.Fatalf("draft bundle not forwarded: %#v", service.draft)
	}
}
