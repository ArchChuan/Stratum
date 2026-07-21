package middleware

import (
	"fmt"
	"net/http"
	"testing"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	"go.uber.org/zap"
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapErrorToStatus(tt.err); got != tt.want {
				t.Fatalf("MapErrorToStatus() = %d, want %d", got, tt.want)
			}
		})
	}
}
