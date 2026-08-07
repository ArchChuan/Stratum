package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// forwardingOnboardRepo 记录参数并返回可脚本化结果，未覆盖的方法 panic 以暴露未预期调用。
type forwardingOnboardRepo struct {
	createTenantInput  domain.CreateTenantInput
	createdUserID      string
	guestID            string
	guestLogin         string
	guestExpiresAt     time.Time
	guestErr           error
	lastNow            time.Time
	expiredGuests      []string
	found              bool
	userTenantByUserID string
}

func (r *forwardingOnboardRepo) CreateTenant(_ context.Context, in domain.CreateTenantInput) (*domain.CreateTenantResult, error) {
	r.createTenantInput = in
	return &domain.CreateTenantResult{TenantID: "t1", SchemaName: "tenant_t1"}, nil
}
func (r *forwardingOnboardRepo) CreateTenantForUser(_ context.Context, userID, name string) (string, error) {
	r.createdUserID = userID
	return "t1", nil
}
func (r *forwardingOnboardRepo) GetUserTenant(_ context.Context, githubID string) (string, string, bool, error) {
	return "u1", "t1", r.found, nil
}
func (r *forwardingOnboardRepo) GetUserTenants(_ context.Context, githubID string) (string, string, []domain.TenantInfo, bool, error) {
	return "u1", "admin", []domain.TenantInfo{{TenantID: "t1"}}, r.found, nil
}
func (r *forwardingOnboardRepo) SetGlobalRole(_ context.Context, userID, role string) error {
	r.createdUserID = userID + ":" + role
	return nil
}
func (r *forwardingOnboardRepo) GetGlobalRole(_ context.Context, userID string) (string, error) {
	return "owner", nil
}
func (r *forwardingOnboardRepo) AutoJoinDefaultTenant(_ context.Context, in domain.AutoJoinInput) (string, string, string, error) {
	return "u1", "t1", "user", nil
}
func (r *forwardingOnboardRepo) GetTenantRole(_ context.Context, userID, tenantID string) (string, error) {
	return "admin", nil
}
func (r *forwardingOnboardRepo) IsMember(_ context.Context, userID, tenantID string) (bool, error) {
	return r.found, nil
}
func (r *forwardingOnboardRepo) CreateGuestSandboxTenant(_ context.Context, githubID, githubLogin, avatarURL string, expiresAt time.Time) (string, string, error) {
	r.guestID, r.guestLogin, r.guestExpiresAt = githubID, githubLogin, expiresAt
	return "guest-uuid", "tenant-1", r.guestErr
}
func (r *forwardingOnboardRepo) ListExpiredGuests(_ context.Context, now time.Time) ([]string, error) {
	r.lastNow = now
	return r.expiredGuests, nil
}
func (r *forwardingOnboardRepo) ListOwnedNonDefaultTenants(_ context.Context, userID string) ([]string, error) {
	return []string{"t2"}, nil
}
func (r *forwardingOnboardRepo) DeleteUser(_ context.Context, userID string) error {
	return nil
}
func (r *forwardingOnboardRepo) RegisterByUsername(_ context.Context, username, passwordHash string) (string, string, error) {
	return "u1", "t1", nil
}
func (r *forwardingOnboardRepo) FindByUsername(_ context.Context, username string) (string, string, string, bool, error) {
	return "u1", "hash", "user", r.found, nil
}
func (r *forwardingOnboardRepo) FindByUsernameWithLogin(_ context.Context, username string) (string, string, string, string, bool, error) {
	return "u1", "hash", "login", "user", r.found, nil
}
func (r *forwardingOnboardRepo) FindUsernameByUserID(_ context.Context, userID string) (string, error) {
	return "username", nil
}
func (r *forwardingOnboardRepo) GetUserTenantByUserID(_ context.Context, userID string) (string, string, error) {
	return r.userTenantByUserID, "member", nil
}
func (r *forwardingOnboardRepo) UpdateProfile(_ context.Context, userID, displayName, avatarURL string) error {
	return nil
}

func TestOnboardServiceForwardsTenantOperations(t *testing.T) {
	repo := &forwardingOnboardRepo{}
	svc := NewOnboardService(repo)
	ctx := context.Background()

	in := domain.CreateTenantInput{Name: "Acme", GitHubLogin: "gh"}
	res, err := svc.CreateTenant(ctx, in)
	if err != nil || res.TenantID != "t1" || repo.createTenantInput.Name != "Acme" {
		t.Fatalf("CreateTenant = %+v, %v", res, err)
	}

	tid, err := svc.CreateTenantForUser(ctx, "u9", "Acme")
	if err != nil || tid != "t1" || repo.createdUserID != "u9" {
		t.Fatalf("CreateTenantForUser = %q, %v", tid, err)
	}

	repo.found = true
	uid, tid2, ok, err := svc.GetUserTenant(ctx, "gh-1")
	if err != nil || !ok || uid != "u1" || tid2 != "t1" {
		t.Fatalf("GetUserTenant = %q %q %v %v", uid, tid2, ok, err)
	}

	uid, role, tenants, ok, err := svc.GetUserTenants(ctx, "gh-1")
	if err != nil || !ok || uid != "u1" || role != "admin" || len(tenants) != 1 {
		t.Fatalf("GetUserTenants = %q %q %v %v %v", uid, role, len(tenants), ok, err)
	}
}

