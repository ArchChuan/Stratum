package application

import (
	"context"
	"fmt"

	"strings"

	"github.com/google/uuid"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// genULID generates a unique identifier using UUID v7 (time-ordered).
func genULID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// CreateProviderInput carries the fields required to create a new provider.
type CreateProviderInput struct {
	Name    string              `json:"name"`
	Kind    domain.ProviderKind `json:"kind"`
	BaseURL string              `json:"baseUrl"`
	APIKey  string              `json:"apiKey"`
}

// UpdateProviderInput carries the fields that can be updated on a provider.
type UpdateProviderInput struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Kind            domain.ProviderKind `json:"kind"`
	BaseURL         string              `json:"baseUrl"`
	APIKey          string              `json:"apiKey"`
	DefaultModel    string              `json:"defaultModel"`
	DefaultSampling *map[string]any     `json:"defaultSampling"`
}

// ProviderService orchestrates LLM provider CRUD operations and
// provider-managed tasks such as model discovery and health checks.
type ProviderService struct {
	repo        port.ProviderRepository
	modelRepo   port.ModelRepository
	runtime     port.ProviderRuntime
	invalidator ModelCacheInvalidator
}

// NewProviderService returns a ProviderService wired with the given
// repository and protocol map.
func NewProviderService(
	repo port.ProviderRepository,
	modelRepo port.ModelRepository,
	runtime port.ProviderRuntime,
	invalidators ...ModelCacheInvalidator,
) *ProviderService {
	service := &ProviderService{
		repo:      repo,
		modelRepo: modelRepo,
		runtime:   runtime,
	}
	if len(invalidators) > 0 {
		service.invalidator = invalidators[0]
	}
	return service
}

// Create persists a new provider and kicks off best-effort model discovery.
func (s *ProviderService) Create(ctx context.Context, tenantID string, input CreateProviderInput) (*domain.Provider, error) {
	p := &domain.Provider{
		ID:      genULID(),
		Name:    input.Name,
		Kind:    input.Kind,
		BaseURL: input.BaseURL,
		APIKey:  input.APIKey,
		Enabled: true,
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return nil, fmt.Errorf("provider service: create: %w", err)
	}
	s.invalidate()
	// Best-effort model discovery — log but never fail the create operation.
	_, _ = s.DiscoverModels(ctx, tenantID, p.ID)
	return p, nil
}

// List returns all providers for a tenant.
func (s *ProviderService) List(ctx context.Context, tenantID string) ([]domain.Provider, error) {
	return s.repo.List(ctx)
}

// Get returns a single provider by ID.
func (s *ProviderService) Get(ctx context.Context, tenantID, id string) (*domain.Provider, error) {
	return s.repo.Get(ctx, id)
}

// Update applies partial updates to an existing provider.
// An empty APIKey means "keep existing". 元数据经 GetMeta 读取（不解密旧 key）：
// 存量明文/损坏密文的 provider 带新 key 重新保存必须可用，先解密旧 key
// 会把该 provider 永久锁死（Get 保持 fail closed 不变）。变更与审计在
// 同一事务提交（repo.Update 内部）。
func (s *ProviderService) Update(ctx context.Context, tenantID, actorID string, input UpdateProviderInput) (*domain.Provider, error) {
	existing, err := s.repo.GetMeta(ctx, input.ID)
	if err != nil {
		return nil, fmt.Errorf("provider service: get for update: %w", err)
	}
	before := providerSafeProjection(existing)
	existing.Name = input.Name
	existing.Kind = input.Kind
	existing.BaseURL = input.BaseURL
	existing.DefaultModel = input.DefaultModel
	if input.APIKey != "" {
		existing.APIKey = input.APIKey
	}
	if input.DefaultSampling != nil {
		existing.DefaultSampling = *input.DefaultSampling
	}
	audit, err := newChangeAudit(ctx, changeAuditInput{
		Kind: auditdomain.ResourceKindProvider, ResourceID: existing.ID, Operation: auditdomain.ChangeOpUpdate,
		ActorID: actorID, Before: before, After: providerSafeProjection(existing),
	})
	if err != nil {
		return nil, err
	}
	if platformRepo, ok := s.repo.(port.PlatformProviderRepository); ok {
		if err := platformRepo.UpdatePlatform(ctx, existing, tenantID, audit); err != nil {
			return nil, fmt.Errorf("provider service: update: %w", err)
		}
	} else if err := s.repo.Update(ctx, existing, tenantID, audit); err != nil {
		return nil, fmt.Errorf("provider service: update: %w", err)
	}
	s.invalidate()
	return existing, nil
}

// Delete removes a provider by ID. Associated models are cascade-deleted by FK.
func (s *ProviderService) Delete(ctx context.Context, tenantID, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("provider service: delete: %w", err)
	}
	s.invalidate()
	return nil
}

