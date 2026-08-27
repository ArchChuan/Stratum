package gen

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

func TestMCPServerConfigRequestIgnoresSystemKey(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`{}`, `{"system_key":null}`, `{"system_key":"stratum.platform_mcp"}`} {
		var req MCPServerConfigRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		if _, err := req.ServerConfig(); err != nil {
			t.Fatalf("ServerConfig err for %s = %v, want nil: system_key 已随契约删除,外部提交由 json.Unmarshal 静默忽略", body, err)
		}
	}
}

func TestMCPServerConfigResponseOmitsDeadFields(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(NewMCPServerConfigResponse(&domain.ServerConfig{ID: "managed"}))
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(body)
	for _, forbidden := range []string{"system_key", "management_mode", "platform_managed", "secret-identity"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("response exposed dead field %q: %s", forbidden, serialized)
		}
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
