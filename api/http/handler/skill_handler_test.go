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
	skillport "github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SkillHandler unit tests cover input validation only.
// DB operations require integration tests with a real pgxpool.Pool.

func newTestSkillHandler() *SkillHandler {
	svc := skillapp.NewSkillService(nil, nil, zap.NewNop())
	return NewSkillHandler(svc, zap.NewNop())
}

type fakeSkillRunner struct {
	gotID    string
	gotInput any
}

func (f *fakeSkillRunner) RunSkill(_ context.Context, skillID string, input any, traceID string) (skillapp.SkillTestResult, error) {
	f.gotID = skillID
	f.gotInput = input
	return skillapp.SkillTestResult{
		TraceID: traceID,
		SkillID: skillID,
		Output:  map[string]any{"answer": "ok"},
	}, nil
}

type promptDraftFactory struct{}

func (f *promptDraftFactory) Build(id string, in skillport.SkillInput) (domain.Skill, error) {
	return executors.NewPromptSkill(id, in.Name, in.Description, in.PromptTemplate), nil
}

type fakeVersionService struct {
	gotCreate         skillapp.CreateSkillDraftInput
	gotContract       skillapp.UpdateContractInput
	gotImplementation skillapp.UpdateImplementationInput
	publishErr        error
}

func (f *fakeVersionService) CreateSkillDraft(_ context.Context, in skillapp.CreateSkillDraftInput) (skillapp.SkillWorkspaceView, error) {
	f.gotCreate = in
	return skillapp.SkillWorkspaceView{
		Skill: skillapp.SkillProduct{
			ID:             "skill-1",
			Name:           in.Name,
			Description:    in.Goal,
			Status:         "draft",
			DraftVersionID: "draft-1",
		},
		Draft: skillapp.SkillVersion{
			ID:      "draft-1",
			SkillID: "skill-1",
			Status:  domain.VersionStatusDraft,
			Capability: domain.Capability{
				Goal:      in.Goal,
				WhenToUse: in.WhenToUse,
			},
			ToolContract: domain.ToolContract{
				ToolName:     "classify_complaint",
				Description:  in.Goal,
				InputSchema:  map[string]any{"type": "object"},
				OutputSchema: map[string]any{"type": "object"},
			},
			Implementation: domain.Implementation{Mode: "prompt", Source: map[string]any{"promptTemplate": "x"}},
		},
	}, nil
}

func (f *fakeVersionService) GetWorkspace(_ context.Context, _ string) (skillapp.SkillWorkspaceView, error) {
	return f.CreateSkillDraft(context.Background(), skillapp.CreateSkillDraftInput{
		Name:           "投诉分类",
		Goal:           "判断客户投诉类型",
		WhenToUse:      "用户表达投诉时",
		SampleInput:    "快递没更新",
		ExpectedOutput: "物流问题",
	})
}

func (f *fakeVersionService) UpdateCapability(_ context.Context, _ string, in skillapp.UpdateCapabilityInput) (skillapp.SkillVersion, error) {
	return skillapp.SkillVersion{
		ID:      "draft-1",
		SkillID: "skill-1",
		Status:  domain.VersionStatusDraft,
		Capability: domain.Capability{
			Goal:       in.Goal,
			WhenToUse:  in.WhenToUse,
			InputSpec:  in.InputSpec,
			OutputSpec: in.OutputSpec,
		},
	}, nil
}

func (f *fakeVersionService) UpdateContract(_ context.Context, _ string, in skillapp.UpdateContractInput) (skillapp.SkillVersion, error) {
	f.gotContract = in
	return skillapp.SkillVersion{
		ID:      "draft-1",
		SkillID: "skill-1",
		Status:  domain.VersionStatusDraft,
		ToolContract: domain.ToolContract{
			ToolName:        in.ToolName,
			Description:     in.Description,
			InputSchema:     in.InputSchema,
			OutputSchema:    in.OutputSchema,
			CallingGuidance: in.CallingGuidance,
			Confirmed:       in.Confirmed,
		},
	}, nil
}

func (f *fakeVersionService) UpdateImplementation(_ context.Context, _ string, in skillapp.UpdateImplementationInput) (skillapp.SkillVersion, error) {
	f.gotImplementation = in
	return skillapp.SkillVersion{
		ID:             "draft-1",
		SkillID:        "skill-1",
		Status:         domain.VersionStatusDraft,
		Implementation: domain.Implementation{Mode: in.Mode, Source: in.Source, Runtime: in.Runtime},
	}, nil
}

