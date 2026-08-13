package application

import (
	"context"
	"errors"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"go.uber.org/zap"
)

type lifecycleRegistryFake struct {
	unregistered string
}

func (f *lifecycleRegistryFake) RegisterServer(context.Context, string, string) error { return nil }
func (f *lifecycleRegistryFake) UnregisterServer(_ string, serverID string) error {
	f.unregistered = serverID
	return nil
}

type lifecycleManagerFake struct {
	deleted      string
	disconnected string
	updated      bool
	stored       *domain.ServerConfig
	updateErr    error
	audits       []*auditdomain.ResourceChangeAuditEvent
	editors      []string
	replaceActor string
}

func (f *lifecycleManagerFake) Connect(_ context.Context, _ *domain.ServerConfig, _ []string, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
	if audit != nil {
		f.audits = append(f.audits, audit)
	}
	return nil
}
func (f *lifecycleManagerFake) Disconnect(_ context.Context, serverID string) error {
	f.disconnected = serverID
	return nil
}
func (f *lifecycleManagerFake) Reconnect(context.Context, string) error { return nil }
func (f *lifecycleManagerFake) UpdateServer(_ context.Context, _ *domain.ServerConfig, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
	f.updated = true
	if audit != nil {
		f.audits = append(f.audits, audit)
	}
	return f.updateErr
}
func (f *lifecycleManagerFake) Delete(_ context.Context, serverID string, audit *auditdomain.ResourceChangeAuditEvent) error {
	f.deleted = serverID
	if audit != nil {
		f.audits = append(f.audits, audit)
	}
	return nil
}
func (f *lifecycleManagerFake) GetServerConfig(context.Context, string) (*domain.ServerConfig, error) {
	return f.stored, nil
}

func TestPlatformManagedServerMutationsAreRejectedBeforeLifecycleChange(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		act  func(*MCPService) error
	}{
		{name: "connect overwrite", act: func(s *MCPService) error {
			return s.ConnectServer(t.Context(), &domain.ServerConfig{ID: "stratum-platform-mcp"}, nil, "user-1")
		}},
		{name: "update", act: func(s *MCPService) error {
			return s.UpdateServer(t.Context(), &domain.ServerConfig{ID: "stratum-platform-mcp"}, "user-1")
		}},
		{name: "delete", act: func(s *MCPService) error {
			return s.DeleteServer(t.Context(), "stratum-platform-mcp", "user-1")
		}},
		{name: "disconnect", act: func(s *MCPService) error {
			return s.DisconnectServer(t.Context(), "stratum-platform-mcp", "user-1")
		}},
		// Reconnect is intentionally allowed for platform-managed servers;
		// it restores connectivity after idle eviction without modifying config.
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := &lifecycleManagerFake{stored: &domain.ServerConfig{
				ID: "stratum-platform-mcp", ManagementMode: "platform_managed",
			}}
			service := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
			service.SetTenantRoleResolver(stubTenantRole{role: "owner"})

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
		ID: "stratum-platform-mcp", SystemKey: "stratum.platform_mcp",
	}}
	service := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
	service.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	err := service.DeleteServer(t.Context(), "stratum-platform-mcp", "user-1")

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
func (f *lifecycleManagerFake) ListEditors(context.Context, string, string) ([]string, error) {
	return append([]string(nil), f.editors...), nil
}
func (f *lifecycleManagerFake) ReplaceEditors(_ context.Context, _, _ string, editorIDs []string, createdBy string, audit *auditdomain.ResourceChangeAuditEvent) error {
	f.editors = append([]string(nil), editorIDs...)
	f.replaceActor = createdBy
	if audit != nil {
		f.audits = append(f.audits, audit)
	}
	return nil
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
	manager := &lifecycleManagerFake{stored: &domain.ServerConfig{ID: "orders", Name: "orders"}}
	service := NewMCPService(registry, manager, zap.NewNop())
	service.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	if err := service.DeleteServer(context.Background(), "orders", "user-1"); err != nil {
		t.Fatal(err)
	}
	if manager.deleted != "orders" {
		t.Fatalf("manager deleted %q", manager.deleted)
	}
	if registry.unregistered != "orders" {
		t.Fatalf("registry unregistered %q", registry.unregistered)
	}
}
