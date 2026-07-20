package handler_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	"github.com/gin-gonic/gin"
)

type refreshTokenStoreFake struct {
	claims      *domain.StoredSession
	rotateCalls int
}

func (f *refreshTokenStoreFake) Create(context.Context, string, string, string, time.Duration) error {
	return nil
}
func (f *refreshTokenStoreFake) Rotate(context.Context, string, string, time.Duration) error {
	f.rotateCalls++
	return nil
}
func (f *refreshTokenStoreFake) Revoke(context.Context, string) error { return nil }
func (f *refreshTokenStoreFake) IsBlacklisted(context.Context, string) (bool, error) {
	return false, nil
}
func (f *refreshTokenStoreFake) GetActiveClaims(context.Context, string) (*domain.StoredSession, error) {
	return f.claims, nil
}

type membershipReaderFake struct {
	roleErr error
}

type oauthExchangeStoreFake struct {
	payload      *iamport.OAuthExchange
	consumeErr   error
	consumeCalls int
	created      *iamport.OAuthExchange
	createCode   string
}

func (f *oauthExchangeStoreFake) Create(_ context.Context, exchange *iamport.OAuthExchange, _ time.Duration) (string, error) {
	f.created = exchange
	if f.createCode == "" {
		return "unused", nil
	}
	return f.createCode, nil
}

type githubOAuthFake struct{}

func (githubOAuthFake) ClientID() string { return "client-id" }
func (githubOAuthFake) ExchangeCode(context.Context, string, string) (string, error) {
	return "github-access", nil
}
func (githubOAuthFake) GetUser(context.Context, string) (*iamport.GitHubProfile, error) {
	return &iamport.GitHubProfile{ID: 42, Login: "octocat", AvatarURL: "https://example.test/avatar"}, nil
}

type onboardRepoFake struct {
	iamport.OnboardRepo
	tenants     []domain.TenantInfo
	exists      bool
	autoJoinErr error
}

func (f onboardRepoFake) GetUserTenants(context.Context, string) (string, string, []domain.TenantInfo, bool, error) {
	return "user-1", "", f.tenants, f.exists, nil
}
func (f onboardRepoFake) AutoJoinDefaultTenant(context.Context, domain.AutoJoinInput) (string, string, string, error) {
	return "", "", "", f.autoJoinErr
}

func (f *oauthExchangeStoreFake) Consume(context.Context, string) (*iamport.OAuthExchange, error) {
	f.consumeCalls++
	if f.consumeCalls > 1 {
		return nil, iamport.ErrOAuthExchangeInvalid
	}
	return f.payload, f.consumeErr
}

func (f membershipReaderFake) GetTenantRole(context.Context, string, string) (string, error) {
	return "", f.roleErr
}
func (membershipReaderFake) GetGlobalRole(context.Context, string) (string, error) { return "", nil }

func setupAuthRouter(h *handler.AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	auth := r.Group("/auth")
	{
		auth.GET("/github", h.GitHubLogin)
		auth.GET("/github/callback", h.GitHubCallback)
		auth.POST("/oauth/exchange", h.OAuthExchange)
		auth.POST("/register", h.Register)
		auth.POST("/refresh", h.Refresh)
		auth.POST("/logout", h.Logout)
		auth.GET("/me", h.Me)
	}
	return r
}

