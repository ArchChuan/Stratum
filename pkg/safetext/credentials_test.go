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
