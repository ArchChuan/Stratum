package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
)

func TestNewMCPServerConfigResponseOmitsCredentials(t *testing.T) {
	t.Parallel()

	cfg := &domain.ServerConfig{
		ID:      "server-1",
		Name:    "private server",
		Env:     map[string]string{"MODE": "prod", "OPENAI_API_KEY": "env-secret"},
		Headers: map[string]string{"X-Region": "cn", "Authorization": "Bearer header-secret"},
		Auth: &domain.AuthConfig{
			Type:               domain.AuthTypeOAuth2,
			Token:              "bearer-secret",
			APIKeyValue:        "api-secret",
			OAuth2ClientID:     "client-id",
			OAuth2ClientSecret: "oauth-secret",
		},
	}

	body, err := json.Marshal(NewMCPServerConfigResponse(cfg))
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	serialized := string(body)
	for _, secret := range []string{"env-secret", "header-secret", "bearer-secret", "api-secret", "oauth-secret"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("response exposed secret %q: %s", secret, serialized)
		}
	}
	for _, allowed := range []string{"MODE", "prod", "X-Region", "cn", "client-id", `"credential_configured":true`} {
		if !strings.Contains(serialized, allowed) {
			t.Errorf("response omitted safe value %q: %s", allowed, serialized)
		}
	}
}

func TestMCPServerConfigRequestTracksSystemKeyPresence(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "absent", body: `{}`},
		{name: "null", body: `{"system_key":null}`, wantErr: true},
		{name: "string", body: `{"system_key":"stratum.platform_mcp"}`, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var req MCPServerConfigRequest
			if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
				t.Fatal(err)
			}
			_, err := req.ServerConfig()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ServerConfig error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMCPServerConfigResponseExposesModeWithoutSystemKey(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(NewMCPServerConfigResponse(&domain.ServerConfig{
		ID: "managed", SystemKey: "secret-identity", ManagementMode: "platform_managed",
	}))
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(body)
	if !strings.Contains(serialized, `"management_mode":"platform_managed"`) {
		t.Fatalf("response omitted management mode: %s", serialized)
	}
	if strings.Contains(serialized, "secret-identity") || strings.Contains(serialized, "system_key") {
		t.Fatalf("response exposed system identity: %s", serialized)
	}
}

func TestIsSensitiveMCPConfigKey(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"Authorization":     true,
		"OPENAI_API_KEY":    true,
		"access-token":      true,
		"clientSecret":      true,
		"database_password": true,
		"credential-file":   true,
		"X-Region":          false,
		"MODEL":             false,
		"workflow_mode":     false,
	}
	for key, want := range tests {
		key, want := key, want
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			if got := IsSensitiveMCPConfigKey(key); got != want {
				t.Fatalf("IsSensitiveMCPConfigKey(%q)=%v, want %v", key, got, want)
			}
		})
	}
}