func TestOnboardServiceRoleAndMembership(t *testing.T) {
	repo := &forwardingOnboardRepo{}
	svc := NewOnboardService(repo)
	ctx := context.Background()

	if err := svc.SetGlobalRole(ctx, "u1", "owner"); err != nil {
		t.Fatal(err)
	}
	if repo.createdUserID != "u1:owner" {
		t.Fatalf("SetGlobalRole forwarded = %q", repo.createdUserID)
	}
	if role, err := svc.GetGlobalRole(ctx, "u1"); err != nil || role != "owner" {
		t.Fatalf("GetGlobalRole = %q, %v", role, err)
	}
	if role, err := svc.GetTenantRole(ctx, "u1", "t1"); err != nil || role != "admin" {
		t.Fatalf("GetTenantRole = %q, %v", role, err)
	}
	repo.found = true
	if ok, err := svc.IsMember(ctx, "u1", "t1"); err != nil || !ok {
		t.Fatalf("IsMember = %v, %v", ok, err)
	}
}

func TestOnboardServiceCreateGuest(t *testing.T) {
	// 极端情况：guest login 取 uuid 尾部随机段（前缀撞窗口），expiresAt ≈ now + TTL。
	repo := &forwardingOnboardRepo{}
	svc := NewOnboardService(repo)
	before := time.Now()

	guest, err := svc.CreateGuest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if guest.UserID != "guest-uuid" || guest.TenantID != "tenant-1" {
		t.Fatalf("guest = %+v", guest)
	}
	if !strings.HasPrefix(repo.guestID, constants.GuestGitHubIDPrefix) {
		t.Fatalf("github id prefix = %q", repo.guestID)
	}
	if len(guest.GitHubLogin) != len("guest-")+12 {
		t.Fatalf("login suffix length = %q", guest.GitHubLogin)
	}
	wantExpiry := before.Add(constants.GuestAccountTTL)
	if repo.guestExpiresAt.Before(wantExpiry.Add(-time.Minute)) || repo.guestExpiresAt.After(wantExpiry.Add(time.Minute)) {
		t.Fatalf("expiry = %v, want ≈ %v", repo.guestExpiresAt, wantExpiry)
	}
}

func TestOnboardServiceCreateGuestPropagatesRepoError(t *testing.T) {
	repo := &forwardingOnboardRepo{guestErr: context.Canceled}
	svc := NewOnboardService(repo)
	if _, err := svc.CreateGuest(context.Background()); err == nil {
		t.Fatal("repo error must propagate")
	}
}

func TestOnboardServiceGuestAndUserQueries(t *testing.T) {
	repo := &forwardingOnboardRepo{}
	svc := NewOnboardService(repo)
	ctx := context.Background()

	now := time.Now()
	got, err := svc.ListExpiredGuests(ctx, now)
	if err != nil || len(got) != 0 {
		t.Fatalf("ListExpiredGuests = %v, %v", got, err)
	}
	if !repo.lastNow.Equal(now) {
		t.Fatal("ListExpiredGuests must forward now")
	}
	repo.expiredGuests = []string{"g1"}
	got, err = svc.ListExpiredGuests(ctx, now)
	if err != nil || len(got) != 1 || got[0] != "g1" {
		t.Fatalf("ListExpiredGuests = %v, %v", got, err)
	}

	owned, err := svc.ListOwnedNonDefaultTenants(ctx, "u1")
	if err != nil || len(owned) != 1 || owned[0] != "t2" {
		t.Fatalf("ListOwnedNonDefaultTenants = %v, %v", owned, err)
	}

	if err := svc.DeleteUser(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
}

func TestOnboardServiceLocalUserFlow(t *testing.T) {
	repo := &forwardingOnboardRepo{}
	svc := NewOnboardService(repo)
	ctx := context.Background()

	uid, tid, err := svc.RegisterByUsername(ctx, "bob", "hash")
	if err != nil || uid != "u1" || tid != "t1" {
		t.Fatalf("RegisterByUsername = %q %q %v", uid, tid, err)
	}
	repo.found = true
	uid, hash, role, ok, err := svc.FindByUsername(ctx, "bob")
	if err != nil || !ok || uid != "u1" || hash != "hash" || role != "user" {
		t.Fatalf("FindByUsername = %q %q %q %v %v", uid, hash, role, ok, err)
	}
	uid, hash, login, role, ok, err := svc.FindByUsernameWithLogin(ctx, "bob")
	if err != nil || !ok || login != "login" {
		t.Fatalf("FindByUsernameWithLogin = %q %q %q %q %v %v", uid, hash, login, role, ok, err)
	}
	if u, err := svc.FindUsernameByUserID(ctx, "u1"); err != nil || u != "username" {
		t.Fatalf("FindUsernameByUserID = %q, %v", u, err)
	}
	if err := svc.UpdateProfile(ctx, "u1", "Bob", ""); err != nil {
		t.Fatal(err)
	}
}

func TestOnboardServiceGetUserTenantByUserID(t *testing.T) {
	repo := &forwardingOnboardRepo{userTenantByUserID: "t1"}
	svc := NewOnboardService(repo)
	tid, role, err := svc.GetUserTenantByUserID(context.Background(), "u1")
	if err != nil || tid != "t1" || role != "member" {
		t.Fatalf("GetUserTenantByUserID = %q %q %v", tid, role, err)
	}
}
