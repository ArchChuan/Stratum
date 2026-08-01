package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"go.uber.org/zap"
)

type lifecycleRegistryFake struct {
	unregistered string
}

func (f *lifecycleRegistryFake) RegisterServer(context.Context, string) error { return nil }
func (f *lifecycleRegistryFake) UnregisterServer(serverID string) error {
	f.unregistered = serverID
	return nil
}

type lifecycleManagerFake struct {
	deleted      string
	disconnected string
	updated      bool
	stored       *domain.ServerConfig
}

func (f *lifecycleManagerFake) Connect(context.Context, *domain.ServerConfig) error { return nil }
func (f *lifecycleManagerFake) Disconnect(_ context.Context, serverID string) error {
	f.disconnected = serverID
	return nil
}
func (f *lifecycleManagerFake) Reconnect(context.Context, string) error { return nil }
func (f *lifecycleManagerFake) UpdateServer(context.Context, *domain.ServerConfig) error {
	f.updated = true
	return nil
}
func (f *lifecycleManagerFake) Delete(_ context.Context, serverID string) error {
	f.deleted = serverID
	return nil
}
func (f *lifecycleManagerFake) GetServerConfig(context.Context, string) (*domain.ServerConfig, error) {
	return f.stored, nil
}
func (f *lifecycleManagerFake) HandleForwardedToolCall(
	context.Context, string, string, string, map[string]any,
) (domain.ForwardedCallResult, error) {
	return domain.ForwardedCallResult{}, nil
}

func TestPlatformManagedServerMutationsAreRejectedBeforeLifecycleChange(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		act  func(*MCPService) error
	}{
		{name: "connect overwrite", act: func(s *MCPService) error {
			return s.ConnectServer(t.Context(), &domain.ServerConfig{ID: platformmcp.SystemServerID})
		}},
		{name: "update", act: func(s *MCPService) error {
			return s.UpdateServer(t.Context(), &domain.ServerConfig{ID: platformmcp.SystemServerID})
		}},
		{name: "delete", act: func(s *MCPService) error {
			return s.DeleteServer(t.Context(), platformmcp.SystemServerID)
		}},
		{name: "disconnect", act: func(s *MCPService) error {
			return s.DisconnectServer(t.Context(), platformmcp.SystemServerID)
		}},
		// Reconnect is intentionally allowed for platform-managed servers;
		// it restores connectivity after idle eviction without modifying config.
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := &lifecycleManagerFake{stored: &domain.ServerConfig{
				ID: platformmcp.SystemServerID, ManagementMode: platformmcp.ManagementPlatform,
			}}
			service := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())

			err := tt.act(service)

			if !errors.Is(err, domain.ErrPlatformManagedServer) {
				t.Fatalf("error = %v, want ErrPlatformManagedServer", err)
			}
			if manager.updated || manager.deleted != "" || manager.disconnected != "" {
				t.Fatalf("managed lifecycle mutated: %+v", manager)
			}
		})
	}
}

func TestPlatformManagedServerSystemKeyFailsClosedWithoutManagementMode(t *testing.T) {
	manager := &lifecycleManagerFake{stored: &domain.ServerConfig{
		ID: platformmcp.SystemServerID, SystemKey: platformmcp.SystemServerKey,
	}}
	service := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())

	err := service.DeleteServer(t.Context(), platformmcp.SystemServerID)

	if !errors.Is(err, domain.ErrPlatformManagedServer) {
		t.Fatalf("error = %v, want ErrPlatformManagedServer", err)
	}
	if manager.deleted != "" {
		t.Fatalf("managed server deleted: %q", manager.deleted)
	}
}
func (f *lifecycleManagerFake) ListTools(context.Context, string) ([]*domain.Tool, error) {
	return nil, nil
}
func (f *lifecycleManagerFake) ListResources(context.Context, string) ([]*domain.Resource, error) {
	return nil, nil
}
func (f *lifecycleManagerFake) GetServerInfo(context.Context, string) *domain.ServerInfo { return nil }
func (f *lifecycleManagerFake) GetAllServerInfo(context.Context) []*domain.ServerInfo    { return nil }
func (f *lifecycleManagerFake) RemoveTenant(context.Context, string) error               { return nil }
func (f *lifecycleManagerFake) Quota(context.Context) domain.Quota                       { return domain.Quota{} }

func TestDeleteServerUnregistersDiscoveredTools(t *testing.T) {
	registry := &lifecycleRegistryFake{}
	manager := &lifecycleManagerFake{}
	service := NewMCPService(registry, manager, zap.NewNop())

	if err := service.DeleteServer(context.Background(), "orders"); err != nil {
		t.Fatal(err)
	}
	if manager.deleted != "orders" {
		t.Fatalf("manager deleted %q", manager.deleted)
	}
	if registry.unregistered != "orders" {
		t.Fatalf("registry unregistered %q", registry.unregistered)
	}
}
