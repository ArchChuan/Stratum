package safetext

import (
	"strings"
	"testing"
)

func TestRedactCredentialsCoversAuthorizationAndQuotedValues(t *testing.T) {
	input := "Authorization: Bearer raw-secret\nAuthorization: Basic dXNlcjpwYXNz\napi_key: \"secret value\"\n{\"token\":\"json-secret\",\"title\":\"keep me\"}\npassword=tail-secret"
	got := RedactCredentials(input)
	for _, secret := range []string{"raw-secret", "dXNlcjpwYXNz", "secret value", "json-secret", "tail-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("credential %q leaked in %q", secret, got)
		}
	}
	if !strings.Contains(got, `"title":"keep me"`) {
		t.Fatalf("unrelated field was swallowed: %q", got)
	}
	if strings.Count(got, "[REDACTED]") != 5 {
		t.Fatalf("redactions = %q", got)
	}
}

func TestRedactCredentialsCoversCompositeKeyNames(t *testing.T) {
	input := `{"api_key_value":"sk-123","oauth2_client_secret":"oauth-sec",` +
		`"ANTHROPIC_API_KEY":"sk-ant-1","X-Api-Key":"x-api-sec",` +
		`"client_secret":"cs-val","password_reset_token":"prt","title":"keep"}`
	got := RedactCredentials(input)
	for _, secret := range []string{"sk-123", "oauth-sec", "sk-ant-1", "x-api-sec", "cs-val", "prt"} {
		if strings.Contains(got, secret) {
			t.Fatalf("credential %q leaked in %q", secret, got)
		}
	}
	if !strings.Contains(got, `"title":"keep"`) {
		t.Fatalf("adjacent field was swallowed: %q", got)
	}
}

func TestRedactCredentialsCoversRawHeaderCompositeKeys(t *testing.T) {
	input := "X-Api-Key: sk-1\nclient_secret=abc\nANTHROPIC_API_KEY=sk-ant-2"
	got := RedactCredentials(input)
	for _, secret := range []string{"sk-1", "abc", "sk-ant-2"} {
		if strings.Contains(got, secret) {
			t.Fatalf("credential %q leaked in %q", secret, got)
		}
	}
}

func TestRedactCredentialsCoversDigitSuffixKeys(t *testing.T) {
	input := `{"secret1":"s-1","token2":"s-2","api_key3":"s-3","clientSecret2":"s-4","X-Api-Key3":"s-5","title":"keep"}`
	got := RedactCredentials(input)
	for _, secret := range []string{"s-1", "s-2", "s-3", "s-4", "s-5"} {
		if strings.Contains(got, secret) {
			t.Fatalf("credential %q leaked in %q", secret, got)
		}
	}
	if !strings.Contains(got, `"title":"keep"`) {
		t.Fatalf("adjacent field was swallowed: %q", got)
	}
}

func TestRedactCredentialsDoesNotMatchTokenWords(t *testing.T) {
	input := `{"tokens":5,"tokenizer":"x","prompt_tokens":100,"title":"keep"}`
	got := RedactCredentials(input)
	if got != input {
		t.Fatalf("token-like non-credential fields were redacted: %q", got)
	}
}

func TestRedactCredentialsIsIdempotent(t *testing.T) {
	input := `{"api_key_value":"sk-123","oauth2_client_secret":"sec","password":"p1"}`
	once := RedactCredentials(input)
	twice := RedactCredentials(once)
	if once != twice {
		t.Fatalf("not idempotent: once=%q twice=%q", once, twice)
	}
}

func TestRedactCredentialsCoversJSONAuthorizationKeys(t *testing.T) {
	input := `{"authorization":"Bearer json-secret","Authorization":"Basic dXNlcjpwYXNz","title":"keep"}`
	got := RedactCredentials(input)
	for _, secret := range []string{"json-secret", "dXNlcjpwYXNz"} {
		if strings.Contains(got, secret) {
			t.Fatalf("credential %q leaked in %q", secret, got)
		}
	}
	if !strings.Contains(got, `"title":"keep"`) {
		t.Fatalf("adjacent field was swallowed: %q", got)
	}
}
