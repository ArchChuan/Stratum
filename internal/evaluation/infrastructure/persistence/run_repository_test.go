package persistence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeValueRedactsSensitiveEvaluationOutput(t *testing.T) {
	value := map[string]any{
		"result": "ok",
		"token":  "secret-token",
		"nested": map[string]any{"api_key": "secret-key", "count": 2},
		"text":   "authorization=Bearer secret-value",
	}

	encoded, err := json.Marshal(sanitizeValue(value))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"secret-token", "secret-key", "secret-value"} {
		if strings.Contains(text, secret) {
			t.Fatalf("sanitized output leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[REDACTED]") || !strings.Contains(text, `"result":"ok"`) {
		t.Fatalf("unexpected sanitized output: %s", text)
	}
}
