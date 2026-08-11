package middleware

import (
	"fmt"
	"net/http"
	"testing"

	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

func TestMapErrorToStatus_LLMGatewaySentinels(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "model not embedding enabled", err: llmgatewaydomain.ErrModelNotEmbeddingEnabled, status: http.StatusBadRequest},
		{name: "model not found", err: llmgatewaydomain.ErrModelNotFound, status: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapErrorToStatus(fmt.Errorf("set default embedding: %w", tt.err)); got != tt.status {
				t.Fatalf("MapErrorToStatus() = %d, want %d", got, tt.status)
			}
		})
	}
}
