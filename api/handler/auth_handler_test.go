package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/handler"
	"github.com/gin-gonic/gin"
)

func setupAuthRouter(h *handler.AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
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

func newNilDepsHandler() *handler.AuthHandler {
	return handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient: nil,
		JWTService:   nil,
		TokenStore:   nil,
		OnboardSvc:   nil,
		Logger:       nil,
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
