package harness

import (
	"testing"

	"go.uber.org/zap"
)

func TestNew(t *testing.T) {
	logger := zap.NewNop()
	harness := New(logger)

	if harness == nil {
		t.Error("expected Harness to be non-nil")
	}
}
