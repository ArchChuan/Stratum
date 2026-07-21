package observability

import (
	"strings"
	"testing"
)

func TestSafeTracePayloadRedactsAndBoundsUTF8(t *testing.T) {
	payload := SafeTracePayload(map[string]any{
		"query":   strings.Repeat("中文", 20),
		"token":   "secret-token-value",
		"api_key": "secret-api-key",
	}, 32)

	if strings.Contains(payload.Preview, "secret-token-value") || strings.Contains(payload.Preview, "secret-api-key") {
		t.Fatalf("sensitive value leaked in preview: %q", payload.Preview)
	}
	if !strings.Contains(payload.Preview, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %q", payload.Preview)
	}
	if len([]rune(payload.Preview)) > 32 {
		t.Fatalf("preview exceeds rune limit: %d", len([]rune(payload.Preview)))
	}
	if !payload.Truncated {
		t.Fatal("expected payload to be marked truncated")
	}
	if len(payload.SHA256) != 64 {
		t.Fatalf("unexpected sha256: %q", payload.SHA256)
	}
}

func TestSafeTracePayloadHashIsStable(t *testing.T) {
	value := map[string]any{"input": "same", "nested": map[string]any{"password": "hidden"}}
	first := SafeTracePayload(value, 100)
	second := SafeTracePayload(value, 100)

	if first.SHA256 != second.SHA256 {
		t.Fatalf("hash is not stable: %q != %q", first.SHA256, second.SHA256)
	}
	if strings.Contains(first.Preview, "hidden") {
		t.Fatalf("password leaked in preview: %q", first.Preview)
	}
}

func TestTraceContentCaptureRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("OTEL_CAPTURE_CONTENT", "")
	if TraceContentCaptureEnabled() {
		t.Fatal("content capture must be disabled by default")
	}
	t.Setenv("OTEL_CAPTURE_CONTENT", "true")
	if !TraceContentCaptureEnabled() {
		t.Fatal("content capture should be enabled by explicit opt-in")
	}
}
