package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// failRoleResolver 使租户角色解析失败，验证授权 fail-closed。
type failRoleResolver struct{ err error }

func (f failRoleResolver) ResolveTenantRole(context.Context, string, string) (string, error) {
	return "", f.err
}

// connectServerFake 覆盖 GetServerConfig 返回 ErrServerNotFound，驱动
// ConnectServer 的"新建服务器"分支；Connect 记录收到的 cfg 供断言。
type connectServerFake struct {
	*lifecycleManagerFake
	getErr    error
	connected *domain.ServerConfig
}

func (f *connectServerFake) GetServerConfig(context.Context, string) (*domain.ServerConfig, error) {
	return nil, f.getErr
}

func (f *connectServerFake) Connect(_ context.Context, cfg *domain.ServerConfig, _ []string, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
	f.connected = cfg
	f.audits = append(f.audits, audit)
	return nil
}

func TestMCPServiceUpdateServerOwnershipMatrix(t *testing.T) {
	t.Parallel()

	const actor = "user-1"
	const seedID = "orders"

	cases := []struct {
		name      string
		createdBy string
		role      string
		resolver  interface {
			ResolveTenantRole(context.Context, string, string) (string, error)
		}
		emptyActor bool
		wantErr    bool
	}{
		{name: "owner updates others resource", createdBy: "other-user", role: "owner"},
		{name: "owner updates unowned resource", createdBy: "", role: "owner"},
		{name: "admin updates own resource", createdBy: actor, role: "admin"},
		{name: "admin updates others resource", createdBy: "other-user", role: "admin", wantErr: true},
		{name: "admin updates unowned resource", createdBy: "", role: "admin", wantErr: true},
		{name: "member updates own resource", createdBy: actor, role: "member", wantErr: true},
		{name: "role resolution failure fails closed", createdBy: actor, resolver: failRoleResolver{err: errors.New("upstream down")}, wantErr: true},
		{name: "nil resolver fails closed", createdBy: actor, wantErr: true},
		{name: "empty actor fails closed", createdBy: actor, role: "owner", emptyActor: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			manager := &lifecycleManagerFake{stored: &domain.ServerConfig{ID: seedID, Name: "orders", CreatedBy: tc.createdBy}}
			svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
			switch {
			case tc.resolver != nil:
				svc.SetTenantRoleResolver(tc.resolver)
			case tc.role != "":
				svc.SetTenantRoleResolver(stubTenantRole{role: tc.role})
			}
			actorID := actor
			if tc.emptyActor {
				actorID = ""
			}

			err := svc.UpdateServer(t.Context(), &domain.ServerConfig{ID: seedID, Name: "orders-v2"}, actorID)

			if tc.wantErr {
				require.ErrorIs(t, err, domain.ErrForbidden)
				require.False(t, manager.updated, "forbidden update must not reach lifecycle")
			} else {
				require.NoError(t, err)
				require.True(t, manager.updated, "allowed update must reach lifecycle")
			}
		})
	}
}

func TestMCPServiceUpdateServerAuditEvent(t *testing.T) {
	t.Parallel()
	manager := &lifecycleManagerFake{stored: &domain.ServerConfig{ID: "orders", Name: "orders", CreatedBy: "user-1"}}
	svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	err := svc.UpdateServer(t.Context(), &domain.ServerConfig{ID: "orders", Name: "orders-v2"}, "user-1")
	require.NoError(t, err)

	require.Len(t, manager.audits, 1)
	ev := manager.audits[0]
	require.Equal(t, auditdomain.ResourceKindMCP, ev.ResourceKind)
	require.Equal(t, "orders", ev.ResourceID)
	require.Equal(t, auditdomain.ChangeOpUpdate, ev.Operation)
	require.Equal(t, "user-1", ev.ActorID)
	require.Equal(t, auditdomain.ChangeActorUser, ev.ActorType)
	require.Equal(t, auditdomain.ChangeSourceAPI, ev.Source)
	require.Empty(t, ev.ProposalID)
	// before/after 为脱敏投影：before 保留旧名，after 含新名，均不含凭据字段。
	require.NotEmpty(t, ev.Before)
	require.Contains(t, string(ev.Before), "orders")
	require.Contains(t, string(ev.After), "orders-v2")
}

