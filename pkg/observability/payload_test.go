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

func TestSafeTracePayloadExemptsLLMUsageCounters(t *testing.T) {
	// LLM 用量计数（tokens_used 等）是 token 消耗而非认证凭据，不得被打码——
	// stratum_delegate 结构化外壳即依赖这些字段展示子 agent 的 token 用量。
	payload := SafeTracePayload(map[string]any{
		"tokens_used":       42,
		"prompt_tokens":     10,
		"completion_tokens": 32,
		"total_tokens":      42,
	}, 512)
	if strings.Contains(payload.Preview, "[REDACTED]") {
		t.Fatalf("LLM usage counter must not be redacted: %q", payload.Preview)
	}
	if !strings.Contains(payload.Preview, `"tokens_used":42`) {
		t.Fatalf("tokens_used value lost: %q", payload.Preview)
	}

	// 真实凭据仍被打码——豁免只影响 usage 白名单，不削弱保护。
	payload = SafeTracePayload(map[string]any{
		"tokens_used": 42,
		"auth_token":  "secret-bearer",
	}, 512)
	if strings.Contains(payload.Preview, "secret-bearer") {
		t.Fatalf("real credential leaked after usage exemption: %q", payload.Preview)
	}
	if !strings.Contains(payload.Preview, "[REDACTED]") {
		t.Fatalf("real credential redaction marker missing: %q", payload.Preview)
	}
}

func TestSafeTracePayloadRedactsStringInsideUsageKey(t *testing.T) {
	// 豁免只对「数值标量」生效：把凭据字符串塞进 total_tokens 等白名单 key
	// 仍必须打码，堵住经 usage 字段夹带绕过脱敏的通道（security 中-1）。
	payload := SafeTracePayload(map[string]any{
		"total_tokens": "secret-bearer-token",
	}, 512)
	if strings.Contains(payload.Preview, "secret-bearer-token") {
		t.Fatalf("credential smuggled via usage key leaked: %q", payload.Preview)
	}
	if !strings.Contains(payload.Preview, "[REDACTED]") {
		t.Fatalf("smuggled credential redaction marker missing: %q", payload.Preview)
	}

	// 数值型 usage 计数仍豁免（正常路径不受影响）。
	payload = SafeTracePayload(map[string]any{"total_tokens": 42}, 512)
	if strings.Contains(payload.Preview, "[REDACTED]") {
		t.Fatalf("numeric usage counter must stay visible: %q", payload.Preview)
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
