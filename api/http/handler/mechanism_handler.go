package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/mechanism/application"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// errMatrixUnavailable 标记矩阵服务未装配（evaluation 缺库时 wiring 置 nil，
// 矩阵端点 fail closed，不静默返回空报告）。
var errMatrixUnavailable = errors.New("mechanism matrix: unavailable")

// matrixService 是 handler 对矩阵服务（*application.MatrixService）的依赖收窄。
type matrixService interface {
	GetMatrix(ctx context.Context, tenantID string) (application.MatrixReport, error)
	RunMatrix(ctx context.Context, tenantID, requestedBy string) (application.RunMatrixResult, error)
	AdoptProfile(ctx context.Context, familyKey, updatedBy string) error
}

// MechanismHandler exposes the mechanism baseline (model_profiles) management
// surface under /mechanism/profiles. 管理面依附默认租户（middleware 层校验），
// 消费路径（memory 管线）经 Service 透明取用同一份档案。
type MechanismHandler struct {
	svc    *application.Service
	matrix matrixService
	logger *zap.Logger
}

// NewMechanismHandler 构造机制档案管理 handler。matrix 为评测矩阵工作台
// 服务（阶段3），evaluation 缺库时可传 nil（矩阵端点 503 fail closed）。
func NewMechanismHandler(svc *application.Service, matrix matrixService, logger *zap.Logger) *MechanismHandler {
	return &MechanismHandler{svc: svc, matrix: matrix, logger: logger}
}

// List GET /mechanism/profiles — 全部模型族档案。
func (h *MechanismHandler) List(c *gin.Context) {
	profiles, err := h.svc.ListProfiles(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := gen.ListProfilesResponse{Profiles: make([]gen.ProfileResponse, 0, len(profiles))}
	for _, p := range profiles {
		resp.Profiles = append(resp.Profiles, toProfileResponse(p))
	}
	c.JSON(http.StatusOK, resp)
}

// Get GET /mechanism/profiles/:familyKey — 按族键读取档案。
func (h *MechanismHandler) Get(c *gin.Context) {
	p, err := h.svc.GetByFamilyKey(c.Request.Context(), c.Param("familyKey"))
	if err != nil {
		// 404（ErrProfileNotFound）由统一错误管线的 errorStatusTable 映射。
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toProfileResponse(p))
}

// Upsert PUT /mechanism/profiles — 建档/覆盖档案（族键冲突升级版本）。
func (h *MechanismHandler) Upsert(c *gin.Context) {
	var req gen.UpsertProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	profile := domainFromUpsert(req)
	if err := h.svc.UpsertProfile(c.Request.Context(), profile, c.GetString(middleware.ContextKeySub)); err != nil {
		// 400（ErrInvalidProfile）由统一错误管线的 errorStatusTable 映射。
		_ = c.Error(err)
		return
	}
	updated, err := h.svc.GetByFamilyKey(c.Request.Context(), req.FamilyKey)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toProfileResponse(updated))
}

// toProfileResponse 映射 domain.Profile → DTO。
func toProfileResponse(p mechanismdomain.Profile) gen.ProfileResponse {
	return gen.ProfileResponse{
		ID:             p.ID,
		FamilyKey:      p.FamilyKey,
		DisplayName:    p.DisplayName,
		FamilyPrefixes: p.Matcher.FamilyPrefixes,
		Fingerprint:    p.Fingerprint,
		//nolint:gosec // 版本号每次建档/更新自增的小整数,不可能溢出 int32(proto 契约)
		Version:   int32(p.Version),
		Status:    p.Status,
		CreatedBy: p.CreatedBy,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
		Baseline: gen.ProfileBaselineResponse{
			MemoryExtraction: p.Baseline.Prompts.MemoryExtraction,
			MemorySummary:    p.Baseline.Prompts.MemorySummary,
			MemoryEnrichment: p.Baseline.Prompts.MemoryEnrichment,
			MemorySummarize:  p.Baseline.Prompts.MemorySummarize,
			MemorySupersede:  p.Baseline.Prompts.MemorySupersede,
			Compaction:       p.Baseline.Prompts.Compaction,
			EnrichModel:      p.Baseline.Models.EnrichModel,
			SummaryModel:     p.Baseline.Models.SummaryModel,
		},
	}
}

// MatrixReport GET /mechanism/matrix — 评测矩阵工作台快照：基准集、档案
// 单元格（多维指标）与帕累托前沿标注。
func (h *MechanismHandler) MatrixReport(c *gin.Context) {
	if h.matrix == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errMatrixUnavailable))
		return
	}
	report, err := h.matrix.GetMatrix(c.Request.Context(), c.GetString(middleware.ContextKeyTenantID))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toMatrixReportResponse(report))
}