func TestSkillHandlerUpdateDraftContract_ReturnsDraftVersion(t *testing.T) {
	versionSvc := &fakeVersionService{}
	h := NewSkillHandler(newTestSkillHandler().svc, zap.NewNop(), versionSvc)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.PATCH("/skills/:id/draft/contract", h.UpdateDraftContract)

	body, _ := json.Marshal(dto.UpdateSkillContractRequest{
		ToolName:        "classify_complaint",
		Description:     "判断客户投诉类型",
		InputSchema:     map[string]any{"type": "object"},
		OutputSchema:    map[string]any{"type": "object"},
		CallingGuidance: "用户表达投诉时调用",
		Confirmed:       true,
	})
	req := httptest.NewRequest("PATCH", "/skills/skill-1/draft/contract", bytes.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if versionSvc.gotContract.ToolName != "classify_complaint" || !versionSvc.gotContract.Confirmed {
		t.Fatalf("expected contract input forwarded, got %#v", versionSvc.gotContract)
	}
	var resp dto.SkillVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ToolContract["toolName"] != "classify_complaint" {
		t.Fatalf("unexpected contract response: %#v", resp.ToolContract)
	}
}

func TestSkillHandlerUpdateDraftImplementation_ReturnsDraftVersion(t *testing.T) {
	versionSvc := &fakeVersionService{}
	h := NewSkillHandler(newTestSkillHandler().svc, zap.NewNop(), versionSvc)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.PATCH("/skills/:id/draft/implementation", h.UpdateDraftImplementation)

	body, _ := json.Marshal(dto.UpdateSkillImplementationRequest{
		Mode:    "prompt",
		Source:  map[string]any{"promptTemplate": "分类：{{.input}}"},
		Runtime: map[string]any{"model": "gpt-4.1-mini"},
	})
	req := httptest.NewRequest("PATCH", "/skills/skill-1/draft/implementation", bytes.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if versionSvc.gotImplementation.Mode != "prompt" {
		t.Fatalf("expected implementation input forwarded, got %#v", versionSvc.gotImplementation)
	}
	var resp dto.SkillVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Implementation["mode"] != "prompt" {
		t.Fatalf("unexpected implementation response: %#v", resp.Implementation)
	}
}

func (f *fakeVersionService) PublishDraft(_ context.Context, _ string) (skillapp.SkillVersion, error) {
	if f.publishErr != nil {
		return skillapp.SkillVersion{}, f.publishErr
	}
	return skillapp.SkillVersion{ID: "version-1", SkillID: "skill-1", VersionNo: 1, Status: domain.VersionStatusPublished}, nil
}

func TestSkillHandlerCreateSkill_MissingRequiredFields(t *testing.T) {
	h := newTestSkillHandler()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/skills", h.CreateSkill)

	req := httptest.NewRequest("POST", "/skills", bytes.NewReader([]byte("{}"))) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing required fields, got %d", w.Code)
	}
}

func TestSkillHandlerCreateSkill_InvalidType(t *testing.T) {
	h := newTestSkillHandler()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/skills", h.CreateSkill)

	body, _ := json.Marshal(dto.CreateSkillRequest{Name: "x", Type: "invalid"})
	req := httptest.NewRequest("POST", "/skills", bytes.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid type, got %d", w.Code)
	}
}

func TestSkillHandlerCreateSkill_CapabilityPayloadCreatesDraft(t *testing.T) {
	versionSvc := &fakeVersionService{}
	h := NewSkillHandler(newTestSkillHandler().svc, zap.NewNop(), versionSvc)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/skills", h.CreateSkill)

	body, _ := json.Marshal(dto.CreateSkillRequest{
		Name:           "投诉分类",
		Goal:           "判断客户投诉类型",
		WhenToUse:      "用户表达投诉时",
		SampleInput:    "快递没更新",
		ExpectedOutput: "物流问题",
	})
	req := httptest.NewRequest("POST", "/skills", bytes.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if versionSvc.gotCreate.Goal != "判断客户投诉类型" {
		t.Fatalf("expected capability create path, got %#v", versionSvc.gotCreate)
	}
	var resp dto.SkillWorkspaceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Skill.ID != "skill-1" || resp.Draft.ID != "draft-1" {
		t.Fatalf("unexpected workspace response: %#v", resp)
	}
}

func TestSkillHandlerPublishSkill_ReturnsPublishedVersion(t *testing.T) {
	h := NewSkillHandler(newTestSkillHandler().svc, zap.NewNop(), &fakeVersionService{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/skills/:id/publish", h.PublishSkill)

	req := httptest.NewRequest("POST", "/skills/skill-1/publish", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp dto.SkillVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ID != "version-1" || resp.Status != "published" {
		t.Fatalf("unexpected publish response: %#v", resp)
	}
}

func TestInputFromCreateReq_UnifiedRequestDefaultsToPromptSkill(t *testing.T) {
	in := inputFromCreateReq(dto.CreateSkillRequest{
		Name:           "投诉分类",
		Description:    "将客户投诉分成物流、质量、售后和价格四类，并说明理由。",
		ExpectedInput:  "客户投诉原文",
		ExpectedOutput: "分类、理由、建议动作",
		SampleCases:    "我的快递三天没更新 -> 物流",
	})

	if in.Type != "prompt" {
		t.Fatalf("expected unified request to default to prompt, got %q", in.Type)
	}
	if in.PromptTemplate == "" {
		t.Fatal("expected generated prompt template")
	}
	if !bytes.Contains([]byte(in.PromptTemplate), []byte("将客户投诉分成物流、质量、售后和价格四类")) {
		t.Fatalf("expected prompt template to include description, got %q", in.PromptTemplate)
	}
	if !bytes.Contains([]byte(in.PromptTemplate), []byte("{{.input}}")) {
		t.Fatalf("expected prompt template to include input placeholder, got %q", in.PromptTemplate)
	}
	if !bytes.Contains([]byte(in.PromptTemplate), []byte("客户投诉原文")) {
		t.Fatalf("expected prompt template to include expected input, got %q", in.PromptTemplate)
	}
	if !bytes.Contains([]byte(in.PromptTemplate), []byte("分类、理由、建议动作")) {
		t.Fatalf("expected prompt template to include expected output, got %q", in.PromptTemplate)
	}
	if !bytes.Contains([]byte(in.PromptTemplate), []byte("我的快递三天没更新")) {
		t.Fatalf("expected prompt template to include sample cases, got %q", in.PromptTemplate)
	}
}

func TestInputFromCreateReq_ExplicitLegacyTypeIsPreserved(t *testing.T) {
	in := inputFromCreateReq(dto.CreateSkillRequest{
		Name:     "脚本处理",
		Type:     "code",
		Code:     "def process(input_data): return input_data",
		Language: "python",
	})

	if in.Type != "code" {
		t.Fatalf("expected explicit type to be preserved, got %q", in.Type)
	}
	if in.Code == "" || in.Language != "python" {
		t.Fatalf("expected code config to be preserved, got code=%q language=%q", in.Code, in.Language)
	}
}

func TestSkillHandlerExecuteSkill_ReturnsTestResult(t *testing.T) {
	runner := &fakeSkillRunner{}
	svc := skillapp.NewSkillService(nil, nil, zap.NewNop(), runner)
	h := NewSkillHandler(svc, zap.NewNop())
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/skills/:id/test", h.ExecuteSkill)

	body, _ := json.Marshal(dto.ExecuteSkillRequest{Input: map[string]any{"text": "测试输入"}, TraceID: "trace-1"})
	req := httptest.NewRequest("POST", "/skills/skill-1/test", bytes.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if runner.gotID != "skill-1" {
		t.Fatalf("expected skill id skill-1, got %q", runner.gotID)
	}
	gotInput, ok := runner.gotInput.(map[string]any)
	if !ok || gotInput["text"] != "测试输入" {
		t.Fatalf("expected input forwarded to runner, got %#v", runner.gotInput)
	}
	var resp dto.ExecuteSkillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok || result["answer"] != "ok" {
		t.Fatalf("expected result answer ok, got %#v", resp.Result)
	}
	if resp.TraceID != "trace-1" {
		t.Fatalf("expected trace id trace-1, got %q", resp.TraceID)
	}
}

func TestSkillHandlerExecuteDraftSkill_ReturnsRenderedResult(t *testing.T) {
	svc := skillapp.NewSkillService(nil, &promptDraftFactory{}, zap.NewNop())
	h := NewSkillHandler(svc, zap.NewNop())
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/skills/test-draft", h.ExecuteDraftSkill)

	body, _ := json.Marshal(dto.ExecuteDraftSkillRequest{
		Skill: dto.CreateSkillRequest{
			Name:           "投诉分类",
			Description:    "将客户投诉分类",
			ExpectedOutput: "分类和理由",
		},
		Input:   "客户反馈快递三天没有更新",
		TraceID: "trace-draft",
	})
	req := httptest.NewRequest("POST", "/skills/test-draft", bytes.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp dto.ExecuteSkillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resp.Result)
	}
	content, _ := result["content"].(string)
	if !bytes.Contains([]byte(content), []byte("客户反馈快递三天没有更新")) {
		t.Fatalf("expected rendered content to include draft input, got %q", content)
	}
	if resp.TraceID != "trace-draft" {
		t.Fatalf("expected trace id trace-draft, got %q", resp.TraceID)
	}
}

func TestSkillHandlerGetSkill_NoTenantContext(t *testing.T) {
	t.Skip("integration: requires a real repo; covered by service-level tests")
}
