package application

import (
	"context"
	"encoding/json"
	"fmt"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// UpdateModelInput carries the fields that can be updated on a model.
type UpdateModelInput struct {
	ID            string                   `json:"id"`
	DisplayName   string                   `json:"displayName"`
	Capabilities  []domain.ModelCapability `json:"capabilities"`
	ContextWindow int                      `json:"contextWindow"`
	MaxTokens     int                      `json:"maxTokens"`
	InputPrice    float64                  `json:"inputPrice"`
	OutputPrice   float64                  `json:"outputPrice"`
	Recommended   bool                     `json:"recommended"`
	// SamplingParams/MaxTemperature 为指针：nil（缺省或 null）= 保留现值；
	// 非 nil（含空 struct/0）= 覆盖为提供值（空 = 清空默认采样）。
	// MaxTokens 由 v1 的 clamp 语义升级为显式字段值（0 = 未配置，运行时注入）。
	SamplingParams *domain.SamplingParams `json:"samplingParams"`
	MaxTemperature *float64               `json:"maxTemperature"`
}

// UpdateModelPolicyInput updates only runtime policy fields. A nil pointer
// preserves the current value; the dedicated endpoint prevents discovery
// facts from being overwritten by policy edits.
type UpdateModelPolicyInput struct {
	ID                    string                 `json:"id"`
	OperatorContextWindow OptionalInt            `json:"operatorContextWindow"`
	OperatorMaxTokens     OptionalInt            `json:"operatorMaxTokens"`
	DefaultOutputTokens   OptionalInt            `json:"defaultOutputTokens"`
	SamplingParams        *domain.SamplingParams `json:"samplingParams"`
	MaxTemperature        *float64               `json:"maxTemperature"`
	// FallbackCandidates 是平台显式配置的降级候选模型名（有序，最优先在前）。
	// PATCH 语义：nil（缺省/null）= 保留现值；非 nil（含空数组）= 覆盖，空
	// 数组即清空显式候选，恢复纯隐式兜底。写入时 fail-closed 校验候选合法。
	FallbackCandidates *[]string `json:"fallbackCandidates"`
}

// OptionalInt distinguishes an omitted PATCH field from an explicit JSON null.
// Set=false means preserve, Set=true/Value=nil means clear, and a non-nil
// Value means set the positive integer after final-state validation.
type OptionalInt struct {
	Set   bool
	Value *int
}

func (v *OptionalInt) UnmarshalJSON(data []byte) error {
	v.Set = true
	if string(data) == "null" {
		v.Value = nil
		return nil
	}
	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	v.Value = &value
	return nil
}

// ModelMgmtService wraps model CRUD operations that are initiated by
// tenant administrators (as opposed to auto-discovery or runtime resolution).
type ModelMgmtService struct {
	repo        port.ModelRepository
	invalidator ModelCacheInvalidator
	health      port.ModelHealthProvider
}

// ModelCacheInvalidator evicts the global runtime model resolution cache
// after management changes. providers/models 已提升为 public 全局目录，
// 缓存不再区分租户维度，变更后全量失效。
type ModelCacheInvalidator interface {
	Invalidate()
}

// NewModelMgmtService returns a ModelMgmtService wired with the given repo.
func NewModelMgmtService(repo port.ModelRepository, invalidators ...ModelCacheInvalidator) *ModelMgmtService {
	service := &ModelMgmtService{repo: repo}
	if len(invalidators) > 0 {
		service.invalidator = invalidators[0]
	}
	return service
}

// WithHealth 注入模型健康状态投影源（平台级 HealthRegistry）。注入后目录
// 响应（List/Get/Update/UpdatePolicy）为每个模型附加运行时健康状态。
func (s *ModelMgmtService) WithHealth(h port.ModelHealthProvider) *ModelMgmtService {
	s.health = h
	return s
}

// List returns models matching the given filter.
func (s *ModelMgmtService) List(ctx context.Context, tenantID string, filter port.ModelFilter) ([]domain.Model, error) {
	models, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	for i := range models {
		models[i].Health = s.modelHealth(models[i].Name)
	}
	return models, nil
}

// Get returns a single model by ID.
func (s *ModelMgmtService) Get(ctx context.Context, tenantID, id string) (*domain.Model, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.withHealth(m), nil
}

// modelHealth 读取模型健康状态；未注入 health 源或未记录状态时返回空字符串
// （omitempty 不输出，前端按"未探活"展示）。
func (s *ModelMgmtService) modelHealth(model string) string {
	if s.health == nil {
		return ""
	}
	return s.health.ModelHealth(model)
}

func (s *ModelMgmtService) withHealth(m *domain.Model) *domain.Model {
	if m != nil {
		m.Health = s.modelHealth(m.Name)
	}
	return m
}

// Update applies partial edits to a model's display, pricing and parameter
// fields. 采样参数写时校验（temperature ≤ max_temperature 等）在持久化前
// 执行；变更与审计在同一事务提交（repo.Update 内部）。
func (s *ModelMgmtService) Update(ctx context.Context, tenantID, actorID string, input UpdateModelInput) (*domain.Model, error) {
	if err := domain.ValidateSamplingWrite(input.SamplingParams, input.MaxTemperature); err != nil {
		return nil, fmt.Errorf("model mgmt: validate: %w", err)
	}
	m, err := s.repo.Get(ctx, input.ID)
	if err != nil {
		return nil, fmt.Errorf("model mgmt: get: %w", err)
	}
	if err := validateObservedCapabilityUnchanged(*m, input); err != nil {
		return nil, err
	}
	before := modelSafeProjection(m)
	m.DisplayName = input.DisplayName
	m.Capabilities = input.Capabilities
	m.InputPrice = input.InputPrice
	m.OutputPrice = input.OutputPrice
	m.Recommended = input.Recommended
	if input.SamplingParams != nil {
		m.SamplingParams = input.SamplingParams
	}
	if input.MaxTemperature != nil {
		m.MaxTemperature = input.MaxTemperature
	}
	audit, err := newChangeAudit(ctx, changeAuditInput{
		Kind: auditdomain.ResourceKindModel, ResourceID: m.ID, Operation: auditdomain.ChangeOpUpdate,
		ActorID: actorID, Before: before, After: modelSafeProjection(m),
	})
	if err != nil {
		return nil, err
	}
	if platformRepo, ok := s.repo.(port.PlatformModelRepository); ok {
		if err := platformRepo.UpdatePlatform(ctx, m, tenantID, audit); err != nil {
			return nil, fmt.Errorf("model mgmt: update: %w", err)
		}
	} else if err := s.repo.Update(ctx, m, tenantID, audit); err != nil {
		return nil, fmt.Errorf("model mgmt: update: %w", err)
	}
	s.invalidate()
	return s.withHealth(m), nil
}

func validateObservedCapabilityUnchanged(model domain.Model, input UpdateModelInput) error {
	if input.ContextWindow > 0 && input.ContextWindow != model.ContextWindow {
		return fmt.Errorf("model mgmt: context window is discovery-managed")
	}
	if input.MaxTokens > 0 && input.MaxTokens != model.MaxTokens {
		return fmt.Errorf("model mgmt: max tokens is discovery-managed")
	}
	return nil
}

// UpdatePolicy applies a runtime policy without changing observed discovery
// facts or catalog metadata.
func (s *ModelMgmtService) UpdatePolicy(
	ctx context.Context,
	tenantID, actorID string,
	input UpdateModelPolicyInput,
) (*domain.Model, error) {
	m, err := s.repo.Get(ctx, input.ID)
	if err != nil {
		return nil, fmt.Errorf("model mgmt: get policy target: %w", err)
	}
	before := modelSafeProjection(m)
	mergeModelPolicy(m, input)
	if err := domain.ValidateOperatorPolicy(*m); err != nil {
		return nil, fmt.Errorf("model mgmt: validate policy: %w", err)
	}
	if err := s.validateFallbackCandidates(ctx, m); err != nil {
		return nil, err
	}
	audit, err := newChangeAudit(ctx, changeAuditInput{
		Kind: auditdomain.ResourceKindModel, ResourceID: m.ID, Operation: auditdomain.ChangeOpUpdate,
		ActorID: actorID, Before: before, After: modelSafeProjection(m),
	})
	if err != nil {
		return nil, err
	}
	policyRepo, ok := s.repo.(interface {
		UpdatePolicy(context.Context, *domain.Model, string, *auditdomain.ResourceChangeAuditEvent) error
	})
	if !ok {
		return nil, fmt.Errorf("model mgmt: policy repository unavailable")
	}
	if platformRepo, ok := s.repo.(port.PlatformModelRepository); ok {
		if err := platformRepo.UpdatePlatform(ctx, m, tenantID, audit); err != nil {
			return nil, fmt.Errorf("model mgmt: update policy: %w", err)
		}
	} else if err := policyRepo.UpdatePolicy(ctx, m, tenantID, audit); err != nil {
		return nil, fmt.Errorf("model mgmt: update policy: %w", err)
	}
	s.invalidate()
	return s.withHealth(m), nil
}

func mergeModelPolicy(m *domain.Model, input UpdateModelPolicyInput) {
	if input.OperatorContextWindow.Set {
		m.OperatorContextWindow = input.OperatorContextWindow.Value
	}
	if input.OperatorMaxTokens.Set {
		m.OperatorMaxTokens = input.OperatorMaxTokens.Value
	}
	if input.DefaultOutputTokens.Set {
		m.DefaultOutputTokens = input.DefaultOutputTokens.Value
	}
	if input.SamplingParams != nil {
		m.SamplingParams = input.SamplingParams
	}
	if input.MaxTemperature != nil {
		m.MaxTemperature = input.MaxTemperature
	}
	if input.FallbackCandidates != nil {
		m.FallbackCandidates = *input.FallbackCandidates
	}
}

// validateFallbackCandidates 校验模型合并后的显式降级候选（fail-closed）：
// 上限、非自身、去重，且候选必须是目录中存在、enabled、支持 chat 的模型。
// 空配置直接放行；目录查询失败向上传播（DB 故障不静默放行）。运行期解析
// 仍逐项容错跳过失效候选（见 ModelRegistry 显式候选逻辑），此处保证写入
// 即合法，双重防护。
func (s *ModelMgmtService) validateFallbackCandidates(ctx context.Context, m *domain.Model) error {
	if err := validateCandidateSet(m.FallbackCandidates, m.Name); err != nil {
		return err
	}
	chat, err := s.repo.List(ctx, port.ModelFilter{Capability: domain.CapChat})
	if err != nil {
		return fmt.Errorf("model mgmt: list chat models for fallback validation: %w", err)
	}
	return validateCandidatesInCatalog(m.FallbackCandidates, chat)
}

// validateCandidateSet 校验候选结构：长度上限、非自身、去重。全部失败路径都
// wrap 领域 sentinel（ErrInvalidFallbackCandidates）以映射 4xx，绝不落入 5xx。
func validateCandidateSet(candidates []string, self string) error {
	if len(candidates) > constants.MaxModelFallbackCandidates {
		return fmt.Errorf("%w: fallback candidates exceed max %d", domain.ErrInvalidFallbackCandidates, constants.MaxModelFallbackCandidates)
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, name := range candidates {
		if name == self {
			return fmt.Errorf("%w: fallback candidate %q must not be the model itself", domain.ErrInvalidFallbackCandidates, name)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("%w: duplicate fallback candidate %q", domain.ErrInvalidFallbackCandidates, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// validateCandidatesInCatalog 校验每个候选都出现在 enabled+chat 目录中。
// 显式检查 Enabled/Capability 是为防御 List 过滤语义演进，不依赖其内部实现。
func validateCandidatesInCatalog(candidates []string, catalog []domain.Model) error {
	if len(candidates) == 0 {
		return nil
	}
	available := make(map[string]struct{}, len(catalog))
	for _, m := range catalog {
		if !m.Enabled || !hasCapability(m.Capabilities, domain.CapChat) {
			continue
		}
		available[m.Name] = struct{}{}
	}
	for _, name := range candidates {
		if _, ok := available[name]; !ok {
			return fmt.Errorf("%w: fallback candidate %q is not an enabled chat model", domain.ErrInvalidFallbackCandidates, name)
		}
	}
	return nil
}

// Toggle enables or disables a model.
func (s *ModelMgmtService) Toggle(ctx context.Context, tenantID, id string, enabled bool) error {
	if err := s.repo.Toggle(ctx, id, enabled); err != nil {
		return err
	}
	s.invalidate()
	return nil
}

func hasCapability(caps []domain.ModelCapability, want domain.ModelCapability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// Delete removes a non-provider-managed model by ID.
func (s *ModelMgmtService) Delete(ctx context.Context, tenantID, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("model mgmt: delete: %w", err)
	}
	s.invalidate()
	return nil
}

func (s *ModelMgmtService) invalidate() {
	if s.invalidator != nil {
		s.invalidator.Invalidate()
	}
}
