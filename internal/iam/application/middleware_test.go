package application_test

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	application "github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/gin-gonic/gin"
)

func TestJWTMiddleware_ValidToken(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	svc := application.NewJWTService(key)

	claims := application.TokenClaims{Sub: "u1", TenantID: "t1", Role: "admin", JTI: "j1"}
	token, _ := svc.Sign(claims, 15*time.Minute)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(application.JWTMiddleware(svc))
	r.GET("/protected", func(c *gin.Context) {
		sub, _ := c.Get(application.ContextKeySub)
		tid, _ := c.Get(application.ContextKeyTenantID)
		c.JSON(http.StatusOK, gin.H{"sub": sub, "tid": tid})
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil) //nolint:noctx
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestJWTMiddleware_MissingToken(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	svc := application.NewJWTService(key)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(application.JWTMiddleware(svc))
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/protected", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
