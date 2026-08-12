package application

import "testing"

func TestRedactSensitivePayload(t *testing.T) {
	got := RedactSensitivePayload(map[string]any{
		"apiKey": "sk-123", "token": "abc", "password": "p", "config": map[string]any{
			"url": "https://x", "secretKey": "s", "timeoutSec": 30,
		},
		"command": "npm start",
	})
	cfg := got["config"].(map[string]any)
	if got["apiKey"] != "***" || got["token"] != "***" || got["password"] != "***" {
		t.Fatalf("expected sensitive keys redacted, got %v", got)
	}
	if cfg["secretKey"] != "***" {
		t.Fatalf("expected nested secretKey redacted, got %v", cfg)
	}
	if cfg["url"] != "https://x" || cfg["timeoutSec"] != 30 || got["command"] != "npm start" {
		t.Fatalf("expected non-sensitive values preserved, got %v", got)
	}
}

func TestRedactSensitivePayloadRedactsInsideArrays(t *testing.T) {
	got := RedactSensitivePayload(map[string]any{
		"items": []any{
			map[string]any{"name": "a", "authorization": "Bearer x"},
			map[string]any{"name": "b", "value": 42},
			"plain",
		},
	})
	items := got["items"].([]any)
	first := items[0].(map[string]any)
	if first["authorization"] != "***" {
		t.Fatalf("expected array item authorization redacted, got %v", first)
	}
	if items[1].(map[string]any)["value"] != 42 || items[2] != "plain" {
		t.Fatalf("expected non-sensitive array items preserved, got %v", items)
	}
}

// 回归（review blocking）：敏感键的容器值必须整体掩蔽——{"token": {"value": "sk-1"}}
// 此前递归后完整泄露。
func TestRedactSensitivePayloadMasksSensitiveKeyContainers(t *testing.T) {
	got := RedactSensitivePayload(map[string]any{
		"token":      map[string]any{"value": "sk-1"},
		"config":     []any{map[string]any{"apiKey": "sk-2"}},
		"authHeader": []any{"Authorization: Bearer sk-3"},
		"tokenData":  map[string]any{"nested": map[string]any{"secretKey": "sk-4"}},
	})
	if got["token"] != "***" {
		t.Fatalf("expected token container masked entirely, got %v", got["token"])
	}
	if got["authHeader"] != "***" {
		t.Fatalf("expected authHeader array masked entirely, got %v", got["authHeader"])
	}
	if got["tokenData"] != "***" {
		t.Fatalf("expected tokenData container masked entirely, got %v", got["tokenData"])
	}
	cfg := got["config"].([]any)
	if cfg[0].(map[string]any)["apiKey"] != "***" {
		t.Fatalf("expected config array apiKey redacted, got %v", cfg)
	}
}

// 回归（review minor）：嵌套数组 [[{api_key}]] 必须递归脱敏。
func TestRedactSensitivePayloadHandlesNestedArrays(t *testing.T) {
	got := RedactSensitivePayload(map[string]any{
		"config": []any{
			[]any{map[string]any{"api_key": "sk-LEAKED"}},
			[]any{"plain", 42},
		},
	})
	outer := got["config"].([]any)
	inner := outer[0].([]any)
	if inner[0].(map[string]any)["api_key"] != "***" {
		t.Fatalf("expected nested array api_key redacted, got %v", inner)
	}
	if outer[1].([]any)[0] != "plain" {
		t.Fatalf("expected non-sensitive nested array preserved, got %v", outer)
	}
}

// 扩展 suffix：passwd/auth/session/bearer/cookie 命中。
func TestRedactSensitivePayloadExtendedSuffixes(t *testing.T) {
	got := RedactSensitivePayload(map[string]any{
		"passwd": "hunter2", "auth": "Basic dXNlcjpwYXNz",
		"sessionId": "s-1", "bearerToken": "b-1", "cookie": "c=1",
	})
	for k, v := range got {
		if v != "***" {
			t.Fatalf("expected %s masked, got %v", k, v)
		}
	}
}

// 回归（review minor）：review 轮补充的凭据键名——pwd/jwt/signature/otp/passphrase 命中；
// 非凭据键（design/museum 等含 "sig" 子串的词）不误伤。
func TestRedactSensitivePayloadReviewSupplementedSuffixes(t *testing.T) {
	got := RedactSensitivePayload(map[string]any{
		"pwd": "hunter2", "jwt": "eyJhbGciOi", "signature": "sha256:abc",
		"otp": "123456", "passphrase": "p", "design": "diagram", "museum": "hall",
	})
	for _, k := range []string{"pwd", "jwt", "signature", "otp", "passphrase"} {
		if got[k] != "***" {
			t.Fatalf("expected %s masked, got %v", k, got[k])
		}
	}
	if got["design"] != "diagram" || got["museum"] != "hall" {
		t.Fatalf("expected non-credential keys preserved, got %v", got)
	}
}
