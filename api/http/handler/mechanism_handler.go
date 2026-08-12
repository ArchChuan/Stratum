package handler

import (
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/mechanism/application"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MechanismHandler exposes the mechanism baseline (model_profiles) management
// surface under /mechanism/profiles. 管理面依附默认租户（middleware 层校验），
// 消费路径（memory 管线）经 Service 透明取用同一份档案。
type MechanismHandler struct {
	svc    *application.Service
	logger *zap.Logger
}

// NewMechanismHandler 构造机制档案管理 handler。
func NewMechanismHandler(svc *application.Service, logger *zap.Logger) *MechanismHandler {
	return &MechanismHandler{svc: svc, logger: logger}
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
