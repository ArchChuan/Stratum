package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/model"
	"github.com/byteBuilderX/stratum/internal/llmgateway"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SkillHandler unit tests cover input validation only.
// DB operations require integration tests with a real pgxpool.Pool.

func TestSkillHandlerCreateSkill_MissingRequiredFields(t *testing.T) {
	h := NewSkillHandler(nil, zap.NewNop(), llmgateway.NewGateway(), nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
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
	h := NewSkillHandler(nil, zap.NewNop(), llmgateway.NewGateway(), nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/skills", h.CreateSkill)

	body, _ := json.Marshal(model.CreateSkillRequest{Name: "x", Type: "invalid"})
	req := httptest.NewRequest("POST", "/skills", bytes.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid type, got %d", w.Code)
	}
}

func TestSkillHandlerGetSkill_NoTenantContext(t *testing.T) {
	h := NewSkillHandler(nil, zap.NewNop(), llmgateway.NewGateway(), nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/skills/:id", h.GetSkill)

	req := httptest.NewRequest("GET", "/skills/some-id", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 without tenant context, got %d", w.Code)
	}
}
