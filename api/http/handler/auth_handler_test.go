package handler_test

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

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
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
		auth.POST("/register", h.Register)
		auth.POST("/refresh", h.Refresh)
		auth.POST("/logout", h.Logout)
		auth.GET("/me", h.Me)
	}
	return r
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
		JWTService:       application.NewJWTService(key),
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
		JWTService:       application.NewJWTService(key),
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