func TestMCPServiceUpdateServerPropagatesManagerError(t *testing.T) {
	t.Parallel()
	manager := &lifecycleManagerFake{
		stored:    &domain.ServerConfig{ID: "orders", Name: "orders", CreatedBy: "user-1"},
		updateErr: errors.New("nats unavailable"),
	}
	svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	err := svc.UpdateServer(t.Context(), &domain.ServerConfig{ID: "orders", Name: "orders-v2"}, "user-1")

	// 持久化失败必须向上传播，不得吞掉（fail closed）。
	require.ErrorContains(t, err, "nats unavailable")
}

func TestMCPServiceUpdateServerSystemActorBypassesOwnership(t *testing.T) {
	t.Parallel()
	manager := &lifecycleManagerFake{stored: &domain.ServerConfig{ID: "orders", Name: "orders", CreatedBy: "other-user"}}
	svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
	// member 本应被拒绝；system actor 跳过归属校验但仍落审计。
	svc.SetTenantRoleResolver(stubTenantRole{role: "member"})

	ctx := reqctx.WithSystemActor(t.Context(), "evaluation-worker")
	err := svc.UpdateServer(ctx, &domain.ServerConfig{ID: "orders", Name: "orders-v2"}, "user-1")
	require.NoError(t, err)

	require.Len(t, manager.audits, 1)
	ev := manager.audits[0]
	require.Equal(t, "evaluation-worker", ev.ActorID)
	require.Equal(t, auditdomain.ChangeActorSystem, ev.ActorType)
	require.Equal(t, auditdomain.ChangeSourceOptimization, ev.Source)
}

func TestMCPServiceConnectServerNewAuditsCreate(t *testing.T) {
	t.Parallel()
	// ConnectServer 的 create 审计路径此前无测试覆盖，这里补齐打点断言。
	manager := &connectServerFake{lifecycleManagerFake: &lifecycleManagerFake{}, getErr: domain.ErrServerNotFound}
	svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	err := svc.ConnectServer(t.Context(), &domain.ServerConfig{ID: "orders", Name: "orders", Transport: "streamable-http"}, nil, "user-1")
	require.NoError(t, err)

	require.Len(t, manager.audits, 1)
	ev := manager.audits[0]
	require.Equal(t, auditdomain.ResourceKindMCP, ev.ResourceKind)
	require.Equal(t, auditdomain.ChangeOpCreate, ev.Operation)
	require.Equal(t, "user-1", ev.ActorID)
	require.Equal(t, auditdomain.ChangeActorUser, ev.ActorType)
	require.Equal(t, auditdomain.ChangeSourceAPI, ev.Source)
	// create 无前态：应用层 Before 为空，持久化层默认落成 {}。
	require.Empty(t, ev.Before)
	require.Contains(t, string(ev.After), "orders")
	// 新建路径把创建者写回 cfg 成为属主。
	require.Equal(t, "user-1", manager.connected.CreatedBy)
}

// TestMCPServiceUpdateServerEditorGranted pins the granted-editor row of the
// matrix: a foreign admin in the editor set may update the server config
// (update-only); the editorActor is forwarded to the lifecycle for
// in-transaction re-validation.
func TestMCPServiceUpdateServerEditorGranted(t *testing.T) {
	t.Parallel()

	manager := &lifecycleManagerFake{
		stored:  &domain.ServerConfig{ID: "orders", Name: "orders", CreatedBy: "other-user"},
		editors: []string{"user-1"},
	}
	svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "admin"})
	ctx := reqctx.WithTenantID(t.Context(), "tenant-1")

	err := svc.UpdateServer(ctx, &domain.ServerConfig{ID: "orders", Name: "orders-v2"}, "user-1")
	require.NoError(t, err)
	require.True(t, manager.updated, "granted editor update must reach lifecycle")
}

