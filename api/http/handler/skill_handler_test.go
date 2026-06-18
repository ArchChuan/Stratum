package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SkillHandler unit tests cover input validation only.
// DB operations require integration tests with a real pgxpool.Pool.

func newTestSkillHandler() *SkillHandler {
	svc := skillapp.NewSkillService(nil, nil, nil, zap.NewNop())
	return NewSkillHandler(svc, zap.NewNop())
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

func TestSkillHandlerGetSkill_NoTenantContext(t *testing.T) {
	t.Skip("integration: requires a real repo; covered by service-level tests")
}
