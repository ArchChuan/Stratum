package observability

import (
	"testing"
)

func TestDefaultTraceConfig(t *testing.T) {
	cfg := DefaultTraceConfig()

	if cfg == nil {
		t.Error("expected config to be non-nil")
	}

	if cfg.ServiceName == "" {
		t.Error("expected non-empty ServiceName")
	}
}
