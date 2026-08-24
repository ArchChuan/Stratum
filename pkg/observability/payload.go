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
			if isTraceSensitiveKey(key) && !isExemptUsageValue(key, item) {
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
	normalized := normalizedTraceKey(key)
	for _, fragment := range []string{"password", "token", "api_key", "apikey", "authorization", "secret"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

// isExemptUsageValue 只豁免 LLM 用量计数：命中白名单 key 且值为数值标量时才放行
// （记录 token 消耗而非认证凭据）。字符串塞入白名单 key 仍按敏感值打码，堵住
// 经 usage 字段夹带凭据绕过三层脱敏的通道（security 中-1）。
func isExemptUsageValue(key string, value any) bool {
	switch normalizedTraceKey(key) {
	case "tokens_used", "total_tokens", "prompt_tokens", "completion_tokens":
		return isNumericScalar(value)
	default:
		return false
	}
}

func isNumericScalar(value any) bool {
	switch value.(type) {
	case float64, float32, int, int64, int32, uint, uint64, json.Number:
		return true
	default:
		return false
	}
}

func normalizedTraceKey(key string) string {
	return strings.ToLower(strings.ReplaceAll(key, "-", "_"))
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
