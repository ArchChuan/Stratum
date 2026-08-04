package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// memberTenantRepo 嵌入 settingsTenantRepo 并覆盖成员相关方法，供权限规则测试。
type memberTenantRepo struct {
	*settingsTenantRepo
	memberRole     string
	roleErr        error
	roleCalls      int
	members        []domain.Member
	total          int
	countErr       error
	listErr        error
	deleted        string
	updated        [3]string
	updatedErr     error
	userTenants    []domain.UserTenantInfo
	userTenantsErr error
}

func (r *memberTenantRepo) GetMemberRole(_ context.Context, _, userID string) (string, error) {
	r.roleCalls++
	if r.roleErr != nil {
		return "", r.roleErr
	}
	if userID == "missing" {
		return "", domain.ErrMemberNotFound
	}
	return r.memberRole, nil
}

func (r *memberTenantRepo) ListMembers(_ context.Context, _ string, limit, offset int) ([]domain.Member, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.members, nil
}

func (r *memberTenantRepo) CountMembers(_ context.Context, _ string) (int, error) {
	return r.total, r.countErr
}

func (r *memberTenantRepo) DeleteMember(_ context.Context, _ string, userID string) error {
	r.deleted = userID
	return nil
}

func (r *memberTenantRepo) UpdateMemberRole(_ context.Context, _ string, userID, newRole string) error {
	r.updated = [3]string{userID, newRole, ""}
	return r.updatedErr
}

func (r *memberTenantRepo) ListUserTenants(_ context.Context, userID string) ([]domain.UserTenantInfo, error) {
	return r.userTenants, r.userTenantsErr
}

func newMemberTenantRepo() *memberTenantRepo {
	return &memberTenantRepo{settingsTenantRepo: &settingsTenantRepo{}}
}

// errSettingsRepo 覆盖 GetTenantSettings 返回持久化错误。
type errSettingsRepo struct {
	*settingsTenantRepo
}

func (r *errSettingsRepo) GetTenantSettings(context.Context, string) (string, bool, []byte, error) {
	return "", false, nil, errors.New("db down")
}

func TestListMembersNormalisesPagination(t *testing.T) {
	// 极端情况：page/pageSize 越界归一化，offset 计算正确。
	repo := newMemberTenantRepo()
	repo.total = 3
	repo.members = []domain.Member{{UserID: "u1"}}
	svc := NewTenantService(repo, zap.NewNop())

	_, total, page, size, err := svc.ListMembers(context.Background(), "t1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || page != 1 || size != constants.DefaultPageSize {
		t.Fatalf("page=%d size=%d total=%d", page, size, total)
	}
	if size != constants.DefaultPageSize {
		t.Fatalf("default page size = %d", size)
	}
	_, _, _, size, err = svc.ListMembers(context.Background(), "t1", 1, constants.MaxPageSize+1)
	if err != nil {
		t.Fatal(err)
	}
	if size != constants.DefaultPageSize {
		t.Fatalf("oversize pageSize must fall back to default, got %d", size)
	}
}

func TestListMembersPropagatesRepoErrors(t *testing.T) {
	svc := NewTenantService(&memberTenantRepo{settingsTenantRepo: &settingsTenantRepo{}, countErr: errors.New("boom")}, zap.NewNop())
	if _, _, _, _, err := svc.ListMembers(context.Background(), "t1", 1, 10); err == nil {
		t.Fatal("count error must propagate")
	}
	svc = NewTenantService(&memberTenantRepo{settingsTenantRepo: &settingsTenantRepo{}, listErr: errors.New("boom")}, zap.NewNop())
	if _, _, _, _, err := svc.ListMembers(context.Background(), "t1", 1, 10); err == nil {
		t.Fatal("list error must propagate")
	}
}

func TestUpdateMemberRolePermissionMatrix(t *testing.T) {
	cases := []struct {
		name        string
		callerRole  string
		callerID    string
		target      string
		targetRole  string
		wantErr     error
		wantUpdated bool
	}{
		{"non-owner forbidden", "admin", "caller", "u2", "member", ErrForbiddenOwner, false},
		{"self modification forbidden", "owner", "self", "self", "member", ErrForbiddenSelfModify, false},
		{"missing target is not found", "owner", "caller", "missing", "", domain.ErrMemberNotFound, false},
		{"owner role immutable", "owner", "caller", "u2", "owner", ErrForbiddenOwnerRole, false},
		{"owner promotes member", "owner", "caller", "u2", "member", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemberTenantRepo()
			repo.memberRole = tc.targetRole
			svc := NewTenantService(repo, zap.NewNop())
			err := svc.UpdateMemberRole(context.Background(), "t1", tc.callerID, tc.callerRole, tc.target, "admin")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !tc.wantUpdated {
				t.Fatal("expected update")
			}
			if repo.updated[0] != "u2" || repo.updated[1] != "admin" {
				t.Fatalf("updated = %v", repo.updated)
			}
		})
	}
}

