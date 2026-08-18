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
		{name: "sampling out of range", err: llmgatewaydomain.ErrSamplingOutOfRange, status: http.StatusBadRequest},
		{name: "capability unsupported", err: llmgatewaydomain.ErrCapabilityUnsupported, status: http.StatusBadRequest},
		{name: "context length exceeded (duck-type)", err: contextLengthExceededStub{}, status: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapErrorToStatus(fmt.Errorf("set default embedding: %w", tt.err)); got != tt.status {
				t.Fatalf("MapErrorToStatus() = %d, want %d", got, tt.status)
			}
		})
	}
}

// contextLengthExceededStub 模拟 infrastructure 的 contextLengthExceededError：
// middleware 不依赖 infrastructure 包，经 duck-type 探测映射（error_mapping.go）。
type contextLengthExceededStub struct{}

func (contextLengthExceededStub) Error() string                { return "context length exceeded" }
func (contextLengthExceededStub) ContextLengthExceeded() bool  { return true }