func TestAuthHandler_OAuthExchange_ConsumesLoginCodeOnce(t *testing.T) {
	store := &oauthExchangeStoreFake{payload: &iamport.OAuthExchange{
		Kind:        iamport.OAuthExchangeLogin,
		AccessToken: "access-token",
	}}
	h := handler.NewAuthHandler(handler.AuthHandlerDeps{
		OAuthExchangeStore: store,
		Logger:             zap.NewNop(),
	})
	r := setupAuthRouter(h)

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/auth/oauth/exchange", strings.NewReader(`{"code":"one-time-code"}`)) //nolint:noctx
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	first := request()
	if first.Code != http.StatusOK {
		t.Fatalf("first exchange status=%d body=%s", first.Code, first.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(first.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["access_token"] != "access-token" {
		t.Fatalf("access token missing from exchange response: %#v", body)
	}

	second := request()
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("replayed exchange status=%d body=%s", second.Code, second.Body.String())
	}
}

func TestAuthHandler_OAuthExchange_RejectsExpiredCode(t *testing.T) {
	store := &oauthExchangeStoreFake{consumeErr: iamport.ErrOAuthExchangeInvalid}
	h := handler.NewAuthHandler(handler.AuthHandlerDeps{
		OAuthExchangeStore: store,
		Logger:             zap.NewNop(),
	})
	r := setupAuthRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/exchange", strings.NewReader(`{"code":"expired-code"}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired exchange status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAuthHandler_GitHubCallback_RedirectsReturningUserWithCodeOnly(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	store := &oauthExchangeStoreFake{createCode: "one-time-code"}
	h := handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient:       githubOAuthFake{},
		JWTService:         iamtoken.NewJWTService(key),
		TokenStore:         &refreshTokenStoreFake{},
		OnboardSvc:         application.NewOnboardService(onboardRepoFake{exists: true, tenants: []domain.TenantInfo{{TenantID: "tenant-1", Role: "member"}}}),
		OAuthExchangeStore: store,
		Logger:             zap.NewNop(),
		CallbackURL:        "http://api.test/auth/github/callback",
		FrontendURL:        "https://app.test",
	})
	r := setupAuthRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=github-code&state=state", nil) //nolint:noctx
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "state"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assertOAuthCodeOnlyRedirect(t, w, "one-time-code")
	if store.created == nil || store.created.Kind != iamport.OAuthExchangeLogin || store.created.AccessToken == "" {
		t.Fatalf("login exchange not stored server-side: %#v", store.created)
	}
}

func TestAuthHandler_GitHubCallback_RedirectsOnboardingWithCodeOnly(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	store := &oauthExchangeStoreFake{createCode: "onboarding-code"}
	h := handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient:       githubOAuthFake{},
		JWTService:         iamtoken.NewJWTService(key),
		TokenStore:         &refreshTokenStoreFake{},
		OnboardSvc:         application.NewOnboardService(onboardRepoFake{autoJoinErr: errors.New("no default tenant")}),
		OAuthExchangeStore: store,
		Logger:             zap.NewNop(),
		CallbackURL:        "http://api.test/auth/github/callback",
		FrontendURL:        "https://app.test",
	})
	r := setupAuthRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=github-code&state=state", nil) //nolint:noctx
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "state"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assertOAuthCodeOnlyRedirect(t, w, "onboarding-code")
	if store.created == nil || store.created.Kind != iamport.OAuthExchangeOnboarding || store.created.OnboardingToken == "" {
		t.Fatalf("onboarding exchange not stored server-side: %#v", store.created)
	}
}

func assertOAuthCodeOnlyRedirect(t *testing.T, w *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	if w.Code != http.StatusFound {
		t.Fatalf("callback status=%d body=%s", w.Code, w.Body.String())
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	if query.Get("code") != wantCode {
		t.Fatalf("redirect code=%q, want %q", query.Get("code"), wantCode)
	}
	for _, forbidden := range []string{"access_token", "onboarding_token", "github_login", "avatar_url"} {
		if query.Has(forbidden) {
			t.Fatalf("redirect leaked %s in %q", forbidden, location.String())
		}
	}
}

func setupAuthRouterFull(h *handler.AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	auth := r.Group("/auth")
	{
		auth.GET("/github", h.GitHubLogin)
		auth.GET("/github/callback", h.GitHubCallback)
		auth.POST("/register", h.Register)
		auth.POST("/refresh", h.Refresh)
		auth.POST("/logout", h.Logout)
		auth.GET("/me", h.Me)
		auth.POST("/switch-tenant", h.SwitchTenant)
	}
	return r
}

func newNilDepsHandler() *handler.AuthHandler {
	return handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient: nil,
		JWTService:   nil,
		TokenStore:   nil,
		OnboardSvc:   nil,
		Logger:       zap.NewNop(),
		CallbackURL:  "http://localhost/auth/github/callback",
		GlobalAdmin:  "",
	})
}

