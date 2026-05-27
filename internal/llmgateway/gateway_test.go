package llmgateway

import (
	"testing"
)

func TestNewGateway(t *testing.T) {
	gateway := NewGateway()

	if gateway == nil {
		t.Error("expected Gateway to be non-nil")
	}
}
