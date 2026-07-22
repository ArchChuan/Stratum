package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/domain/port"
	"go.uber.org/zap"
)

type secretTestManager struct {
	stored  *domain.ServerConfig
	updated *domain.ServerConfig
}

func (m *secretTestManager) Connect(context.Context, *domain.ServerConfig) error { return nil }
func (m *secretTestManager) Disconnect(context.Context, string) error            { return nil }
func (m *secretTestManager) Reconnect(context.Context, string) error             { return nil }
func (m *secretTestManager) Delete(context.Context, string) error                { return nil }
func (m *secretTestManager) ListTools(context.Context, string) ([]*domain.Tool, error) {
	return nil, nil
}
func (m *secretTestManager) ListResources(context.Context, string) ([]*domain.Resource, error) {
	return nil, nil
}
func (m *secretTestManager) GetServerInfo(context.Context, string) *domain.ServerInfo { return nil }
func (m *secretTestManager) GetAllServerInfo(context.Context) []*domain.ServerInfo    { return nil }
func (m *secretTestManager) GetServerConfig(context.Context, string) (*domain.ServerConfig, error) {
	return m.stored, nil
}
func (m *secretTestManager) UpdateServer(_ context.Context, cfg *domain.ServerConfig) error {
	m.updated = cfg
	return nil
}

type secretTestRegistry struct{}

func (secretTestRegistry) RegisterServer(context.Context, string) error { return nil }
func (secretTestRegistry) UnregisterServer(string) error                { return nil }

var _ port.ServerManager = (*secretTestManager)(nil)
var _ port.ToolRegistry = secretTestRegistry{}

func TestUpdateServerPreservesOmittedSecrets(t *testing.T) {
	t.Parallel()

	manager := &secretTestManager{stored: &domain.ServerConfig{
		ID:        "server-1",
		Transport: "http",
		Headers:   map[string]string{"X-Region": "old", "Authorization": "header-secret"},
		Auth:      &domain.AuthConfig{Type: domain.AuthTypeAPIKey, APIKeyHeader: "X-API-Key", APIKeyValue: "auth-secret"},
	}}
	svc := NewMCPService(secretTestRegistry{}, manager, zap.NewNop())
	incoming := &domain.ServerConfig{
		ID:        "server-1",
		Transport: "http",
		Headers:   map[string]string{"X-Region": "new"},
		Auth:      &domain.AuthConfig{Type: domain.AuthTypeAPIKey, APIKeyHeader: "X-API-Key"},
	}

	if err := svc.UpdateServer(context.Background(), incoming); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}
	if got := manager.updated.Auth.APIKeyValue; got != "auth-secret" {
		t.Errorf("API key=%q, want preserved value", got)
	}
	if got := manager.updated.Headers["Authorization"]; got != "header-secret" {
		t.Errorf("authorization header=%q, want preserved value", got)
	}
	if manager.stored.Headers["X-Region"] != "old" {
		t.Fatal("stored config was mutated during merge")
	}
}

func TestUpdateServerPreservesOmittedEnvironmentSecretForStdio(t *testing.T) {
	t.Parallel()

	manager := &secretTestManager{stored: &domain.ServerConfig{
		ID: "server-1", Transport: "stdio", Env: map[string]string{"MODE": "old", "OPENAI_API_KEY": "env-secret"},
	}}
	svc := NewMCPService(secretTestRegistry{}, manager, zap.NewNop())
	incoming := &domain.ServerConfig{
		ID: "server-1", Transport: "stdio", Env: map[string]string{"MODE": "new"},
	}

	if err := svc.UpdateServer(context.Background(), incoming); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}
	if got := manager.updated.Env["OPENAI_API_KEY"]; got != "env-secret" {
		t.Errorf("env secret=%q, want preserved value", got)
	}
	if manager.stored.Env["MODE"] != "old" {
		t.Fatal("stored config was mutated during merge")
	}
}

func TestUpdateServerReplacesOrClearsAuthSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		auth *domain.AuthConfig
		want *domain.AuthConfig
	}{
		{
			name: "replacement",
			auth: &domain.AuthConfig{Type: domain.AuthTypeAPIKey, APIKeyValue: "new-secret"},
			want: &domain.AuthConfig{Type: domain.AuthTypeAPIKey, APIKeyValue: "new-secret"},
		},
		{
			name: "authentication type changed",
			auth: &domain.AuthConfig{Type: domain.AuthTypeBearer},
			want: &domain.AuthConfig{Type: domain.AuthTypeBearer},
		},
		{name: "authentication removed", auth: nil, want: nil},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			manager := &secretTestManager{stored: &domain.ServerConfig{
				ID: "server-1", Auth: &domain.AuthConfig{Type: domain.AuthTypeAPIKey, APIKeyValue: "old-secret"},
			}}
			svc := NewMCPService(secretTestRegistry{}, manager, zap.NewNop())
			if err := svc.UpdateServer(context.Background(), &domain.ServerConfig{ID: "server-1", Auth: tt.auth}); err != nil {
				t.Fatalf("UpdateServer: %v", err)
			}
			if tt.want == nil {
				if manager.updated.Auth != nil {
					t.Fatalf("auth=%+v, want nil", manager.updated.Auth)
				}
				return
			}
			if manager.updated.Auth.Type != tt.want.Type || manager.updated.Auth.APIKeyValue != tt.want.APIKeyValue {
				t.Fatalf("auth=%+v, want %+v", manager.updated.Auth, tt.want)
			}
		})
	}
}
