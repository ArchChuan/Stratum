package handler_test

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestRefresh_PlatformAdmin_NonMember_IssuesAdminRole(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwtSvc := iamtoken.NewJWTService(key)
	h := handler.NewAuthHandler(handler.AuthHandlerDeps{
		JWTService: jwtSvc,
		// GetActiveClaims 返回 f.claims（auth_handler_test.go:43）——必须带 claims，否则 nil 解引用 panic。
		TokenStore: &refreshTokenStoreFake{claims: &domain.StoredSession{UserID: "user-1", TenantID: "tenant-1"}},
		MembershipReader: membershipReaderFake{
			roleErr:    domain.ErrMemberNotFound,
			globalRole: string(domain.GlobalRoleSystemAdmin),
		},
		Logger: zap.NewNop(),
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/auth/refresh", h.Refresh)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/auth/refresh", nil) //nolint:noctx
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "some-refresh-token"})
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRefresh_OrdinaryUser_NonMember_Still401(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwtSvc := iamtoken.NewJWTService(key)
	h := handler.NewAuthHandler(handler.AuthHandlerDeps{
		JWTService: jwtSvc,
		TokenStore: &refreshTokenStoreFake{claims: &domain.StoredSession{UserID: "user-1", TenantID: "tenant-1"}},
		MembershipReader: membershipReaderFake{
			roleErr:    domain.ErrMemberNotFound,
			globalRole: string(domain.GlobalRoleUser),
		},
		Logger: zap.NewNop(),
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/auth/refresh", h.Refresh)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/auth/refresh", nil) //nolint:noctx
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "some-refresh-token"})
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}
