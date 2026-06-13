package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestSkillHandlerCreateSkill(t *testing.T) {
	logger := zap.NewNop()
	registry := orchestrator.NewRegistry(nil)
	gateway := llmgateway.NewGateway()
	handler := NewSkillHandler(registry, logger, gateway, nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/skills", handler.CreateSkill)

	req := model.CreateSkillRequest{
		Name:        "test-skill",
		Description: "test description",
		Type:        "code",
		Code:        "print('hello')",
		Language:    "python",
	}

	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/skills", bytes.NewReader(body)) //nolint:noctx
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
	}

	var resp model.SkillResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got '%s'", resp.Name)
	}
}

func TestSkillHandlerGetSkill(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()
	registry := orchestrator.NewRegistry(nil)
	gateway := llmgateway.NewGateway()
	handler := NewSkillHandler(registry, logger, gateway, nil)

	s := skill.NewCodeSkill("test-id", "test", "desc", "code", "python")
	if err := registry.Register(ctx, "test-id", s); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/skills/:id", handler.GetSkill)

	httpReq := httptest.NewRequest("GET", "/skills/test-id", nil) //nolint:noctx
	w := httptest.NewRecorder()

	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestSkillHandlerGetAllSkills(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()
	registry := orchestrator.NewRegistry(nil)
	gateway := llmgateway.NewGateway()
	handler := NewSkillHandler(registry, logger, gateway, nil)

	s1 := skill.NewCodeSkill("id1", "skill1", "desc1", "code", "python")
	s2 := skill.NewCodeSkill("id2", "skill2", "desc2", "code", "go")
	if err := registry.Register(ctx, "id1", s1); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}
	if err := registry.Register(ctx, "id2", s2); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/skills", handler.GetAllSkills)

	httpReq := httptest.NewRequest("GET", "/skills", nil) //nolint:noctx
	w := httptest.NewRecorder()

	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestSkillHandlerDeleteSkill(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()
	registry := orchestrator.NewRegistry(nil)
	gateway := llmgateway.NewGateway()
	handler := NewSkillHandler(registry, logger, gateway, nil)

	s := skill.NewCodeSkill("test-id", "test", "desc", "code", "python")
	if err := registry.Register(ctx, "test-id", s); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/skills/:id", handler.DeleteSkill)

	httpReq := httptest.NewRequest("DELETE", "/skills/test-id", nil) //nolint:noctx
	w := httptest.NewRecorder()

	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}
