package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/mechanism/application"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	mechanismport "github.com/byteBuilderX/stratum/internal/mechanism/domain/port"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// stubMatrixService 是 matrixService 的内存替身，记录入参供断言；
// profiles 非空时 AdoptProfile 模拟真实副作用（draft → active）供 readback。
type stubMatrixService struct {
	report         application.MatrixReport
	reportErr      error
	runResult      application.RunMatrixResult
	runErr         error
	adoptErr       error
	profiles       []mechanismdomain.Profile
	gotTenantID    string
	gotRequestedBy string
	gotFamilyKey   string
	gotUpdatedBy   string
}

func (s *stubMatrixService) GetMatrix(_ context.Context, tenantID string) (application.MatrixReport, error) {
	s.gotTenantID = tenantID
	if s.reportErr != nil {
		return application.MatrixReport{}, s.reportErr
	}
	return s.report, nil
}

func (s *stubMatrixService) RunMatrix(_ context.Context, tenantID, requestedBy string) (application.RunMatrixResult, error) {
	s.gotTenantID = tenantID
	s.gotRequestedBy = requestedBy
	if s.runErr != nil {
		return application.RunMatrixResult{}, s.runErr
	}
	return s.runResult, nil
}

func (s *stubMatrixService) AdoptProfile(_ context.Context, familyKey, updatedBy string) error {
	s.gotFamilyKey = familyKey
	s.gotUpdatedBy = updatedBy
	if s.adoptErr != nil {
		return s.adoptErr
	}
	for i := range s.profiles {
		if s.profiles[i].FamilyKey == familyKey {
			s.profiles[i].Status = mechanismdomain.ProfileStatusActive
		}
	}
	return nil
}

func newMechanismMatrixRouter(repo *mechanismFakeRepo, matrix matrixService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewMechanismHandler(application.NewService(repo), matrix, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	g := r.Group("/mechanism/matrix")
	g.GET("", h.MatrixReport)
	g.POST("/runs", h.RunMatrix)
	g.POST("/adopt", h.AdoptProfile)
	return r
}

func TestMechanismMatrixReportMapsSnapshot(t *testing.T) {
	repo := &mechanismFakeRepo{profiles: []mechanismdomain.Profile{mechanismProfile()}}
	matrix := &stubMatrixService{report: application.MatrixReport{
		Suites: []mechanismport.BenchmarkSuite{
			{ID: "s1", Name: "机制基线基准集", Description: "d", ActiveRevision: "v2", CaseCount: 3},
		},
		Cells: []application.MatrixCell{
			{FamilyKey: "qwen", DisplayName: "通义千问", Status: "draft", Fingerprint: "fp-1",
				Version: 3, EnrichModel: "qwen-max", RunID: "run-1", Passed: true,
				PassRate: 0.66, TotalCost: 0.12, AvgLatency: 800, TotalCases: 3, Frontier: true},
		},
		FrontierKeys: []string{"qwen"},
	}}
	r := newMechanismMatrixRouter(repo, matrix)

	rec := doJSON(t, r, http.MethodGet, "/mechanism/matrix", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp gen.MatrixReportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Suites) != 1 || resp.Suites[0].ActiveRevision != "v2" || resp.Suites[0].CaseCount != 3 {
		t.Fatalf("unexpected suites: %+v", resp.Suites)
	}
	if len(resp.Cells) != 1 || resp.Cells[0].FamilyKey != "qwen" || !resp.Cells[0].Frontier ||
		resp.Cells[0].PassRate != 0.66 || resp.Cells[0].TotalCost != 0.12 || resp.Cells[0].AvgLatency != 800 {
		t.Fatalf("unexpected cells: %+v", resp.Cells)
	}
	if len(resp.FrontierKeys) != 1 || resp.FrontierKeys[0] != "qwen" {
		t.Fatalf("unexpected frontier keys: %+v", resp.FrontierKeys)
	}
}

func TestMechanismMatrixReportPropagatesError(t *testing.T) {
	matrix := &stubMatrixService{reportErr: errors.New("boom")}
	r := newMechanismMatrixRouter(&mechanismFakeRepo{}, matrix)
	rec := doJSON(t, r, http.MethodGet, "/mechanism/matrix", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMechanismMatrixUnavailableFailsClosed(t *testing.T) {
	// evaluation 缺库时 matrix 为 nil：端点 503，不返回空报告。
	r := newMechanismMatrixRouter(&mechanismFakeRepo{}, nil)
	for _, path := range []string{"/mechanism/matrix", "/mechanism/matrix/runs"} {
		method := http.MethodGet
		if path == "/mechanism/matrix/runs" {
			method = http.MethodPost
		}
		rec := doJSON(t, r, method, path, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestMechanismMatrixRunMatrix(t *testing.T) {
	matrix := &stubMatrixService{runResult: application.RunMatrixResult{
		SuiteRevisionID: "rev-1", TriggeredCount: 2,
	}}
	r := newMechanismMatrixRouter(&mechanismFakeRepo{}, matrix)
	rec := doJSON(t, r, http.MethodPost, "/mechanism/matrix/runs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp gen.RunMatrixResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.SuiteRevisionID != "rev-1" || resp.TriggeredCount != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestMechanismMatrixRunMatrixNoProfiles(t *testing.T) {
	matrix := &stubMatrixService{runErr: application.ErrMatrixNoProfiles}
	r := newMechanismMatrixRouter(&mechanismFakeRepo{}, matrix)
	rec := doJSON(t, r, http.MethodPost, "/mechanism/matrix/runs", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMechanismMatrixAdoptProfile(t *testing.T) {
	repo := &mechanismFakeRepo{profiles: []mechanismdomain.Profile{{
		FamilyKey: "qwen", Status: mechanismdomain.ProfileStatusDraft, Version: 1,
	}}}
	matrix := &stubMatrixService{profiles: repo.profiles}
	r := newMechanismMatrixRouter(repo, matrix)
	rec := doJSON(t, r, http.MethodPost, "/mechanism/matrix/adopt", map[string]string{"family_key": "qwen"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if matrix.gotFamilyKey != "qwen" || matrix.gotUpdatedBy != "" {
		t.Fatalf("unexpected adopt args: family=%q updatedBy=%q", matrix.gotFamilyKey, matrix.gotUpdatedBy)
	}
	var resp gen.ProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.FamilyKey != "qwen" || resp.Status != mechanismdomain.ProfileStatusActive {
		t.Fatalf("unexpected adopted profile: %+v", resp)
	}
}

func TestMechanismMatrixAdoptInvalidTransition(t *testing.T) {
	repo := &mechanismFakeRepo{profiles: []mechanismdomain.Profile{{
		FamilyKey: "qwen", Status: mechanismdomain.ProfileStatusActive, Version: 1,
	}}}
	matrix := &stubMatrixService{adoptErr: application.ErrAdoptInvalidTransition}
	r := newMechanismMatrixRouter(repo, matrix)
	rec := doJSON(t, r, http.MethodPost, "/mechanism/matrix/adopt", map[string]string{"family_key": "qwen"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMechanismMatrixAdoptBindRejected(t *testing.T) {
	r := newMechanismMatrixRouter(&mechanismFakeRepo{}, &stubMatrixService{})
	rec := doJSON(t, r, http.MethodPost, "/mechanism/matrix/adopt", map[string]string{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
