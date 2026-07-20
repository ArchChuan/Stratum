package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestReadinessHandlerReflectsMandatoryComponentHealth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		result map[string]error
		want   int
	}{
		{name: "ready", result: map[string]error{"postgres": nil}, want: http.StatusOK},
		{name: "not ready", result: map[string]error{"postgres": errors.New("down")}, want: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/readyz", readinessHandler(func(context.Context) map[string]error { return tc.result }))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil)) //nolint:noctx
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
