package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// evalDeleteSvcFake 实现 evaluationDeleteService：按 resourceID 脚本化返回错误
// （nil 表示删除成功），并记录 (tenant, id, actor) 调用，供 handler 断言。
type evalDeleteSvcFake struct {
	byID  map[string]error
	err   error
	calls []deleteCall
}

type deleteCall struct{ tenant, id, actor string }

func (f *evalDeleteSvcFake) do(tenant, id, actor string) error {
	f.calls = append(f.calls, deleteCall{tenant, id, actor})
	if err, ok := f.byID[id]; ok {
		return err
	}
	return f.err
}

func (f *evalDeleteSvcFake) DeleteSuite(_ context.Context, tenant, id, actor string) error {
	return f.do(tenant, id, actor)
}
func (f *evalDeleteSvcFake) DeleteRun(_ context.Context, tenant, id, actor string) error {
	return f.do(tenant, id, actor)
}
func (f *evalDeleteSvcFake) DeleteJob(_ context.Context, tenant, id, actor string) error {
	return f.do(tenant, id, actor)
}
func (f *evalDeleteSvcFake) DeleteExperiment(_ context.Context, tenant, id, actor string) error {
	return f.do(tenant, id, actor)
}
func (f *evalDeleteSvcFake) DeleteCandidate(_ context.Context, tenant, id, actor string) error {
	return f.do(tenant, id, actor)
}
func (f *evalDeleteSvcFake) DeleteReviewItem(_ context.Context, tenant, id, actor string) error {
	return f.do(tenant, id, actor)
}
func (f *evalDeleteSvcFake) DeleteFeedback(_ context.Context, tenant, id, actor string) error {
	return f.do(tenant, id, actor)
}

// TestEvaluationDeleteHandlerStatus 覆盖删除 handler 的统一形状：
// tenant→user→service 透传；门禁/引用/未命中错误经统一中间件映射为
// 403/409/404；成功 204 空体。服务级授权矩阵由 delete_service_test 覆盖，
// 此处聚焦 handler 的错误传播与响应状态。
func TestEvaluationDeleteHandlerStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		path       string
		err        error
		wantStatus int
	}{
		{"creator deletes suite", "/evaluations/suites/suite-1", nil, http.StatusNoContent},
		{"owner deletes run", "/evaluations/runs/run-1", nil, http.StatusNoContent},
		{"non-creator forbidden", "/evaluations/suites/suite-x", evaldomain.ErrDeleteForbidden, http.StatusForbidden},
		{"referenced rejected", "/evaluations/runs/run-x", evaldomain.ErrEntityReferenced, http.StatusConflict},
		{"not found", "/evaluations/suites/missing", application.ErrSuiteNotFound, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantID := strings.TrimPrefix(tc.path, "/evaluations/suites/")
			if strings.HasPrefix(tc.path, "/evaluations/runs/") {
				wantID = strings.TrimPrefix(tc.path, "/evaluations/runs/")
			}
			svc := &evalDeleteSvcFake{byID: map[string]error{wantID: tc.err}}
			h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop()).
				WithDeleteService(svc)
			r := gin.New()
			r.Use(middleware.ErrorHandler(zap.NewNop()))
			r.DELETE("/evaluations/suites/:id", withTenantAndUser("tenant-1", "user-1"), h.DeleteSuite)
			r.DELETE("/evaluations/runs/:id", withTenantAndUser("tenant-1", "user-1"), h.DeleteRun)

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("DELETE %s: status=%d want=%d body=%s", tc.path, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusNoContent && rec.Body.Len() != 0 {
				t.Fatalf("DELETE %s: expected empty body, got %q", tc.path, rec.Body.String())
			}
			if len(svc.calls) != 1 || svc.calls[0] != (deleteCall{"tenant-1", wantID, "user-1"}) {
				t.Fatalf("DELETE %s: calls=%+v", tc.path, svc.calls)
			}
		})
	}
}

// TestEvaluationDeleteHandlerUnavailable 验证删除服务未装配时 fail-closed 503，
// 即使请求路径合法也不允许任何删除动作执行。
func TestEvaluationDeleteHandlerUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.DELETE("/evaluations/suites/:id", withTenantAndUser("tenant-1", "user-1"), h.DeleteSuite)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/evaluations/suites/suite-1", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"error":`) {
		t.Fatalf("expected frozen error body, got %q", rec.Body.String())
	}
}

// TestEvaluationDeleteHandlerRequiresIdentity 验证缺少 tenant/actor 时 fail-closed，
// 服务不被调用。
func TestEvaluationDeleteHandlerRequiresIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &evalDeleteSvcFake{}
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop()).
		WithDeleteService(svc)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.DELETE("/evaluations/suites/:id", func(c *gin.Context) { c.Next() }, h.DeleteSuite)

	// 无 tenant 也无 user 上下文：respondMissingTenant 返回 401
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/evaluations/suites/suite-1", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing identity status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.calls) != 0 {
		t.Fatalf("service must not run without identity: %+v", svc.calls)
	}
}
