package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/api/wiring"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	mechanismapp "github.com/byteBuilderX/stratum/internal/mechanism/application"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	mechanismport "github.com/byteBuilderX/stratum/internal/mechanism/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// mechanismRepoFake 是 mechanism ProfileRepo 的内存替身，供 RBAC 集成测试。
// Upsert 模拟真实 SQL 语义：族键冲突时 version 自增。
type mechanismRepoFake struct {
	profiles []mechanismdomain.Profile
}

func (f *mechanismRepoFake) GetByFamilyKey(_ context.Context, familyKey string) (mechanismdomain.Profile, bool, error) {
	for _, p := range f.profiles {
		if p.FamilyKey == familyKey {
			return p, true, nil
		}
	}
	return mechanismdomain.Profile{}, false, nil
}

func (f *mechanismRepoFake) List(_ context.Context) ([]mechanismdomain.Profile, error) {
	return f.profiles, nil
}

func (f *mechanismRepoFake) Upsert(_ context.Context, p mechanismdomain.Profile) error {
	for i, existing := range f.profiles {
		if existing.FamilyKey == p.FamilyKey {
			p.Version = existing.Version + 1
			f.profiles[i] = p
			return nil
		}
	}
	f.profiles = append(f.profiles, p)
	return nil
}

// TestMechanismProfilesRBAC 验证 /mechanism/profiles 的权限链：
// JWT → 租户上下文 → RequireTenantRole(admin) → RequireDefaultTenant → requireActive。
// 与 registerMechanism 的真实挂载（protectedTenantMiddleware + profiles.Use(requireActive)）一致。
func TestMechanismProfilesRBAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tokens := iamtoken.NewJWTService(key)
	repo := &mechanismRepoFake{profiles: []mechanismdomain.Profile{
		{FamilyKey: "qwen", Matcher: mechanismdomain.ModelMatcher{FamilyPrefixes: []string{"qwen"}},
			Status: mechanismdomain.ProfileStatusActive, Version: 1},
	}}
	c := &wiring.Container{Logger: zap.NewNop(),
		Platform:  &wiring.Platform{JWTService: tokens, Metrics: observability.NewPrometheusMetrics(zap.NewNop())},
		Mechanism: &wiring.Mechanism{Service: mechanismapp.NewService(repo)},
	}
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	requireActive := func(c *gin.Context) {
		if c.GetHeader("X-Tenant-Status") == "inactive" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tenant is not active"})
			return
		}
		c.Next()
	}
	registerMechanism(r, c, requireActive)

	// 默认租户 admin 可读可写（管理面依附默认租户迭代机制参数）。
	platformAdmin := signEvaluationToken(t, tokens, "tenant_default", "admin")
	rec := performEvaluationRequest(r, http.MethodGet, "/mechanism/profiles", platformAdmin, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("default tenant admin GET: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = performEvaluationRequest(r, http.MethodPut, "/mechanism/profiles", platformAdmin, "",
		strings.NewReader(`{"family_key":"glm","family_prefixes":["glm"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("default tenant admin PUT: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 默认租户 member 被角色门槛拦截。
	member := signEvaluationToken(t, tokens, "tenant_default", "member")
	rec = performEvaluationRequest(r, http.MethodGet, "/mechanism/profiles", member, "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("default tenant member GET: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 非默认租户 admin 被 RequireDefaultTenant 拦截（普通租户不参与机制管理）。
	otherAdmin := signEvaluationToken(t, tokens, "tenant-1", "admin")
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec = performEvaluationRequest(r, method, "/mechanism/profiles", otherAdmin, "",
			strings.NewReader(`{"family_key":"glm","family_prefixes":["glm"]}`))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("non-default tenant admin %s: status=%d body=%s", method, rec.Code, rec.Body.String())
		}
	}

	// 无 token fail closed。
	rec = performEvaluationRequest(r, http.MethodGet, "/mechanism/profiles", "", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 停用租户（default）读写均被 requireActive 拦截。
	inactive := signEvaluationToken(t, tokens, "tenant_default", "admin")
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec = performEvaluationRequest(r, method, "/mechanism/profiles", inactive, "inactive",
			strings.NewReader(`{"family_key":"glm","family_prefixes":["glm"]}`))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("inactive %s: status=%d body=%s", method, rec.Code, rec.Body.String())
		}
	}
}

// matrixEvaluatorFake 是 mechanismport.MatrixEvaluator 的内存替身（RBAC 测试
// 只验证权限链，业务链路由 MatrixService 单测覆盖）。
type matrixEvaluatorFake struct{}

func (f *matrixEvaluatorFake) ListBenchmarkSuites(context.Context, string) ([]mechanismport.BenchmarkSuite, error) {
	return nil, nil
}

func (f *matrixEvaluatorFake) EnsureBenchmarkSuite(context.Context, string) (string, error) {
	return "rev-1", nil
}

func (f *matrixEvaluatorFake) StartMatrixRun(context.Context, string, string, string, string) error {
	return nil
}

func (f *matrixEvaluatorFake) LatestMatrixRuns(context.Context, string, []string) ([]mechanismport.MatrixRun, error) {
	return nil, nil
}

// TestMechanismMatrixRBAC 验证 /mechanism/matrix 的权限链与档案管理同门槛：
// JWT → 租户上下文 → RequireTenantRole(admin) → RequireDefaultTenant → requireActive。
// 矩阵组仅在 Matrix 装配后挂载（evaluation 缺库 → 端点 404，而非空报告降级）。
func TestMechanismMatrixRBAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tokens := iamtoken.NewJWTService(key)
	repo := &mechanismRepoFake{profiles: []mechanismdomain.Profile{
		{FamilyKey: "qwen", Matcher: mechanismdomain.ModelMatcher{FamilyPrefixes: []string{"qwen"}},
			Status: mechanismdomain.ProfileStatusDraft, Version: 1},
	}}
	matrix := mechanismapp.NewMatrixService(mechanismapp.NewService(repo), &matrixEvaluatorFake{})
	c := &wiring.Container{Logger: zap.NewNop(),
		Platform:  &wiring.Platform{JWTService: tokens, Metrics: observability.NewPrometheusMetrics(zap.NewNop())},
		Mechanism: &wiring.Mechanism{Service: mechanismapp.NewService(repo), Matrix: matrix},
	}
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	requireActive := func(c *gin.Context) {
		if c.GetHeader("X-Tenant-Status") == "inactive" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tenant is not active"})
			return
		}
		c.Next()
	}
	registerMechanism(r, c, requireActive)

	// 默认租户 admin：报告可读、评测可触发、draft 档案可采纳。
	platformAdmin := signEvaluationToken(t, tokens, "tenant_default", "admin")
	rec := performEvaluationRequest(r, http.MethodGet, "/mechanism/matrix", platformAdmin, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("default tenant admin GET: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = performEvaluationRequest(r, http.MethodPost, "/mechanism/matrix/runs", platformAdmin, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("default tenant admin POST runs: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = performEvaluationRequest(r, http.MethodPost, "/mechanism/matrix/adopt", platformAdmin, "",
		strings.NewReader(`{"family_key":"qwen"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("default tenant admin POST adopt: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 默认租户 member 被角色门槛拦截。
	member := signEvaluationToken(t, tokens, "tenant_default", "member")
	rec = performEvaluationRequest(r, http.MethodGet, "/mechanism/matrix", member, "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("default tenant member GET: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 非默认租户 admin 被 RequireDefaultTenant 拦截。
	otherAdmin := signEvaluationToken(t, tokens, "tenant-1", "admin")
	rec = performEvaluationRequest(r, http.MethodGet, "/mechanism/matrix", otherAdmin, "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-default tenant admin GET: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 无 token fail closed。
	rec = performEvaluationRequest(r, http.MethodGet, "/mechanism/matrix", "", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 停用租户被 requireActive 拦截。
	inactive := signEvaluationToken(t, tokens, "tenant_default", "admin")
	rec = performEvaluationRequest(r, http.MethodGet, "/mechanism/matrix", inactive, "inactive", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("inactive GET: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 矩阵未装配（evaluation 缺库）→ 组不挂载，端点 404 fail closed。
	c2 := &wiring.Container{Logger: zap.NewNop(),
		Platform:  &wiring.Platform{JWTService: tokens, Metrics: observability.NewPrometheusMetrics(zap.NewNop())},
		Mechanism: &wiring.Mechanism{Service: mechanismapp.NewService(repo)},
	}
	r2 := gin.New()
	r2.Use(middleware.ErrorHandler(zap.NewNop()))
	registerMechanism(r2, c2, requireActive)
	rec = performEvaluationRequest(r2, http.MethodGet, "/mechanism/matrix", platformAdmin, "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("matrix unassembled GET: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
