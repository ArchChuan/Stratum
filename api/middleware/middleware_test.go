package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestErrorHandler(t *testing.T) {
	logger := zap.NewNop()
	handler := ErrorHandler(logger)

	if handler == nil {
		t.Error("expected ErrorHandler to be non-nil")
	}
}

func TestMapErrorToStatusMapsTraceEvidenceFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: agentdomain.ErrEvidenceNotFound, want: http.StatusNotFound},
		{name: "invalid", err: fmt.Errorf("decode: %w", agentdomain.ErrEvidenceInvalid), want: http.StatusBadGateway},
		{name: "unavailable", err: fmt.Errorf("opik: %w", agentdomain.ErrEvidenceUnavailable), want: http.StatusServiceUnavailable},
		{name: "assistant model unavailable", err: agentdomain.ErrAssistantModelUnavailable, want: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapErrorToStatus(tt.err); got != tt.want {
				t.Fatalf("MapErrorToStatus() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestErrorHandlerDoesNotTurnCanceledClientRequestIntoServerError(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	router := gin.New()
	router.Use(ErrorHandler(zap.New(core)))
	router.GET("/canceled", func(c *gin.Context) {
		ctx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		c.Request = c.Request.WithContext(ctx)
		_ = c.Error(fmt.Errorf("repository: %w", context.Canceled))
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/canceled", nil)
	router.ServeHTTP(response, request)

	if response.Code >= http.StatusInternalServerError {
		t.Fatalf("canceled request returned server error: %d", response.Code)
	}
	if logs.Len() != 0 {
		t.Fatalf("canceled request emitted error log: %s", logs.All()[0].Message)
	}
}
