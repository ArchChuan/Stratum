package application

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
)

func TestMCPSafeProjectionNeverExposesCredentials(t *testing.T) {
	cfg := &domain.ServerConfig{
		ID: "orders", Name: "orders", Version: "1.0.0", Transport: "streamable-http",
		URL:          "https://user:pass@mcp.example.com/sse?token=abc&x=1",
		Env:          map[string]string{"API_KEY": "sk-secret", "PATH": "/usr/bin"},
		Headers:      map[string]string{"Authorization": "Bearer xxx"},
		Auth:         &domain.AuthConfig{Type: "bearer", Token: "tok-secret"},
		Capabilities: []string{"tools"},
		Timeout:      5e9,
	}
	proj := MCPSafeProjection(cfg)

	for _, forbidden := range []string{"env", "headers", "auth", "token", "secret", "pass"} {
		if _, ok := proj[forbidden]; ok {
			t.Fatalf("projection exposes %q key", forbidden)
		}
	}
	if got := proj["url"]; got == "https://user:pass@mcp.example.com/sse?token=abc&x=1" {
		t.Fatalf("projection leaked credential-bearing URL: %v", got)
	}
	if got := proj["url"]; got != "https://mcp.example.com/sse?x=1" {
		t.Fatalf("url not scrubbed of userinfo and credential query: %v", got)
	}
	if proj["name"] != "orders" || proj["id"] != "orders" {
		t.Fatalf("projection lost identity fields: %v", proj)
	}
}

func TestMCPSafeProjectionNilIsEmpty(t *testing.T) {
	if got := MCPSafeProjection(nil); len(got) != 0 {
		t.Fatalf("nil projection = %v, want empty", got)
	}
}

func TestMCPSafeURLCredentialScrubbing(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "userinfo", in: "http://admin:hunter2@host:8080/path", want: "http://host:8080/path"},
		{name: "token query", in: "https://host/sse?api_key=abc&refresh_token=xyz&ok=1", want: "https://host/sse?ok=1"},
		{name: "no credentials untouched", in: "https://host/sse?workspace=ws", want: "https://host/sse?workspace=ws"},
		{name: "relative url passes through", in: "/sse?token=abc&ok=1", want: "/sse?ok=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MCPSafeURL(tc.in); got != tc.want {
				t.Fatalf("MCPSafeURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
