package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

func newModelHandler(registry *llmgateway.ModelRegistry) *ModelHandler {
	return NewModelHandler(llmapp.NewModelService(registry))
}

func TestListModels_emptyRegistry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/models", nil) //nolint:noctx

	reg := llmgateway.NewModelRegistry(
		llmgateway.NewPgModelRepo(nil),
		llmgateway.NewPgProviderRepo(nil),
		nil, nil, 5*time.Minute,
	)
	h := newModelHandler(reg)
	h.ListModels(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	raw, ok := resp["models"]
	if !ok {
		t.Fatal("response missing 'models' key")
	}
	models, ok := raw.([]any)
	if !ok {
		t.Fatalf("'models' is not an array, got %T", raw)
	}
	if len(models) != 0 {
		t.Errorf("expected empty array, got %v", models)
	}
}
