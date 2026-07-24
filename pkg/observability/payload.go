package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"

	"github.com/byteBuilderX/stratum/pkg/safetext"
)

const traceRedactedValue = "[REDACTED]"

// TracePayload is a bounded, redacted representation safe for telemetry attributes.
type TracePayload struct {
	Preview   string
	SHA256    string
	Truncated bool
}

// TraceContentCaptureEnabled reports whether raw content previews may be sent
// to the configured telemetry backend. It is disabled unless explicitly opted in.
func TraceContentCaptureEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_CAPTURE_CONTENT")), "true")
}

// SafeTracePayload serializes value after redacting sensitive keys, then returns
// a stable hash and a UTF-8-safe preview bounded by maxRunes.
func SafeTracePayload(value any, maxRunes int) TracePayload {
	raw, hash := SanitizedTracePayload(value)
	preview, truncated := truncateTraceRunes(string(raw), maxRunes)
	return TracePayload{Preview: preview, SHA256: hash, Truncated: truncated}
}

// SanitizedTracePayload returns the complete redacted serialization and its stable hash.
func SanitizedTracePayload(value any) ([]byte, string) {
	sanitized := sanitizeTraceValue(value)
	raw, err := json.Marshal(sanitized)
	if err != nil {
		raw = []byte(`"[UNSERIALIZABLE]"`)
	}
	text := safetext.RedactCredentials(string(raw))
	sum := sha256.Sum256([]byte(text))
	return []byte(text), hex.EncodeToString(sum[:])
}

func sanitizeTraceValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isTraceSensitiveKey(key) {
				out[key] = traceRedactedValue
				continue
			}
			out[key] = sanitizeTraceValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeTraceValue(item)
		}
		return out
	default:
		return typed
	}
}

func isTraceSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, fragment := range []string{"password", "token", "api_key", "apikey", "authorization", "secret"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func truncateTraceRunes(value string, maxRunes int) (string, bool) {
	if maxRunes <= 0 {
		return "", value != ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value, false
	}
	return string(runes[:maxRunes]), true
}