// TestMCPServiceDeleteServerEditorDenied pins the delete column: editors never
// grant delete rights; the creator passes.
func TestMCPServiceDeleteServerEditorDenied(t *testing.T) {
	t.Parallel()

	manager := &lifecycleManagerFake{
		stored:  &domain.ServerConfig{ID: "orders", Name: "orders", CreatedBy: "user-1"},
		editors: []string{"editor-1"},
	}
	svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "admin"})
	ctx := reqctx.WithTenantID(t.Context(), "tenant-1")

	err := svc.DeleteServer(ctx, "orders", "editor-1")
	require.ErrorIs(t, err, domain.ErrForbidden)
	require.Empty(t, manager.deleted, "editor must not reach lifecycle delete")

	require.NoError(t, svc.DeleteServer(ctx, "orders", "user-1"))
	require.Equal(t, "orders", manager.deleted)
}

// TestMCPServiceSetEditorsPinsManagementEndpoint covers the editor management
// endpoint: owner/creator may replace the set with an audited before/after
// projection; a granted editor cannot delegate their own right.
func TestMCPServiceSetEditorsPinsManagementEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("owner replaces editor set", func(t *testing.T) {
		t.Parallel()
		manager := &lifecycleManagerFake{
			stored:  &domain.ServerConfig{ID: "orders", Name: "orders", CreatedBy: "creator-1"},
			editors: []string{"old-editor"},
		}
		svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
		svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
		ctx := reqctx.WithTenantID(t.Context(), "tenant-1")

		require.NoError(t, svc.SetEditors(ctx, "orders", "owner-1", []string{"editor-a", "editor-b"}))
		require.Equal(t, []string{"editor-a", "editor-b"}, manager.editors)
		require.Equal(t, "owner-1", manager.replaceActor)
		require.Len(t, manager.audits, 1)

		var before, after map[string]any
		require.NoError(t, json.Unmarshal(manager.audits[0].Before, &before))
		require.NoError(t, json.Unmarshal(manager.audits[0].After, &after))
		require.Equal(t, []any{"old-editor"}, before["editors"])
		require.Equal(t, []any{"editor-a", "editor-b"}, after["editors"])
	})

	t.Run("granted editor cannot delegate", func(t *testing.T) {
		t.Parallel()
		manager := &lifecycleManagerFake{
			stored:  &domain.ServerConfig{ID: "orders", Name: "orders", CreatedBy: "creator-1"},
			editors: []string{"editor-1"},
		}
		svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())
		svc.SetTenantRoleResolver(stubTenantRole{role: "admin"})
		ctx := reqctx.WithTenantID(t.Context(), "tenant-1")

		err := svc.SetEditors(ctx, "orders", "editor-1", []string{"someone-else"})
		require.ErrorIs(t, err, domain.ErrForbidden)
		require.Equal(t, []string{"editor-1"}, manager.editors, "denied replace must not reach the repository")
	})
}

// TestMCPServiceListEditorsWrapper pins the detail prefill path: the service
// delegates straight to the manager.
func TestMCPServiceListEditorsWrapper(t *testing.T) {
	t.Parallel()
	manager := &lifecycleManagerFake{
		stored:  &domain.ServerConfig{ID: "orders", Name: "orders", CreatedBy: "creator-1"},
		editors: []string{"editor-a"},
	}
	svc := NewMCPService(&lifecycleRegistryFake{}, manager, zap.NewNop())

	editors, err := svc.ListEditors(t.Context(), "tenant-1", "orders")
	require.NoError(t, err)
	require.Equal(t, []string{"editor-a"}, editors)
}
