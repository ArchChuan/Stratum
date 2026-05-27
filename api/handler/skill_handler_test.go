package handler

import (
	"bytes"
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
	registry := orchestrator.NewRegistry()
	gateway := llmgateway.NewGateway()
	handler := NewSkillHandler(registry, logger, gateway)

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
	httpReq := httptest.NewRequest("POST", "/skills", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
	}

	var resp model.SkillResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got '%s'", resp.Name)
	}
}

func TestSkillHandlerGetSkill(t *testing.T) {
	logger := zap.NewNop()
	registry := orchestrator.NewRegistry()
	gateway := llmgateway.NewGateway()
	handler := NewSkillHandler(registry, logger, gateway)

	s := skill.NewCodeSkill("test-id", "test", "desc", "code", "python")
	registry.Register("test-id", s)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/skills/:id", handler.GetSkill)

	httpReq := httptest.NewRequest("GET", "/skills/test-id", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestSkillHandlerGetAllSkills(t *testing.T) {
	logger := zap.NewNop()
	registry := orchestrator.NewRegistry()
	gateway := llmgateway.NewGateway()
	handler := NewSkillHandler(registry, logger, gateway)

	s1 := skill.NewCodeSkill("id1", "skill1", "desc1", "code", "python")
	s2 := skill.NewCodeSkill("id2", "skill2", "desc2", "code", "go")
	registry.Register("id1", s1)
	registry.Register("id2", s2)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/skills", handler.GetAllSkills)

	httpReq := httptest.NewRequest("GET", "/skills", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestSkillHandlerDeleteSkill(t *testing.T) {
	logger := zap.NewNop()
	registry := orchestrator.NewRegistry()
	gateway := llmgateway.NewGateway()
	handler := NewSkillHandler(registry, logger, gateway)

	s := skill.NewCodeSkill("test-id", "test", "desc", "code", "python")
	registry.Register("test-id", s)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/skills/:id", handler.DeleteSkill)

	httpReq := httptest.NewRequest("DELETE", "/skills/test-id", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}