func TestRemoveMemberPermissionMatrix(t *testing.T) {
	cases := []struct {
		name        string
		callerRole  string
		target      string
		targetRole  string
		wantErr     error
		wantRemoved bool
	}{
		{"member forbidden", "member", "u2", "member", ErrForbiddenAdminOrOwner, false},
		{"self removal forbidden", "owner", "caller", "member", ErrForbiddenSelfModify, false},
		{"missing target is not found", "owner", "missing", "", domain.ErrMemberNotFound, false},
		{"owner removal forbidden", "owner", "owner-u", "owner", ErrForbiddenRemoveOwner, false},
		{"admin cannot remove admin", "admin", "admin-u", "admin", ErrForbiddenAdminRemove, false},
		{"owner removes admin", "owner", "admin-u", "admin", nil, true},
		{"admin removes member", "admin", "u2", "member", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemberTenantRepo()
			repo.memberRole = tc.targetRole
			svc := NewTenantService(repo, zap.NewNop())
			err := svc.RemoveMember(context.Background(), "t1", "caller", tc.callerRole, tc.target)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tc.wantRemoved && repo.deleted != tc.target {
				t.Fatalf("deleted = %q, want %q", repo.deleted, tc.target)
			}
		})
	}
}

func TestGetSettingsPropagatesUnmarshalError(t *testing.T) {
	// 极端情况：settings 非法 JSON 时返回错误。
	repo := &settingsTenantRepo{settings: []byte("{not json")}
	svc := NewTenantService(repo, zap.NewNop())
	if _, _, _, err := svc.GetSettings(context.Background(), "t1"); err == nil {
		t.Fatal("malformed settings must error")
	}
}

func TestGetSettingsRepoError(t *testing.T) {
	// 包装类型覆盖 GetTenantSettings 返回错误。
	repo := &errSettingsRepo{settingsTenantRepo: &settingsTenantRepo{}}
	svc := NewTenantService(repo, zap.NewNop())
	if _, _, _, err := svc.GetSettings(context.Background(), "t1"); err == nil {
		t.Fatal("repo error must propagate")
	}
}

func TestUpdateSettingsForbiddenRole(t *testing.T) {
	repo := newMemberTenantRepo()
	svc := NewTenantService(repo, zap.NewNop())
	err := svc.UpdateSettings(context.Background(), "t1", "member", UpdateSettingsInput{Name: "x"})
	if !errors.Is(err, ErrForbiddenAdminOrOwner) {
		t.Fatalf("err = %v", err)
	}
	if repo.settingsTenantRepo.updatedName != "" {
		t.Fatal("name must not be updated for forbidden role")
	}
}

func TestUpdateSettingsNilSettingsOnlyRenames(t *testing.T) {
	repo := newMemberTenantRepo()
	svc := NewTenantService(repo, zap.NewNop())
	if err := svc.UpdateSettings(context.Background(), "t1", "admin", UpdateSettingsInput{Name: "renamed"}); err != nil {
		t.Fatal(err)
	}
	if repo.settingsTenantRepo.updatedName != "renamed" || repo.settingsTenantRepo.updates != 0 {
		t.Fatalf("name=%q updates=%d", repo.settingsTenantRepo.updatedName, repo.settingsTenantRepo.updates)
	}
}

func TestUpdateSettingsMergesAndDropsLegacyKeys(t *testing.T) {
	// 极端情况：既有 llm_api_keys 被合并后清掉；新 key 覆盖旧值。
	repo := &settingsTenantRepo{settings: []byte(`{"llm_api_keys":{"x":"old"},"theme":"dark","locale":"en"}`)}
	svc := NewTenantService(repo, zap.NewNop())
	err := svc.UpdateSettings(context.Background(), "t1", "owner", UpdateSettingsInput{
		Settings: map[string]interface{}{"locale": "zh-CN", "new": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(repo.settings, &got); err != nil {
		t.Fatal(err)
	}
	if got["locale"] != "zh-CN" || got["theme"] != "dark" || got["new"] != float64(1) {
		t.Fatalf("merged settings = %v", got)
	}
	if _, exists := got["llm_api_keys"]; exists {
		t.Fatal("legacy keys must be dropped from merged settings")
	}
}

func TestListUserTenantsAndGetMemberRoleForward(t *testing.T) {
	repo := newMemberTenantRepo()
	repo.userTenants = []domain.UserTenantInfo{{TenantID: "t1"}}
	repo.memberRole = "admin"
	svc := NewTenantService(repo, zap.NewNop())

	got, err := svc.ListUserTenants(context.Background(), "u1")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListUserTenants = %v, %v", got, err)
	}
	role, err := svc.GetMemberRole(context.Background(), "t1", "u1")
	if err != nil || role != "admin" {
		t.Fatalf("GetMemberRole = %q, %v", role, err)
	}
}
