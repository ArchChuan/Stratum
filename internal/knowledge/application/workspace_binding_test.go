package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestValidateWorkspaceBindings_allResolve(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	repo.workspaces["legal"] = &domain.Workspace{ID: "ws-legal", Name: "legal"}
	repo.workspaces["hr"] = &domain.Workspace{ID: "ws-hr", Name: "hr"}
	svc := NewWorkspaceService(repo, nil, zap.NewNop())

	require.NoError(t, svc.ValidateWorkspaceBindings(context.Background(), "t1", []string{"ws-legal", "ws-hr"}))
}

func TestValidateWorkspaceBindings_unknownIDRejected(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	repo.workspaces["legal"] = &domain.Workspace{ID: "ws-legal", Name: "legal"}
	svc := NewWorkspaceService(repo, nil, zap.NewNop())

	err := svc.ValidateWorkspaceBindings(context.Background(), "t1", []string{"ws-legal", "nope"})
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrWorkspaceNotFound)
	require.ErrorContains(t, err, "nope")
}

func TestValidateWorkspaceBindings_repoErrorFailsClosed(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	repo.getErr = errors.New("db down")
	svc := NewWorkspaceService(repo, nil, zap.NewNop())

	err := svc.ValidateWorkspaceBindings(context.Background(), "t1", []string{"ws-legal"})
	require.ErrorContains(t, err, "db down")
}

func TestValidateWorkspaceBindings_nilRepoFailsClosed(t *testing.T) {
	// Nil repo means the dependency is unverifiable → every binding rejected.
	svc := NewWorkspaceService(nil, nil, zap.NewNop())

	err := svc.ValidateWorkspaceBindings(context.Background(), "t1", []string{"ws-legal"})
	require.ErrorContains(t, err, "unavailable")
}

func TestValidateWorkspaceBindings_emptyListPasses(t *testing.T) {
	svc := NewWorkspaceService(nil, nil, zap.NewNop())

	require.NoError(t, svc.ValidateWorkspaceBindings(context.Background(), "t1", nil))
}
