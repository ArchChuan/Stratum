package application

import (
	"context"
	"encoding/json"
	"fmt"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
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

// List returns models matching the given filter.
func (s *ModelMgmtService) List(ctx context.Context, tenantID string, filter port.ModelFilter) ([]domain.Model, error) {
	return s.repo.List(ctx, filter)
}

// Get returns a single model by ID.
func (s *ModelMgmtService) Get(ctx context.Context, tenantID, id string) (*domain.Model, error) {
	return s.repo.Get(ctx, id)
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
	return m, nil
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
	return m, nil
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
}

// Toggle enables or disables a model.
func (s *ModelMgmtService) Toggle(ctx context.Context, tenantID, id string, enabled bool) error {
	if err := s.repo.Toggle(ctx, id, enabled); err != nil {
		return err
	}
	s.invalidate()
	return nil
}

// SetDefaultEmbedding 设置或取消模型的默认嵌入标记。启用时校验目标
// capability 含 embedding 且 enabled（fail-closed）；repo 单事务
// clear-then-set 保证并发安全。成功后失效 registry 缓存。
func (s *ModelMgmtService) SetDefaultEmbedding(ctx context.Context, tenantID, id string, enabled bool) error {
	if enabled {
		m, err := s.repo.Get(ctx, id)
		if err != nil {
			return err
		}
		if !m.Enabled || !hasCapability(m.Capabilities, domain.CapEmbedding) {
			return fmt.Errorf("model %s is not an enabled embedding model: %w", id, domain.ErrModelNotEmbeddingEnabled)
		}
	}
	if err := s.repo.SetDefaultEmbedding(ctx, id, enabled); err != nil {
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
