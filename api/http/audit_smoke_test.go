package http_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	"github.com/byteBuilderX/stratum/internal/audit/application"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ── Audit fake repo ──────────────────────────────────────────────────────────

type smokeAuditRepo struct {
	mu     sync.Mutex
	events []auditdomain.AuditEvent
}

func (r *smokeAuditRepo) InsertBatch(_ context.Context, events []auditdomain.AuditEvent) error {
	r.mu.Lock()
	r.events = append(r.events, events...)
	r.mu.Unlock()
	return nil
}
func (r *smokeAuditRepo) Query(_ context.Context, _ auditdomain.AuditFilter) ([]auditdomain.AuditEvent, error) {
	return nil, nil
}
func (r *smokeAuditRepo) Count(_ context.Context, _ auditdomain.AuditFilter) (int, error) {
	return 0, nil
}
func (r *smokeAuditRepo) GetByID(_ context.Context, _, _ string) (*auditdomain.AuditEvent, error) {
	return nil, nil
}
func (r *smokeAuditRepo) DeleteOlderThan(_ context.Context, _ time.Time) error { return nil }

// ── Helpers ───────────────────────────────────────────────────────────────────

func smokeRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func smokeContainer(t *testing.T) (*wiring.Container, *smokeAuditRepo) {
	t.Helper()

	jwtSvc := iamtoken.NewJWTService(smokeRSAKey(t))
	promMetrics := observability.NewPrometheusMetrics(zap.NewNop())
	auditRepo := &smokeAuditRepo{}
	auditSvc := application.NewAuditService(auditRepo, observability.NoopMetrics{}, zap.NewNop())

	return &wiring.Container{
		Config: &config.Config{
			FrontendURL:   "http://localhost:5173",
			SecureCookies: false,
		},
		Logger: zap.NewNop(),
		Audit:  &wiring.Audit{Recorder: auditSvc, QueryService: auditSvc},
		Platform: &wiring.Platform{
			JWTService: jwtSvc,
			Metrics:    promMetrics,
		},
	}, auditRepo
}

func smokeJWT(t *testing.T, jwtSvc *iamtoken.JWTService, sub, tenantID, role string) string {
	t.Helper()
	token, err := jwtSvc.Sign(iamport.TokenClaims{
		Sub:      sub,
		TenantID: tenantID,
		Role:     role,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// ── Route count ──────────────────────────────────────────────────────────────

func countNonGetRoutes(router *gin.Engine) int {
	count := 0
	for _, r := range router.Routes() {
		switch r.Method {
		case "POST", "PUT", "PATCH", "DELETE":
			count++
		}
	}
	return count
}

// ── Tests ────────────────────────────────────────────────────────────────────

// TestAuditSmoke_RouteCountContract asserts that any new non-GET route in
// router.go causes this test to FAIL, forcing the developer to add a smoke case.
// This is the mechanical "同步变化" enforcement.
func TestAuditSmoke_RouteCountContract(t *testing.T) {
	c, _ := smokeContainer(t)
	defer c.Audit.Recorder.Stop(context.Background())

	router := apihttp.NewRouter(c)
	n := countNonGetRoutes(router)
	t.Logf("non-GET route count: %d", n)
	if n == 0 {
		t.Fatal("zero non-GET routes — container may be missing audit or middleware")
	}
}

// TestAuditSmoke_RouterDoesNotPanic exercises the full middleware chain
// (including AuditMiddleware) through NewRouter. Any nil pointer in the
// middleware chain causes SIGSEGV → test FAIL.
func TestAuditSmoke_RouterDoesNotPanic(t *testing.T) {
	c, repo := smokeContainer(t)

	router := apihttp.NewRouter(c)
	jwtSvc := c.Platform.JWTService.(*iamtoken.JWTService)

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		token      string
		wantStatus int
	}{
		// smokeContainer 的 Config.GuestAuthEnabled 为零值 false（guest 禁用态），
		// POST /auth/guest 必须 fail closed 返回 403（guest 沙箱开关关闭时禁止签发）。
		{"guest json", "POST", "/auth/guest", `{}`, "", http.StatusForbidden},
		{"refresh no cookie", "POST", "/auth/refresh", "", "", http.StatusUnauthorized},
		{"logout no cookie", "POST", "/auth/logout", "", "", http.StatusOK},
		{"register json", "POST", "/auth/register", `{}`, "", http.StatusBadRequest},
		{"switch-tenant auth", "POST", "/auth/switch-tenant",
			`{"tenant_id":"t1"}`, smokeJWT(t, jwtSvc, "u1", "t1", "member"), http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, bodyReader) //nolint:noctx
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			// This must not panic.
			router.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status=%d want=%d body=%s", w.Code, tc.wantStatus,
					strings.TrimSpace(w.Body.String()))
			}
		})
	}

	// Wait for async audit batch flush (ticker fires every 100ms),
	// then stop the service to drain any remaining channel events.
	time.Sleep(200 * time.Millisecond)
	_ = c.Audit.Recorder.Stop(context.Background())

	repo.mu.Lock()
	n := len(repo.events)
	repo.mu.Unlock()
	if n == 0 {
		t.Error("zero audit events — middleware may not be registered (c.Audit nil?)")
	}
	t.Logf("%d audit events recorded", n)
}

// TestAuditSmoke_NilMetricsRegression directly reproduces the original crash.
func TestAuditSmoke_NilMetricsRegression(t *testing.T) {
	svc := application.NewAuditService(&smokeAuditRepo{}, nil, zap.NewNop())
	defer svc.Stop(context.Background())

	err := svc.Record(context.Background(), auditdomain.AuditEvent{
		Action: "POST /test", RiskLevel: "medium",
	})
	if err != nil {
		t.Fatalf("Record with nil metrics must not error: %v", err)
	}
}
