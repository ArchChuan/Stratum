package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestEvaluationEvolutionRoutesRBAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tokens := iamtoken.NewJWTService(key)
	queryRepo := &evaluationQueryRepoFake{}
	c := &wiring.Container{Logger: zap.NewNop(), Platform: &wiring.Platform{JWTService: tokens}, Evaluation: &wiring.Evaluation{
		SuiteService: application.NewSuiteService(nil), JobService: application.NewJobService(nil, nil),
		QueryService: application.NewQueryService(queryRepo),
	}}
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	requireActive := func(c *gin.Context) {
		if c.GetHeader("X-Tenant-Status") == "inactive" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tenant is not active"})
			return
		}
		c.Next()
	}
	registerEvaluations(r, c, requireActive)

	member := signEvaluationToken(t, tokens, "tenant-1", "member")
	for _, path := range []string{"/evaluations/overview", "/evaluations/resources", "/evaluations/suites",
		"/evaluations/runs", "/evaluations/candidates", "/evaluations/experiments",
		"/evaluations/resources/skill/skill-1/timeline"} {
		rec := performEvaluationRequest(r, http.MethodGet, path, member, "", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("member GET %s: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/evaluations/candidates/candidate-1/reject", "/evaluations/experiments/experiment-1/pause",
		"/evaluations/experiments/experiment-1/promote", "/evaluations/experiments/experiment-1/rollback"} {
		rec := performEvaluationRequest(r, http.MethodPost, path, member, "", strings.NewReader(`{}`))
		if rec.Code != http.StatusForbidden {
			t.Errorf("member POST %s: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	admin := signEvaluationToken(t, tokens, "tenant-1", "admin")
	rec := performEvaluationRequest(r, http.MethodPost, "/evaluations/experiments/experiment-1/pause", admin, "inactive", strings.NewReader(`{}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("inactive admin status=%d body=%s", rec.Code, rec.Body.String())
	}

	other := signEvaluationToken(t, tokens, "tenant-2", "member")
	rec = performEvaluationRequest(r, http.MethodGet, "/evaluations/resources/skill/skill-1/timeline", other, "", nil)
	if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Body.String(), `{"error":`) {
		t.Fatalf("cross tenant status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func signEvaluationToken(t *testing.T, svc iamport.TokenService, tenantID, role string) string {
	t.Helper()
	token, err := svc.Sign(iamport.TokenClaims{Sub: "user-1", TenantID: tenantID, Role: role,
		SystemRole: iamdomain.SystemRoleUser, JTI: tenantID + role}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func performEvaluationRequest(r http.Handler, method, path, token, status string, body *strings.Reader) *httptest.ResponseRecorder {
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, body)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if status != "" {
		req.Header.Set("X-Tenant-Status", status)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

type evaluationQueryRepoFake struct{}

func (*evaluationQueryRepoFake) Overview(context.Context, string) (domain.CenterOverview, error) {
	return domain.CenterOverview{}, nil
}
func (*evaluationQueryRepoFake) ListResources(context.Context, string, port.CenterFilter) (domain.ResourcePage, error) {
	return domain.ResourcePage{}, nil
}
func (*evaluationQueryRepoFake) ListSuites(context.Context, string, port.CenterFilter) (domain.SuitePage, error) {
	return domain.SuitePage{}, nil
}
func (*evaluationQueryRepoFake) ListRuns(context.Context, string, port.CenterFilter) (domain.RunPage, error) {
	return domain.RunPage{}, nil
}
func (*evaluationQueryRepoFake) ListCandidates(context.Context, string, port.CenterFilter) (domain.CandidatePage, error) {
	return domain.CandidatePage{}, nil
}
func (*evaluationQueryRepoFake) ListExperiments(context.Context, string, port.CenterFilter) (domain.ExperimentPage, error) {
	return domain.ExperimentPage{}, nil
}
func (*evaluationQueryRepoFake) Timeline(_ context.Context, tenantID string, _ port.CenterFilter) (domain.TimelinePage, error) {
	if tenantID != "tenant-1" {
		return domain.TimelinePage{}, port.ErrCenterResourceNotFound
	}
	return domain.TimelinePage{}, nil
}
