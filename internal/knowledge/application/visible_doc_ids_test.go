package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/pkg/platformknowledge"
)

// errWorkspaceRepo fails every workspace lookup.
type errWorkspaceRepo struct{ err error }

func (r *errWorkspaceRepo) Create(context.Context, string, *domain.Workspace, []string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *errWorkspaceRepo) List(context.Context, string) ([]*domain.Workspace, error) {
	return nil, r.err
}
func (r *errWorkspaceRepo) GetByName(context.Context, string, string) (*domain.Workspace, error) {
	return nil, r.err
}
func (r *errWorkspaceRepo) GetByID(context.Context, string, string) (*domain.Workspace, error) {
	return nil, r.err
}
func (r *errWorkspaceRepo) UpdateWorkspaceAll(context.Context, string, string, *string, *string, domain.WorkspaceConfig, string, *auditdomain.ResourceChangeAuditEvent) error {
	return r.err
}
func (r *errWorkspaceRepo) Delete(context.Context, string, string, *auditdomain.ResourceChangeAuditEvent) error {
	return r.err
}
func (r *errWorkspaceRepo) GetConfigForUpload(context.Context, string, string) (domain.WorkspaceConfig, error) {
	return domain.WorkspaceConfig{}, r.err
}
func (r *errWorkspaceRepo) GetConfigByID(context.Context, string, string) (domain.WorkspaceConfig, error) {
	return domain.WorkspaceConfig{}, r.err
}

// failingTenantRole makes role resolution fail — every path must fail closed.
type failingTenantRole struct{}

func (failingTenantRole) ResolveTenantRole(context.Context, string, string) (string, error) {
	return "", errors.New("role backend down")
}

// recordingDocRepo captures the visibility query arguments.
type recordingDocRepo struct {
	deleteDocRepo
	gotViewerID string
	gotRole     string
	ids         []string
	err         error
}

func (r *recordingDocRepo) VisibleDocIDs(_ context.Context, _, _, viewerID, role string) ([]string, error) {
	r.gotViewerID = viewerID
	r.gotRole = role
	return r.ids, r.err
}

func newVisibleDocIDsTestService(ws *domain.Workspace, role string, docs *recordingDocRepo) *WorkspaceService {
	s := NewWorkspaceService(&deleteWorkspaceRepo{workspace: ws}, nil, zap.NewNop())
	s.SetTenantRoleResolver(stubTenantRole{role: role})
	s.SetDocRepo(docs)
	return s
}

func TestVisibleDocIDs_matrix(t *testing.T) {
	ws := &domain.Workspace{ID: "ws-1", Name: "docs", CreatedBy: "owner-1"}
	docs := &recordingDocRepo{ids: []string{"d1", "d2"}}
	ctx := context.Background()

	t.Run("platform managed workspace is unrestricted without role/doc resolution", func(t *testing.T) {
		s := NewWorkspaceService(&deleteWorkspaceRepo{workspace: &domain.Workspace{
			ID: "ws-sys", ManagementMode: platformknowledge.ManagementPlatform,
		}}, nil, zap.NewNop())
		// No role resolver, no doc repo — exemption must not touch them.
		ids, unrestricted, err := s.VisibleDocIDs(ctx, "t1", "ws-sys", "any-user")
		require.NoError(t, err)
		require.Nil(t, ids)
		require.True(t, unrestricted)
	})

	t.Run("system workspace key exempts the whitelist", func(t *testing.T) {
		s := NewWorkspaceService(&deleteWorkspaceRepo{workspace: &domain.Workspace{
			ID: "ws-sys", SystemKey: platformknowledge.SystemWorkspaceKey,
		}}, nil, zap.NewNop())
		ids, unrestricted, err := s.VisibleDocIDs(ctx, "t1", "ws-sys", "any-user")
		require.NoError(t, err)
		require.True(t, unrestricted)
		require.Nil(t, ids)
	})

	t.Run("empty viewerID fails closed", func(t *testing.T) {
		s := newVisibleDocIDsTestService(ws, "member", docs)
		_, _, err := s.VisibleDocIDs(ctx, "t1", "ws-1", "")
		require.ErrorIs(t, err, domain.ErrForbidden)
		require.Empty(t, docs.gotViewerID, "no repo query on fail closed")
	})

	t.Run("missing role resolver fails closed", func(t *testing.T) {
		s := NewWorkspaceService(&deleteWorkspaceRepo{workspace: ws}, nil, zap.NewNop())
		s.SetDocRepo(docs)
		_, _, err := s.VisibleDocIDs(ctx, "t1", "ws-1", "user-1")
		require.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("role resolution failure fails closed", func(t *testing.T) {
		s := NewWorkspaceService(&deleteWorkspaceRepo{workspace: ws}, nil, zap.NewNop())
		s.SetTenantRoleResolver(failingTenantRole{})
		s.SetDocRepo(docs)
		_, _, err := s.VisibleDocIDs(ctx, "t1", "ws-1", "user-1")
		require.ErrorIs(t, err, domain.ErrForbidden)
		require.Empty(t, docs.gotViewerID)
	})

	t.Run("tenant owner sees everything without repo query", func(t *testing.T) {
		s := newVisibleDocIDsTestService(ws, "owner", docs)
		ids, unrestricted, err := s.VisibleDocIDs(ctx, "t1", "ws-1", "owner-1")
		require.NoError(t, err)
		require.True(t, unrestricted)
		require.Nil(t, ids)
	})

	t.Run("tenant admin sees everything without repo query", func(t *testing.T) {
		s := newVisibleDocIDsTestService(ws, "admin", docs)
		ids, unrestricted, err := s.VisibleDocIDs(ctx, "t1", "ws-1", "admin-1")
		require.NoError(t, err)
		require.True(t, unrestricted)
		require.Nil(t, ids)
	})

	t.Run("workspace owner sees everything without repo query", func(t *testing.T) {
		s := newVisibleDocIDsTestService(ws, "editor", docs)
		ids, unrestricted, err := s.VisibleDocIDs(ctx, "t1", "ws-1", "owner-1")
		require.NoError(t, err)
		require.True(t, unrestricted)
		require.Nil(t, ids)
	})

	t.Run("plain user resolves whitelist through repo with role", func(t *testing.T) {
		s := newVisibleDocIDsTestService(ws, "member", docs)
		ids, unrestricted, err := s.VisibleDocIDs(ctx, "t1", "ws-1", "user-1")
		require.NoError(t, err)
		require.False(t, unrestricted)
		require.Equal(t, []string{"d1", "d2"}, ids)
		require.Equal(t, "user-1", docs.gotViewerID)
		require.Equal(t, "member", docs.gotRole)
	})

	t.Run("repo failure fails closed", func(t *testing.T) {
		failing := &recordingDocRepo{err: errors.New("db down")}
		s := newVisibleDocIDsTestService(ws, "member", failing)
		_, _, err := s.VisibleDocIDs(ctx, "t1", "ws-1", "user-1")
		require.Error(t, err)
	})

	t.Run("workspace lookup failure propagates", func(t *testing.T) {
		errRepo := &errWorkspaceRepo{err: domain.ErrWorkspaceNotFound}
		s := NewWorkspaceService(errRepo, nil, zap.NewNop())
		s.SetDocRepo(docs)
		_, _, err := s.VisibleDocIDs(ctx, "t1", "missing", "user-1")
		require.ErrorIs(t, err, domain.ErrWorkspaceNotFound)
	})
}
