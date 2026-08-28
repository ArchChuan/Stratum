package handler_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// newSwitchTenantHandler builds a real AuthHandler over fake OnboardRepo +
// real JWT signer + fake token store. onboardRepoFake already embeds the
// OnboardRepo interface, so only the methods SwitchTenant touches need
// overrides (globalRole/tenantRole/tenantActive/isMember, added in Task 4).
func newSwitchTenantHandler(repo onboardRepoFake) (*handler.AuthHandler, iamport.TokenService) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwtSvc := iamtoken.NewJWTService(key)
	h := handler.NewAuthHandler(handler.AuthHandlerDeps{
		JWTService:    jwtSvc,
		TokenStore:    &refreshTokenStoreFake{},
		OnboardSvc:    application.NewOnboardService(repo),
		Logger:        zap.NewNop(),
		FrontendURL:   "http://localhost",
		CallbackURL:   "http://localhost/cb",
		SecureCookies: false,
	})
	return h, jwtSvc
}

func switchTenantRequest(h *handler.AuthHandler, jwtSvc iamport.TokenService, tokenSub, globalRole, role string, tenantID string) (*httptest.ResponseRecorder, map[string]any) {
	claims := iamport.TokenClaims{Sub: tokenSub, TenantID: "current-tenant", Role: role, GlobalRole: globalRole}
	tok, _ := jwtSvc.Sign(claims, constants.AccessTokenTTL)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/auth/switch-tenant", h.SwitchTenant)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/auth/switch-tenant", strings.NewReader(`{"tenant_id":"`+tenantID+`"}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func TestSwitchTenant_PlatformAdmin_NonMemberAllowed(t *testing.T) {
	h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
		globalRole:   string(domain.GlobalRoleSystemAdmin),
		tenantActive: true,
		tenantRole:   "",
		isMember:     false,
	})
	w, body := switchTenantRequest(h, jwtSvc, "u1", "system_admin", "member", "foreign-tenant")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	gotRole := body["access_token"]
	if gotRole == nil {
		t.Fatal("expected access_token in response")
	}
	// 签发 token 内的 role 应为 admin（平台管理员在非所属租户提升）。
	tok, _ := gotRole.(string)
	claims, err := jwtSvc.Verify(tok)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if claims.Role != "admin" {
		t.Fatalf("issued role = %q, want admin", claims.Role)
	}
	if claims.TenantID != "foreign-tenant" {
		t.Fatalf("issued tenant = %q, want foreign-tenant", claims.TenantID)
	}
}

func TestSwitchTenant_PlatformAdmin_InactiveTenantDenied(t *testing.T) {
	h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
		globalRole:   string(domain.GlobalRoleSystemAdmin),
		tenantActive: false,
	})
	w, _ := switchTenantRequest(h, jwtSvc, "u1", "system_admin", "member", "stopped-tenant")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSwitchTenant_OrdinaryUser_NonMemberDenied(t *testing.T) {
	h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
		globalRole: string(domain.GlobalRoleUser),
		isMember:   false,
	})
	w, _ := switchTenantRequest(h, jwtSvc, "u1", "user", "member", "foreign-tenant")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSwitchTenant_OrdinaryUser_MemberAllowedKeepsRole(t *testing.T) {
	h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
		globalRole: string(domain.GlobalRoleUser),
		isMember:   true,
		tenantRole: "member",
	})
	w, body := switchTenantRequest(h, jwtSvc, "u1", "user", "member", "my-tenant")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	tok, _ := body["access_token"].(string)
	claims, err := jwtSvc.Verify(tok)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if claims.Role != "member" {
		t.Fatalf("issued role = %q, want member", claims.Role)
	}
}

func TestSwitchTenant_PlatformAdmin_OwnerKept(t *testing.T) {
	h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
		globalRole:   string(domain.GlobalRoleGlobalAdmin),
		tenantActive: true,
		tenantRole:   "owner",
		isMember:     true,
	})
	w, body := switchTenantRequest(h, jwtSvc, "u1", "global_admin", "owner", "owned-tenant")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	tok, _ := body["access_token"].(string)
	claims, err := jwtSvc.Verify(tok)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if claims.Role != "owner" {
		t.Fatalf("issued role = %q, want owner", claims.Role)
	}
}
