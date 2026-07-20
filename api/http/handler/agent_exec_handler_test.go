package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestAgentExecutionErrorUsesHTTPErrorPipeline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/execute", func(c *gin.Context) {
		respondAgentExecutionError(c, errors.New("provider unavailable"))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/execute", nil)) //nolint:noctx
	if w.Code == http.StatusOK {
		t.Fatalf("agent failure returned HTTP 200: %s", w.Body.String())
	}
}
