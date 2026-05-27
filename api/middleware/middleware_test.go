package middleware

import (
	"testing"

	"go.uber.org/zap"
)

func TestErrorHandler(t *testing.T) {
	logger := zap.NewNop()
	handler := ErrorHandler(logger)

	if handler == nil {
		t.Error("expected ErrorHandler to be non-nil")
	}
}
