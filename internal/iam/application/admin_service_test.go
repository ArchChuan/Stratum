package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

// stubAdminUserRepo 是 port.AdminUserRepo 的内存实现（mock repo，不 mock service）。
type stubAdminUserRepo struct {
	roles map[string]domain.GlobalRole
}

func (s *stubAdminUserRepo) SearchUsers(context.Context, string, int) ([]port.AdminUser, error) {
	return nil, nil
}
func (s *stubAdminUserRepo) ListAdmins(context.Context) ([]port.AdminUser, error) { return nil, nil }
func (s *stubAdminUserRepo) GetGlobalRole(_ context.Context, id string) (domain.GlobalRole, error) {
	role, ok := s.roles[id]
	if !ok {
		return "", domain.ErrUserNotFound
	}
	return role, nil
}
func (s *stubAdminUserRepo) SetAdminRole(_ context.Context, id string) error {
	if s.roles[id] == domain.GlobalRoleGlobalAdmin {
		return domain.ErrForbidden
	}
	s.roles[id] = domain.GlobalRoleSystemAdmin
	return nil
}
func (s *stubAdminUserRepo) RemoveAdminRole(_ context.Context, id string) error {
	s.roles[id] = domain.GlobalRoleUser
	return nil
}

func newStubAdminRepo(roles map[string]domain.GlobalRole) *stubAdminUserRepo {
	return &stubAdminUserRepo{roles: roles}
}

func TestAdminService_SetAdminRole(t *testing.T) {
	t.Run("non-admin actor rejected", func(t *testing.T) {
		repo := newStubAdminRepo(map[string]domain.GlobalRole{
			"super": domain.GlobalRoleGlobalAdmin,
			"alice": domain.GlobalRoleUser,
		})
		svc := NewAdminService(nil, WithUserRepo(repo))

		require.ErrorIs(t, svc.SetAdminRole(context.Background(), "alice", "bob"), domain.ErrForbidden)
	})

	t.Run("global admin promotes plain user", func(t *testing.T) {
		repo := newStubAdminRepo(map[string]domain.GlobalRole{
			"super": domain.GlobalRoleGlobalAdmin,
			"alice": domain.GlobalRoleUser,
		})
		svc := NewAdminService(nil, WithUserRepo(repo))

		require.NoError(t, svc.SetAdminRole(context.Background(), "super", "alice"))
		require.Equal(t, domain.GlobalRoleSystemAdmin, repo.roles["alice"])
	})

	t.Run("cannot promote a global admin", func(t *testing.T) {
		repo := newStubAdminRepo(map[string]domain.GlobalRole{
			"super": domain.GlobalRoleGlobalAdmin,
		})
		svc := NewAdminService(nil, WithUserRepo(repo))

		require.ErrorIs(t, svc.SetAdminRole(context.Background(), "super", "super"), domain.ErrForbidden)
	})

	t.Run("missing target surfaces ErrUserNotFound", func(t *testing.T) {
		repo := newStubAdminRepo(map[string]domain.GlobalRole{
			"super": domain.GlobalRoleGlobalAdmin,
		})
		svc := NewAdminService(nil, WithUserRepo(repo))

		require.ErrorIs(t, svc.SetAdminRole(context.Background(), "super", "ghost"), domain.ErrUserNotFound)
	})

	t.Run("userRepo unset fails closed", func(t *testing.T) {
		svc := NewAdminService(nil) // 未装配 WithUserRepo
		require.ErrorIs(t, svc.SetAdminRole(context.Background(), "super", "alice"), domain.ErrUserRepoUnavailable)
	})
}

func TestAdminService_RemoveAdminRole(t *testing.T) {
	t.Run("non-admin actor rejected", func(t *testing.T) {
		repo := newStubAdminRepo(map[string]domain.GlobalRole{
			"alice": domain.GlobalRoleUser,
			"admin": domain.GlobalRoleSystemAdmin,
		})
		svc := NewAdminService(nil, WithUserRepo(repo))

		require.ErrorIs(t, svc.RemoveAdminRole(context.Background(), "alice", "admin"), domain.ErrForbidden)
	})

	t.Run("cannot remove self (global admin)", func(t *testing.T) {
		repo := newStubAdminRepo(map[string]domain.GlobalRole{
			"super": domain.GlobalRoleGlobalAdmin,
			"admin": domain.GlobalRoleSystemAdmin,
		})
		svc := NewAdminService(nil, WithUserRepo(repo))

		require.ErrorIs(t, svc.RemoveAdminRole(context.Background(), "super", "super"), domain.ErrForbidden)
	})

	t.Run("global admin removes system admin", func(t *testing.T) {
		repo := newStubAdminRepo(map[string]domain.GlobalRole{
			"super": domain.GlobalRoleGlobalAdmin,
			"admin": domain.GlobalRoleSystemAdmin,
		})
		svc := NewAdminService(nil, WithUserRepo(repo))

		require.NoError(t, svc.RemoveAdminRole(context.Background(), "super", "admin"))
		require.Equal(t, domain.GlobalRoleUser, repo.roles["admin"])
	})
}

func TestAdminService_SearchUsers_UserRepoUnavailable(t *testing.T) {
	svc := NewAdminService(nil)
	_, err := svc.SearchUsers(context.Background(), "a", 20)
	require.ErrorIs(t, err, domain.ErrUserRepoUnavailable)
}

func TestAdminService_ListAdmins_UserRepoUnavailable(t *testing.T) {
	svc := NewAdminService(nil)
	_, err := svc.ListAdmins(context.Background())
	require.ErrorIs(t, err, domain.ErrUserRepoUnavailable)
}