// DiscoverModels queries the provider's API for available models and upserts
// them into the model repository. Model capabilities are inferred from the
// model name — models containing "embed" are classified as embedding, others
// default to chat.
func (s *ProviderService) DiscoverModels(ctx context.Context, tenantID, providerID string) ([]domain.Model, error) {
	provider, err := s.repo.Get(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("discover models: %w", err)
	}
	if s.runtime == nil {
		return nil, fmt.Errorf("discover models: no runtime for kind %q", provider.Kind)
	}
	discovered, err := s.runtime.ListModels(ctx, *provider)
	if err != nil {
		return nil, fmt.Errorf("discover models: list from provider: %w", err)
	}
	models := make([]domain.Model, 0, len(discovered))
	for _, dm := range discovered {
		models = append(models, domain.Model{
			ProviderID:          providerID,
			Name:                dm.Name,
			DisplayName:         dm.Name,
			Capabilities:        inferCapabilities(dm.Name),
			ContextWindow:       dm.ContextWindow,
			MaxTokens:           dm.MaxOutputTokens,
			ContextWindowSource: domain.CapabilitySourceProviderAPI,
			MaxTokensSource:     domain.CapabilitySourceProviderAPI,
			ProviderManaged:     true,
			Enabled:             true,
		})
	}
	upserted, err := s.modelRepo.UpsertDiscovered(ctx, providerID, models)
	if err != nil {
		return nil, err
	}
	s.invalidate()
	return upserted, nil
}

func (s *ProviderService) invalidate() {
	if s.invalidator != nil {
		s.invalidator.Invalidate()
	}
}

// inferCapabilities deduces model capabilities from the model name using
// provider-agnostic naming conventions. 嵌入模型独占 CapEmbedding（不混 chat）；
// 其余至少 CapChat+CapToolUse，多模态/推理模型按命名规则追加 CapVision/CapReasoning
// （必须保留 CapChat，否则被 capability='chat' 过滤出 Agent 下拉）。推理打标与
// infrastructure/model_catalog.go 的 reasoningModels 清单保持同步。
// CapToolUse 对 chat 模型默认开启：主流 OpenAI-compatible chat 模型均支持
// function calling，网关 L4 按能力集拦截需要工具调用的请求；若某模型实际
// 不支持，管理员可在模型管理手动去掉 tool_use 标签（误标时由 provider 运行时
// 报错暴露，可自愈，与"空能力集 = unknown 放行"语义互不影响）。
func inferCapabilities(name string) []domain.ModelCapability {
	lower := strings.ToLower(name)
	if isEmbeddingModelName(lower) {
		return []domain.ModelCapability{domain.CapEmbedding}
	}
	caps := []domain.ModelCapability{domain.CapChat, domain.CapToolUse}
	if isVisionModelName(lower) {
		caps = append(caps, domain.CapVision)
	}
	if isReasoningModelName(lower) {
		caps = append(caps, domain.CapReasoning)
	}
	return caps
}

// isEmbeddingModelName 判断是否为嵌入模型命名。embed 子串是通用约定；
// 扩展覆盖非 "embed" 命名的常见嵌入模型族（bge/m3e/e5/gte/text2vec），
// 避免它们被误判为 chat 模型。
func isEmbeddingModelName(lower string) bool {
	if strings.Contains(lower, "embed") {
		return true
	}
	for _, p := range []string{"bge", "m3e", "e5", "gte", "text2vec"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// isVisionModelName 判断是否为多模态模型。主流多模态模型族前缀 + "-vl"
// 变体（qwen-vl/yi-vl 等）。命名匹配保持保守：只命中已知多模态族，避免
// 短前缀误判。
func isVisionModelName(lower string) bool {
	for _, p := range []string{
		"claude", "gpt-4o", "gpt-4.1", "gpt-4-turbo",
		"gemini", "llava", "internvl",
		"glm-4v", "glm-4.1v", "glm-4.5v", "glm-4.6v", "glm-5v",
		"qwen-vl", "yi-vl", "step-1v",
	} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return strings.Contains(lower, "-vl")
}

// isReasoningModelName 判断模型名是否为已知推理模型。命名规则：o1/o3/o4、
// qwq 前缀，deepseek-reasoner exact-match。发现打标会写入 DB models.capabilities，
// 是网关能力来源之一（DB∨catalog 并集）——与 infrastructure/model_catalog.go
// 的 reasoningModels 清单保持同步：新增推理模型须两处都登记。
func isReasoningModelName(lower string) bool {
	if lower == "deepseek-reasoner" {
		return true
	}
	for _, prefix := range []string{"o1", "o3", "o4", "qwq", "glm-z1"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// HealthCheck verifies that the provider is reachable by calling the
// configured health model endpoint.
func (s *ProviderService) HealthCheck(ctx context.Context, tenantID, providerID string) error {
	provider, err := s.repo.Get(ctx, providerID)
	if err != nil {
		return err
	}
	if s.runtime == nil {
		return fmt.Errorf("no protocol for kind %q", provider.Kind)
	}
	return s.runtime.Health(ctx, *provider)
}
