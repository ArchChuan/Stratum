package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
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
	deleted string
}

func (f *lifecycleManagerFake) Connect(context.Context, *domain.ServerConfig) error { return nil }
func (f *lifecycleManagerFake) Disconnect(context.Context, string) error            { return nil }
func (f *lifecycleManagerFake) Reconnect(context.Context, string) error             { return nil }
func (f *lifecycleManagerFake) UpdateServer(context.Context, *domain.ServerConfig) error {
	return nil
}
func (f *lifecycleManagerFake) Delete(_ context.Context, serverID string) error {
	f.deleted = serverID
	return nil
}
func (f *lifecycleManagerFake) GetServerConfig(context.Context, string) (*domain.ServerConfig, error) {
	return nil, nil
}
func (f *lifecycleManagerFake) ListTools(context.Context, string) ([]*domain.Tool, error) {
	return nil, nil
}
func (f *lifecycleManagerFake) ListResources(context.Context, string) ([]*domain.Resource, error) {
	return nil, nil
}
func (f *lifecycleManagerFake) GetServerInfo(context.Context, string) *domain.ServerInfo { return nil }
func (f *lifecycleManagerFake) GetAllServerInfo(context.Context) []*domain.ServerInfo    { return nil }

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
