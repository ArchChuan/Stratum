package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

func newModelHandler(gw *llmgateway.Gateway) *ModelHandler {
	return NewModelHandler(llmapp.NewModelService(gw))
}

func testModelRegistry() *llmgateway.ModelRegistry {
	readSettings := func(_ context.Context, _ string) ([]byte, error) {
		return json.Marshal(map[string]any{"llm_api_keys": map[string]string{}})
	}
	return llmgateway.NewModelRegistry(readSettings, [32]byte{}, nil)
}

func TestListModels_emptyGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/models", nil) //nolint:noctx

	h := newModelHandler(llmgateway.NewGateway(testModelRegistry()))
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
	// Static catalogue is non-empty now.
	if len(models) == 0 {
		t.Error("expected non-empty static model list")
	}
}

func TestListModels_withProviders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/models", nil) //nolint:noctx

	gw := llmgateway.NewGateway(testModelRegistry())

	h := newModelHandler(gw)
	h.ListModels(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	models, ok := resp["models"].([]any)
	if !ok {
		t.Fatalf("'models' is not an array, got %T", resp["models"])
	}
	if len(models) == 0 {
		t.Fatal("expected non-empty model list")
	}
	// verify sorted
	for i := 1; i < len(models); i++ {
		a, b := models[i-1].(string), models[i].(string)
		if a > b {
			t.Errorf("not sorted: %q > %q", a, b)
		}
	}
}
