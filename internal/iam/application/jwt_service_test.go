package application_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	application "github.com/byteBuilderX/stratum/internal/iam/application"
)

func generateTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key
}

func TestJWT_SignAndVerify(t *testing.T) {
	key := generateTestRSAKey(t)
	svc := application.NewJWTService(key)

	claims := application.TokenClaims{
		Sub:        "user-uuid-1",
		TenantID:   "tenant-uuid-1",
		Role:       "admin",
		GlobalRole: "",
		JTI:        "jti-abc",
	}

	token, err := svc.Sign(claims, 15*time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token string")
	}

	verified, err := svc.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.Sub != claims.Sub {
		t.Errorf("Sub mismatch: got %s want %s", verified.Sub, claims.Sub)
	}
	if verified.TenantID != claims.TenantID {
		t.Errorf("TenantID mismatch: got %s want %s", verified.TenantID, claims.TenantID)
	}
	if verified.Role != claims.Role {
		t.Errorf("Role mismatch: got %s want %s", verified.Role, claims.Role)
	}
}

func TestJWT_Expired(t *testing.T) {
	key := generateTestRSAKey(t)
	svc := application.NewJWTService(key)

	claims := application.TokenClaims{Sub: "u1", TenantID: "t1", Role: "member", JTI: "jti-exp"}
	token, err := svc.Sign(claims, -1*time.Second)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	_, err = svc.Verify(token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestJWT_OnboardingToken(t *testing.T) {
	key := generateTestRSAKey(t)
	svc := application.NewJWTService(key)

	ob := application.OnboardingClaims{
		GitHubID:    42,
		GitHubLogin: "byteBuilderX",
		AvatarURL:   "https://avatars.githubusercontent.com/u/42",
	}
	token, err := svc.SignOnboarding(ob, 5*time.Minute)
	if err != nil {
		t.Fatalf("SignOnboarding: %v", err)
	}

	parsed, err := svc.VerifyOnboarding(token)
	if err != nil {
		t.Fatalf("VerifyOnboarding: %v", err)
	}
	if parsed.GitHubLogin != ob.GitHubLogin {
		t.Errorf("GitHubLogin mismatch: got %s want %s", parsed.GitHubLogin, ob.GitHubLogin)
	}
}