// RunMatrix POST /mechanism/matrix/runs — 触发全部档案 × 基准集矩阵评测
// （异步 job 执行，立即返回排队摘要）。
func (h *MechanismHandler) RunMatrix(c *gin.Context) {
	if h.matrix == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errMatrixUnavailable))
		return
	}
	result, err := h.matrix.RunMatrix(c.Request.Context(),
		c.GetString(middleware.ContextKeyTenantID), c.GetString(middleware.ContextKeySub))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gen.RunMatrixResponse{
		SuiteRevisionID: result.SuiteRevisionID,
		//nolint:gosec // 触发档案数为小整数,不可能溢出 int32(proto 契约)
		TriggeredCount: int32(result.TriggeredCount),
	})
}

// AdoptProfile POST /mechanism/matrix/adopt — 采纳档案（draft → active 两态
// 发布，评测采纳后置 active）。返回采纳后的档案。
func (h *MechanismHandler) AdoptProfile(c *gin.Context) {
	if h.matrix == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errMatrixUnavailable))
		return
	}
	var req gen.AdoptProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := h.matrix.AdoptProfile(c.Request.Context(), req.FamilyKey, c.GetString(middleware.ContextKeySub)); err != nil {
		// 400（ErrAdoptInvalidTransition）由统一错误管线的 errorStatusTable 映射。
		_ = c.Error(err)
		return
	}
	p, err := h.svc.GetByFamilyKey(c.Request.Context(), req.FamilyKey)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toProfileResponse(p))
}

// toMatrixReportResponse 映射 MatrixReport → DTO。
func toMatrixReportResponse(report application.MatrixReport) gen.MatrixReportResponse {
	resp := gen.MatrixReportResponse{
		Suites: make([]gen.BenchmarkSuiteResponse, 0, len(report.Suites)),
		Cells:  make([]gen.MatrixCellResponse, 0, len(report.Cells)),
	}
	for _, s := range report.Suites {
		resp.Suites = append(resp.Suites, gen.BenchmarkSuiteResponse{
			ID: s.ID, Name: s.Name, Description: s.Description,
			ActiveRevision: s.ActiveRevision,
			//nolint:gosec // case 数为小整数,不可能溢出 int32(proto 契约)
			CaseCount: int32(s.CaseCount),
		})
	}
	for _, cell := range report.Cells {
		resp.Cells = append(resp.Cells, gen.MatrixCellResponse{
			FamilyKey:    cell.FamilyKey,
			DisplayName:  cell.DisplayName,
			Status:       cell.Status,
			Fingerprint:  cell.Fingerprint,
			Version:      int32(cell.Version), //nolint:gosec // 版本自增小整数
			EnrichModel:  cell.EnrichModel,
			SummaryModel: cell.SummaryModel,
			RunID:        cell.RunID,
			Passed:       cell.Passed,
			PassRate:     cell.PassRate,
			TotalCost:    cell.TotalCost,
			AvgLatency:   cell.AvgLatency,
			TotalCases:   int32(cell.TotalCases), //nolint:gosec // case 数为小整数
			Frontier:     cell.Frontier,
		})
	}
	resp.FrontierKeys = report.FrontierKeys
	return resp
}

// domainFromUpsert 映射 Upsert DTO → domain.Profile（recall 当前保持 nil）。
func domainFromUpsert(req gen.UpsertProfileRequest) mechanismdomain.Profile {
	return mechanismdomain.Profile{
		FamilyKey:   req.FamilyKey,
		DisplayName: req.DisplayName,
		Matcher:     mechanismdomain.ModelMatcher{FamilyPrefixes: req.FamilyPrefixes},
		Status:      req.Status,
		Baseline: mechanismdomain.Baseline{
			Prompts: mechanismdomain.BaselinePrompts{
				MemoryExtraction: req.Baseline.MemoryExtraction,
				MemorySummary:    req.Baseline.MemorySummary,
				MemoryEnrichment: req.Baseline.MemoryEnrichment,
				MemorySummarize:  req.Baseline.MemorySummarize,
				MemorySupersede:  req.Baseline.MemorySupersede,
				Compaction:       req.Baseline.Compaction,
			},
			Models: mechanismdomain.BaselineModels{
				EnrichModel:  req.Baseline.EnrichModel,
				SummaryModel: req.Baseline.SummaryModel,
			},
		},
	}
}