func TestAuthHandler_Register_MissingOnboardingToken(t *testing.T) {
	h := newNilDepsHandler()
	r := setupAuthRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var body map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&body)
	if _, ok := body["error"]; !ok {
		t.Error("expected 'error' field in response body")
	}
}

func TestAuthHandler_Refresh_NoCookie(t *testing.T) {
	h := newNilDepsHandler()
	r := setupAuthRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthHandler_Refresh_MembershipLookupFailureDoesNotRotate(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	store := &refreshTokenStoreFake{claims: &domain.StoredSession{
		UserID: "user-1", TenantID: "tenant-1",
	}}
	h := handler.NewAuthHandler(handler.AuthHandlerDeps{
		JWTService:       iamtoken.NewJWTService(key),
		TokenStore:       store,
		MembershipReader: membershipReaderFake{roleErr: errors.New("membership unavailable")},
		Logger:           zap.NewNop(),
	})
	r := setupAuthRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil) //nolint:noctx
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "old-refresh"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("membership lookup failure returned success: %s", w.Body.String())
	}
	if store.rotateCalls != 0 {
		t.Fatalf("refresh token rotated before membership validation: calls=%d", store.rotateCalls)
	}
}

func TestAuthHandler_Refresh_RemovedMemberReturnsUnauthorized(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	store := &refreshTokenStoreFake{claims: &domain.StoredSession{
		UserID: "user-1", TenantID: "tenant-1",
	}}
	h := handler.NewAuthHandler(handler.AuthHandlerDeps{
		JWTService:       iamtoken.NewJWTService(key),
		TokenStore:       store,
		MembershipReader: membershipReaderFake{roleErr: domain.ErrMemberNotFound},
		Logger:           zap.NewNop(),
	})
	r := setupAuthRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil) //nolint:noctx
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "old-refresh"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("removed member status=%d body=%s", w.Code, w.Body.String())
	}
	if store.rotateCalls != 0 {
		t.Fatalf("removed member refresh rotated token: calls=%d", store.rotateCalls)
	}
}

func TestAuthHandler_Me_NoToken(t *testing.T) {
	h := newNilDepsHandler()
	r := setupAuthRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	var body map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&body)
	if _, ok := body["error"]; !ok {
		t.Error("expected 'error' field in response body")
	}
}

func TestAuthHandler_Logout_NoCookie(t *testing.T) {
	h := newNilDepsHandler()
	r := setupAuthRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAuthHandler_SwitchTenant_NoAuth(t *testing.T) {
	h := newNilDepsHandler()
	r := setupAuthRouterFull(h)

	req := httptest.NewRequest(http.MethodPost, "/auth/switch-tenant", strings.NewReader(`{"tenant_id":"abc"}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthHandler_SwitchTenant_MissingTenantID(t *testing.T) {
	h := newNilDepsHandler()
	r := setupAuthRouterFull(h)

	// Has Bearer header but JWTService nil → Verify returns error → 401
	req := httptest.NewRequest(http.MethodPost, "/auth/switch-tenant", strings.NewReader(`{}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sometoken")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("should not return 200 with nil JWTService")
	}
}

func TestAuthHandler_Register_InvalidAction(t *testing.T) {
	h := newNilDepsHandler()
	r := setupAuthRouter(h)

	body := `{"onboarding_token":"sometoken","action":"invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// JWTService is nil so we expect 500 (service not initialized), not 400
	// The nil-guard fires before action validation
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (nil JWTService), got %d", w.Code)
	}
}
