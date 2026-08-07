package middleware_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type fakeAuditRecorder struct {
	mu     sync.Mutex
	events []domain.AuditEvent
}

func (f *fakeAuditRecorder) Record(_ context.Context, event domain.AuditEvent) error {
	f.mu.Lock()
	f.events = append(f.events, event)
	f.mu.Unlock()
	return nil
}

func (f *fakeAuditRecorder) last() *domain.AuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.events) == 0 {
		return nil
	}
	return &f.events[len(f.events)-1]
}

func setupAuditMiddlewareRouter(recorder auditport.AuditRecorder) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.Use(middleware.AuditMiddleware(recorder))
	r.POST("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.PUT("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.PATCH("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.DELETE("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.HEAD("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.OPTIONS("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/error", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })
	return r
}

func TestAuditMiddleware_SkipsGetHeadOptions(t *testing.T) {
	rec := &fakeAuditRecorder{}
	r := setupAuditMiddlewareRouter(rec)

	for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
		req := httptest.NewRequest(method, "/test", nil) //nolint:noctx
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if rec.last() != nil {
			t.Errorf("%s should not trigger audit, got %+v", method, rec.last())
		}
	}
}

func TestAuditMiddleware_CapturesMutatingRequests(t *testing.T) {
	rec := &fakeAuditRecorder{}
	r := setupAuditMiddlewareRouter(rec)

	cases := []struct {
		method string
		path   string
		risk   string
	}{
		{"POST", "/test", "medium"},
		{"PUT", "/test", "medium"},
		{"PATCH", "/test", "medium"},
		{"DELETE", "/test", "high"},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			rec.mu.Lock()
			rec.events = nil
			rec.mu.Unlock()

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"key":"val"}`)) //nolint:noctx
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			evt := rec.last()
			if evt == nil {
				t.Fatal("expected audit event")
			}
			if evt.RiskLevel != tc.risk {
				t.Errorf("risk=%q, want %q", evt.RiskLevel, tc.risk)
			}
			if evt.Outcome != "success" {
				t.Errorf("outcome=%q, want success", evt.Outcome)
			}
		})
	}
}

func TestAuditMiddleware_ErrorStatusSetsOutcomeError(t *testing.T) {
	rec := &fakeAuditRecorder{}
	r := setupAuditMiddlewareRouter(rec)

	req := httptest.NewRequest("POST", "/error", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	evt := rec.last()
	if evt == nil {
		t.Fatal("expected audit event")
	}
	if evt.Outcome != "error" {
		t.Errorf("outcome=%q, want error", evt.Outcome)
	}
}

func TestAuditMiddleware_AnonymousActorWhenNoJWT(t *testing.T) {
	rec := &fakeAuditRecorder{}
	r := setupAuditMiddlewareRouter(rec)

	req := httptest.NewRequest("POST", "/test", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	evt := rec.last()
	if evt == nil {
		t.Fatal("expected audit event")
	}
	if evt.Actor.ActorType != domain.ActorTypeSystem || evt.Actor.ActorID != "anonymous" {
		t.Errorf("actor=%s/%s, want system/anonymous", evt.Actor.ActorType, evt.Actor.ActorID)
	}
}

func TestAuditMiddleware_ExtractsActorFromJWT(t *testing.T) {
	rec := &fakeAuditRecorder{}
	gin.SetMode(gin.TestMode)
	r2 := gin.New()
	r2.Use(middleware.ErrorHandler(zap.NewNop()))
	r2.Use(func(c *gin.Context) {
		c.Set("auth.sub", "github-user")
		c.Set("auth.tenant_id", "tenant-1")
		c.Set("request_id", "req-1")
		c.Set("trace_id", "trace-1")
		c.Next()
	})
	r2.Use(middleware.AuditMiddleware(rec))
	r2.POST("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest("POST", "/test", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r2.ServeHTTP(w, req)

	evt := rec.last()
	if evt == nil {
		t.Fatal("expected audit event")
	}
	if evt.Actor.ActorType != domain.ActorTypeUser || evt.Actor.ActorID != "github-user" {
		t.Errorf("actor=%s/%s, want user/github-user", evt.Actor.ActorType, evt.Actor.ActorID)
	}
	if evt.TenantID != "tenant-1" {
		t.Errorf("tenant=%q, want tenant-1", evt.TenantID)
	}
	if evt.RequestID != "req-1" {
		t.Errorf("request_id=%q, want req-1", evt.RequestID)
	}
	if evt.TraceID != "trace-1" {
		t.Errorf("trace_id=%q, want trace-1", evt.TraceID)
	}
}

// TestAuditMiddleware_AuthPathDropsRequestBody verifies that /auth/* bodies
// (login/register/guest carry credentials by design) are never recorded in
// the audit snapshot, while the downstream handler still receives the body.
func TestAuditMiddleware_AuthPathDropsRequestBody(t *testing.T) {
	rec := &fakeAuditRecorder{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.Use(middleware.AuditMiddleware(rec))
	r.POST("/auth/guest", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, string(body))
	})

	body := `{"password":"hunter2","token":"abc"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/guest", strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Body.String(); got != body {
		t.Fatalf("downstream handler body=%q, want original %q", got, body)
	}
	evt := rec.last()
	if evt == nil {
		t.Fatal("expected audit event")
	}
	if len(evt.After) != 0 {
		t.Errorf("auth path body must not be recorded, got %q", evt.After)
	}
}

// TestAuditMiddleware_RedactsCredentialBodies verifies that non-auth bodies
// are redacted before they reach the audit snapshot.
func TestAuditMiddleware_RedactsCredentialBodies(t *testing.T) {
	rec := &fakeAuditRecorder{}
	r := setupAuditMiddlewareRouter(rec)

	req := httptest.NewRequest(http.MethodPost, "/test", //nolint:noctx
		strings.NewReader(`{"password":"hunter2","api_key_value":"sk-123","title":"ok"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	evt := rec.last()
	if evt == nil {
		t.Fatal("expected audit event")
	}
	after := string(evt.After)
	for _, secret := range []string{"hunter2", "sk-123"} {
		if strings.Contains(after, secret) {
			t.Fatalf("credential %q leaked into audit snapshot: %q", secret, after)
		}
	}
	if !strings.Contains(after, `"title":"ok"`) {
		t.Errorf("non-credential field was lost: %q", after)
	}
	if !strings.Contains(after, "[REDACTED]") {
		t.Errorf("expected redaction marker in %q", after)
	}
}

func TestAuditMiddleware_BodyTruncation(t *testing.T) {
	rec := &fakeAuditRecorder{}
	r := setupAuditMiddlewareRouter(rec)

	// 8193-byte body → truncated to 8192
	large := strings.Repeat("x", 8193)
	req := httptest.NewRequest("POST", "/test", strings.NewReader(large)) //nolint:noctx
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	evt := rec.last()
	if evt == nil {
		t.Fatal("expected audit event")
	}
	if len(evt.After) > 8192 {
		t.Errorf("body len=%d, want <=8192", len(evt.After))
	}
}
