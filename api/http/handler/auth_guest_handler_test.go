package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	"github.com/gin-gonic/gin"
)

// guestTokenStoreFake mirrors refreshTokenStoreFake from auth_handler_test.go
// (external test package), which is not visible from this internal test
// package; GuestLogin only needs the no-op implementations.
type guestTokenStoreFake struct{}

func (f *guestTokenStoreFake) Create(context.Context, string, string, string, time.Duration) error {
	return nil
}
func (f *guestTokenStoreFake) Rotate(context.Context, string, string, time.Duration) error {
	return nil
}
func (f *guestTokenStoreFake) Revoke(context.Context, string) error { return nil }
func (f *guestTokenStoreFake) IsBlacklisted(context.Context, string) (bool, error) {
	return false, nil
}
func (f *guestTokenStoreFake) GetActiveClaims(context.Context, string) (*domain.StoredSession, error) {
	return nil, nil
}

// guestSandboxRepoFake records the sandbox provisioning call; all other
// OnboardRepo methods are nil-safe stubs (GuestLogin only calls CreateGuest).
type guestSandboxRepoFake struct {
	iamport.OnboardRepo
	guestID    string
	guestLogin string
	createErr  error
}

func (f *guestSandboxRepoFake) CreateGuestSandboxTenant(_ context.Context, githubID, githubLogin, _ string, _ time.Time) (string, string, error) {
	f.guestID, f.guestLogin = githubID, githubLogin
	return "guest-uuid", "sandbox-tenant", f.createErr
}

type schemaProvisionerFake struct {
	provisionErr error
	activateErr  error
	provisioned  []string
	activated    []string
	failed       []string
}

func (f *schemaProvisionerFake) ProvisionSchema(ctx context.Context, tenantID string) error {
	f.provisioned = append(f.provisioned, tenantID)
	return f.provisionErr
}
func (f *schemaProvisionerFake) ActivateTenant(ctx context.Context, tenantID string) error {
	f.activated = append(f.activated, tenantID)
	return f.activateErr
}
func (f *schemaProvisionerFake) MarkProvisioningFailed(ctx context.Context, tenantID string) error {
	f.failed = append(f.failed, tenantID)
	return nil
}

func newGuestLoginRouter(t *testing.T, enabled bool, svc *application.OnboardService, provisioner iamport.TenantSchemaProvisioner) (*gin.Engine, *iamtoken.JWTService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwtSvc := iamtoken.NewJWTService(key)
	h := NewAuthHandler(AuthHandlerDeps{
		JWTService:        jwtSvc,
		TokenStore:        &guestTokenStoreFake{},
		OnboardSvc:        svc,
		SchemaProvisioner: provisioner,
		Logger:            zap.NewNop(),
		GuestAuthEnabled:  enabled,
	})
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/auth/guest", h.GuestLogin)
	return r, jwtSvc
}

func guestLoginRequest(r *gin.Engine) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/guest", strings.NewReader("{}")) //nolint:noctx
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestNewGuestLoginResponseIncludesUserIdentity(t *testing.T) {
	guest := &application.GuestAccount{
		UserID:      "guest-user",
		TenantID:    "sandbox-tenant",
		GitHubLogin: "guest-demo",
		AvatarURL:   "",
	}

	resp := newGuestLoginResponse(guest, "access-token", domain.SystemRoleUser)

	if resp.AccessToken != "access-token" || resp.TenantID != "sandbox-tenant" {
		t.Fatalf("unexpected token response: %+v", resp)
	}
	if resp.User.Sub != "guest-user" || resp.User.TenantID != "sandbox-tenant" {
		t.Fatalf("unexpected user identity: %+v", resp.User)
	}
	if resp.User.Role != "owner" || resp.User.SystemRole != string(domain.SystemRoleUser) {
		t.Fatalf("unexpected user roles: %+v", resp.User)
	}
	if resp.User.GitHubLogin != "guest-demo" {
		t.Fatalf("unexpected guest login: %+v", resp.User)
	}
}

// TestGuestLoginDisabledFailsClosed: when the feature switch is off the
// unauthenticated endpoint must be rejected before any account/tenant is
// provisioned — no default role, no default tenant, no token.
func TestGuestLoginDisabledFailsClosed(t *testing.T) {
	svc := application.NewOnboardService(&guestSandboxRepoFake{})
	r, _ := newGuestLoginRouter(t, false, svc, &schemaProvisionerFake{})

	rec := guestLoginRequest(r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "guest login disabled") {
		t.Fatalf("body = %q, want disabled error", rec.Body.String())
	}
}

// TestGuestLoginSandboxProvisioned: enabled path provisions a fresh sandbox
// tenant for the guest (never the default tenant), activates its schema, and
// issues a token scoped to the sandbox tenant with the owner role.
func TestGuestLoginSandboxProvisioned(t *testing.T) {
	repo := &guestSandboxRepoFake{}
	svc := application.NewOnboardService(repo)
	provisioner := &schemaProvisionerFake{}
	r, jwtSvc := newGuestLoginRouter(t, true, svc, provisioner)

	rec := guestLoginRequest(r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s, want 201", rec.Code, rec.Body.String())
	}
	if len(provisioner.provisioned) != 1 || provisioner.provisioned[0] != "sandbox-tenant" {
		t.Fatalf("provisioned = %v, want [sandbox-tenant]", provisioner.provisioned)
	}
	if len(provisioner.activated) != 1 || provisioner.activated[0] != "sandbox-tenant" {
		t.Fatalf("activated = %v, want [sandbox-tenant]", provisioner.activated)
	}
	if !strings.HasPrefix(repo.guestID, "guest:") || !strings.HasPrefix(repo.guestLogin, "guest-") {
		t.Fatalf("guest identity = %q / %q", repo.guestID, repo.guestLogin)
	}

	var body struct {
		AccessToken string `json:"access_token"`
		TenantID    string `json:"tenant_id"`
		User        struct {
			Role string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TenantID != "sandbox-tenant" {
		t.Fatalf("tenant_id = %q, want sandbox-tenant (guest must never land in the default tenant)", body.TenantID)
	}
	if body.User.Role != "owner" {
		t.Fatalf("user.role = %q, want owner", body.User.Role)
	}

	// The issued access token must itself be scoped to the sandbox tenant.
	claims, err := jwtSvc.Verify(body.AccessToken)
	if err != nil {
		t.Fatalf("verify access token: %v", err)
	}
	if claims.TenantID != "sandbox-tenant" || claims.Sub != "guest-uuid" {
		t.Fatalf("claims = %+v, want sandbox tenant + guest sub", claims)
	}
	if claims.Role != "owner" || claims.GlobalRole != "" {
		t.Fatalf("claims role = %q global = %q, want owner with no global role", claims.Role, claims.GlobalRole)
	}
}

// TestGuestLoginProvisionFailure: schema provisioning failure must surface as
// an error and must not issue any token.
func TestGuestLoginProvisionFailure(t *testing.T) {
	svc := application.NewOnboardService(&guestSandboxRepoFake{})
	provisioner := &schemaProvisionerFake{provisionErr: errors.New("ddl failed")}
	r, _ := newGuestLoginRouter(t, true, svc, provisioner)

	rec := guestLoginRequest(r)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if len(provisioner.failed) != 1 || provisioner.failed[0] != "sandbox-tenant" {
		t.Fatalf("provisioning failures = %v, want [sandbox-tenant]", provisioner.failed)
	}
}
