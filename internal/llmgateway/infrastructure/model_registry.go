package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

type resolvedEntry struct {
	config       ProviderConfig
	provider     domain.Provider
	capabilities []domain.ModelCapability
	policy       *ModelPolicy
	expires      time.Time
}

// ModelPolicy 是模型权威数据的运行时快照（缓存预计算，避免每请求 N+1
// 查询 DB）。nil 表示模型记录不存在（权威数据缺失 → 网关 L1-L3 跳过 +
// WARN + 指标）。
type ModelPolicy struct {
	MaxTokens                int
	ContextWindow            int
	DefaultOutputTokens      int
	MaxTemperature           *float64
	SamplingDefaults         *domain.SamplingParams
	ProviderSamplingDefaults *domain.SamplingParams
	Capabilities             []domain.ModelCapability
	Reasoning                bool
}

// policyFromModel 从模型记录构造 policy；nil 输入返回 nil。Reasoning 保持
// DB∨catalog 并集语义（与 ResolveReasoning 旧逻辑一致）：DB capabilities
// 含 CapReasoning 或 catalog 已知推理模型均视为推理模型，预计算后 cache
// 短路不丢语义（catalog 是纯函数，无 DB 查询）。
func policyFromModel(m *domain.Model) *ModelPolicy {
	if m == nil {
		return nil
	}
	reasoning := false
	for _, c := range m.Capabilities {
		if c == domain.CapReasoning {
			reasoning = true
			break
		}
	}
	if !reasoning {
		reasoning = ModelSupportsReasoning(m.Name)
	}
	effective := m.EffectivePolicy()
	return &ModelPolicy{
		MaxTokens:           effective.MaxOutputTokens,
		ContextWindow:       effective.ContextWindow,
		DefaultOutputTokens: effective.DefaultOutputTokens,
		MaxTemperature:      m.MaxTemperature,
		SamplingDefaults:    m.SamplingParams,
		Capabilities:        m.Capabilities,
		Reasoning:           reasoning,
	}
}

func policyFromProvider(p *domain.Provider) *ModelPolicy {
	if p == nil {
		return nil
	}
	return &ModelPolicy{ProviderSamplingDefaults: samplingParamsFromMap(p.DefaultSampling)}
}

func policyWithProvider(m *domain.Model, p *domain.Provider) *ModelPolicy {
	policy := policyFromModel(m)
	if policy == nil {
		return policyFromProvider(p)
	}
	policy.ProviderSamplingDefaults = samplingParamsFromMap(p.DefaultSampling)
	return policy
}

func samplingParamsFromMap(values map[string]any) *domain.SamplingParams {
	if len(values) == 0 {
		return nil
	}
	value, ok := values["temperature"].(float64)
	if !ok {
		return nil
	}
	return &domain.SamplingParams{Temperature: &value}
}

// errModelNotResolved 是全局解析链的内部 sentinel：某一级未命中，交由下一级
// 兜底；链尾仍未命中时转换为带模型名的明确错误（fail-closed，禁止默认放行）。
// 它不是业务可识别错误，不对外暴露。
var errModelNotResolved = errors.New("model registry: model not resolved")

// ErrModelNotInCatalog 表示显式请求的模型不在全局目录（未登记、已禁用或
// provider 不可用）。配置层失效必须 fail-closed 并进入监控报警链路，禁止
// 静默降级到其他模型。
var ErrModelNotInCatalog = errors.New("llmgateway: requested model not in catalog")

// ErrNoDefaultModel 表示请求未显式指定模型，且目录中没有任何可用的默认
// 模型（provider default_model / recommended 均缺失）。无兜底模型属于配置
// 缺陷，必须 fail-closed 并进入监控报警链路。
var ErrNoDefaultModel = errors.New("llmgateway: no default model available")

// ModelRegistry wraps a ModelRepository and ProviderRepository with an
// in-memory cache and resolves model names to provider config + protocol.
// models/providers 已提升为 public 平台全局目录，注册表不再区分租户维度。
type ModelRegistry struct {
	modelRepo    port.ModelRepository
	providerRepo port.ProviderRepository
	chatProtos   map[domain.ProviderKind]ChatProtocol
	embedProtos  map[domain.ProviderKind]EmbedProtocol
	health       *HealthRegistry
	cacheTTL     time.Duration

	mu    sync.RWMutex
	cache map[string]*resolvedEntry // "chat:"|"embed:"+modelName -> entry（单层全局缓存）
}

// NewModelRegistry returns a new ModelRegistry.
func NewModelRegistry(
	modelRepo port.ModelRepository,
	providerRepo port.ProviderRepository,
	chatProtos map[domain.ProviderKind]ChatProtocol,
	embedProtos map[domain.ProviderKind]EmbedProtocol,
	cacheTTL time.Duration,
) *ModelRegistry {
	return &ModelRegistry{
		modelRepo:    modelRepo,
		providerRepo: providerRepo,
		chatProtos:   chatProtos,
		embedProtos:  embedProtos,
		cacheTTL:     cacheTTL,
		cache:        make(map[string]*resolvedEntry),
	}
}

// WithHealth 注入 HealthRegistry。解析链据此做健康感知：unhealthy/halfOpen
// 模型视为未命中继续降级，显式指定同样不选中不可用模型（fail-closed）。
// 未注入（纯配置/测试）时所有模型视为可用。
func (r *ModelRegistry) WithHealth(h *HealthRegistry) *ModelRegistry {
	r.health = h
	return r
}

// isModelUsable 判定模型是否可被解析链选中：healthy/degraded → true（degraded
// 已降级但仍可用）；unhealthy/halfOpen → false（已熔断，禁止选中）。health 未
// 注入时恒 true。
func (r *ModelRegistry) isModelUsable(model string) bool {
	if r.health == nil {
		return true
	}
	st := r.health.Get(model)
	return st.Status != ModelHealthUnhealthy && st.Status != ModelHealthHalfOpen
}

// entryUsable 检查缓存 entry 对应的模型是否健康可用（M1：TTL 缓存命中前先
// 查健康）。unhealthy/halfOpen 视为不可用，调用方应走解析链重选而非直接消费
// 缓存。
func (r *ModelRegistry) entryUsable(e *resolvedEntry) bool {
	if r.health == nil {
		return true
	}
	for _, m := range e.config.Models {
		if !r.isModelUsable(m) {
			return false
		}
	}
	return true
}

// Resolve resolves modelName to a provider configuration and chat protocol.
// 空 modelName 表示「未显式指定」，跳过 ① 精确匹配，直接走全局默认解析链
// （② provider.default_model → ③ models.recommended → ⑤ fail-closed）。
func (r *ModelRegistry) Resolve(ctx context.Context, modelName string) (ProviderConfig, ChatProtocol, error) {
	cacheKey := "chat:" + modelName
	if e := r.cacheGet(cacheKey); e != nil && r.entryUsable(e) {
		return r.chatFromEntry(e)
	}
	cfg, provider, err := r.resolveModel(ctx, modelName, cacheKey, domain.CapChat, false)
	if err != nil {
		return ProviderConfig{}, nil, err
	}
	proto, ok := r.chatProtos[provider.Kind]
	if !ok {
		return ProviderConfig{}, nil, fmt.Errorf("model registry: no chat protocol for %q", provider.Kind)
	}
	return cfg, proto, nil
}

// ResolveEmbedding resolves modelName to a provider configuration and embedding
// protocol. 空 modelName 跳过 ①，直接走全局默认解析链（② → ③ → ④
// default_embedding 标记 → ⑤ fail-closed）。
func (r *ModelRegistry) ResolveEmbedding(ctx context.Context, modelName string) (ProviderConfig, EmbedProtocol, error) {
	cacheKey := "embed:" + modelName
	if e := r.cacheGet(cacheKey); e != nil && r.entryUsable(e) {
		return r.embedFromEntry(e)
	}
	cfg, provider, err := r.resolveModel(ctx, modelName, cacheKey, domain.CapEmbedding, true)
	if err != nil {
		return ProviderConfig{}, nil, err
	}
	proto, ok := r.embedProtos[provider.Kind]
	if !ok {
		return ProviderConfig{}, nil, fmt.Errorf("model registry: no embed protocol for %q", provider.Kind)
	}
	return cfg, proto, nil
}

// ResolveEmbeddingExact 精确解析租户显式配置的嵌入模型：仅匹配目录中 enabled
// 且支持 embedding 能力的同名模型，未命中返回 errModelNotResolved，绝不兜底
// 到 provider 默认或推荐模型。用于租户显式配置的 fail-closed 校验——配置拼写
// 错误必须暴露，而不是静默切换到其他模型。
func (r *ModelRegistry) ResolveEmbeddingExact(ctx context.Context, modelName string) (ProviderConfig, EmbedProtocol, error) {
	if modelName == "" {
		return ProviderConfig{}, nil, errModelNotResolved
	}
	cacheKey := "embed:" + modelName
	cfg, provider, err := r.resolveExact(ctx, modelName, cacheKey, domain.CapEmbedding)
	if err != nil {
		return ProviderConfig{}, nil, err
	}
	proto, ok := r.embedProtos[provider.Kind]
	if !ok {
		return ProviderConfig{}, nil, fmt.Errorf("model registry: no embed protocol for %q", provider.Kind)
	}
	return cfg, proto, nil
}

// chatFromEntry 从缓存 entry 取出 chat 协议并校验协议存在。
func (r *ModelRegistry) chatFromEntry(e *resolvedEntry) (ProviderConfig, ChatProtocol, error) {
	proto, ok := r.chatProtos[e.provider.Kind]
	if !ok {
		return ProviderConfig{}, nil, fmt.Errorf("model registry: no chat protocol for provider kind %q", e.provider.Kind)
	}
	return e.config, proto, nil
}

// embedFromEntry 从缓存 entry 取出 embedding 协议并校验协议存在。
func (r *ModelRegistry) embedFromEntry(e *resolvedEntry) (ProviderConfig, EmbedProtocol, error) {
	proto, ok := r.embedProtos[e.provider.Kind]
	if !ok {
		return ProviderConfig{}, nil, fmt.Errorf("model registry: no embed protocol for provider kind %q", e.provider.Kind)
	}
	return e.config, proto, nil
}

// resolveModel 执行全局 5 级解析链，返回命中的 provider 配置与 provider 信息。
// embeddingDefault 为 true 时在链尾追加 ④ default_embedding 兜底（仅 embedding）。
// 真实错误（DB 故障、provider 缺失）立即传播，禁止降级掩盖；只有「未命中」
// 才继续下一级兜底。
func (r *ModelRegistry) resolveModel(
	ctx context.Context,
	modelName, cacheKey string,
	capability domain.ModelCapability,
	embeddingDefault bool,
) (ProviderConfig, domain.Provider, error) {
	if modelName != "" {
		cfg, p, err := r.resolveExact(ctx, modelName, cacheKey, capability)
		if err == nil {
			return cfg, p, nil
		}
		if !errors.Is(err, errModelNotResolved) {
			return ProviderConfig{}, domain.Provider{}, err
		}
		// 显式请求的模型不在目录（未登记/已禁用/provider 不可用）：fail-closed，
		// 禁止静默降级到 provider 默认或推荐模型——失效模型配置必须暴露给
		// 监控报警链路，而不是悄悄换模型。
		return ProviderConfig{}, domain.Provider{}, fmt.Errorf("%w: %q", ErrModelNotInCatalog, modelName)
	}
	for _, step := range []modelResolveStep{r.resolveProviderDefault, r.resolveRecommended} {
		cfg, p, err := step(ctx, cacheKey, capability)
		if err == nil {
			return cfg, p, nil
		}
		if !errors.Is(err, errModelNotResolved) {
			return ProviderConfig{}, domain.Provider{}, err
		}
	}
	if embeddingDefault {
		cfg, p, err := r.resolveEmbeddingMarked(ctx, cacheKey)
		if err == nil {
			return cfg, p, nil
		}
		if !errors.Is(err, errModelNotResolved) {
			return ProviderConfig{}, domain.Provider{}, err
		}
	}
	// 未显式指定模型且目录无任何默认/推荐模型：配置缺陷，fail-closed 并进入
	// 监控报警链路（代码内不写死兜底模型）。
	return ProviderConfig{}, domain.Provider{}, fmt.Errorf(
		"%w: no default %s model in global catalog", ErrNoDefaultModel, capability)
}

// modelResolveStep 是全局解析链的一级兜底：返回 nil 表示命中；返回
// errModelNotResolved 表示该级未命中；其他错误为真实故障，必须传播。
type modelResolveStep func(ctx context.Context, cacheKey string, capability domain.ModelCapability) (ProviderConfig, domain.Provider, error)

// resolveExact 是 ① 级：models 精确匹配（enabled + provider enabled +
// 能力匹配）。未命中返回 errModelNotResolved。
func (r *ModelRegistry) resolveExact(
	ctx context.Context,
	modelName, cacheKey string,
	capability domain.ModelCapability,
) (ProviderConfig, domain.Provider, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, port.ModelFilter{Enabled: &enabled, Capability: capability})
	if err != nil {
		return ProviderConfig{}, domain.Provider{}, fmt.Errorf("model registry: list models: %w", err)
	}
	models = usableModels(models, r.isModelUsable)
	for _, m := range models {
		if m.Name != modelName {
			continue
		}
		provider, err := r.providerRepo.Get(ctx, m.ProviderID)
		if err != nil {
			return ProviderConfig{}, domain.Provider{}, fmt.Errorf("model registry: get provider: %w", err)
		}
		if !provider.Enabled || !r.supports(provider.Kind, capability) {
			continue
		}
		cfg := ProviderConfig{
			Name:        provider.Name,
			BaseURL:     provider.BaseURL,
			APIKey:      provider.APIKey,
			HealthModel: provider.DefaultModel,
			Models:      []string{m.Name},
		}
		r.cacheSet(cacheKey, cfg, *provider, m.Capabilities, policyWithProvider(&m, provider))
		return cfg, *provider, nil
	}
	return ProviderConfig{}, domain.Provider{}, errModelNotResolved
}

// resolveProviderDefault 是 ② 级：取 enabled 且 kind 支持能力的 provider 的
// default_model（HealthModel 复用）。多个 provider 时按 name 排序取第一个，
// 保证确定性。未命中返回 errModelNotResolved。
func (r *ModelRegistry) resolveProviderDefault(
	ctx context.Context,
	cacheKey string,
	capability domain.ModelCapability,
) (ProviderConfig, domain.Provider, error) {
	providers, err := r.providerRepo.List(ctx)
	if err != nil {
		return ProviderConfig{}, domain.Provider{}, fmt.Errorf("model registry: list providers: %w", err)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	for i := range providers {
		p := providers[i]
		if !p.Enabled || p.DefaultModel == "" || !r.supports(p.Kind, capability) || !r.isModelUsable(p.DefaultModel) {
			continue
		}
		cfg := ProviderConfig{
			Name:        p.Name,
			BaseURL:     p.BaseURL,
			APIKey:      p.APIKey,
			HealthModel: p.DefaultModel,
			Models:      []string{p.DefaultModel},
		}
		r.cacheSet(cacheKey, cfg, p, []domain.ModelCapability{capability}, policyFromProvider(&p))
		return cfg, p, nil
	}
	return ProviderConfig{}, domain.Provider{}, errModelNotResolved
}

// resolveRecommended 是 ③ 级：取 enabled 且 Recommended 标记的全局默认
// chat/embed 模型（provider 可用 + 能力匹配），name 排序第一个。未命中返回
// errModelNotResolved。
func (r *ModelRegistry) resolveRecommended(
	ctx context.Context,
	cacheKey string,
	capability domain.ModelCapability,
) (ProviderConfig, domain.Provider, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, port.ModelFilter{Enabled: &enabled, Capability: capability})
	if err != nil {
		return ProviderConfig{}, domain.Provider{}, fmt.Errorf("model registry: list recommended models: %w", err)
	}
	models = usableModels(models, r.isModelUsable)
	var best domain.Model
	var bestProvider *domain.Provider
	found := false
	for _, m := range models {
		if !m.Recommended {
			continue
		}
		provider, err := r.providerRepo.Get(ctx, m.ProviderID)
		if err != nil {
			return ProviderConfig{}, domain.Provider{}, fmt.Errorf("model registry: get provider: %w", err)
		}
		if !provider.Enabled || !r.supports(provider.Kind, capability) {
			continue
		}
		if !found || m.Name < best.Name {
			best, bestProvider = m, provider
			found = true
		}
	}
	if !found {
		return ProviderConfig{}, domain.Provider{}, errModelNotResolved
	}
	cfg := ProviderConfig{
		Name:        bestProvider.Name,
		BaseURL:     bestProvider.BaseURL,
		APIKey:      bestProvider.APIKey,
		HealthModel: bestProvider.DefaultModel,
		Models:      []string{best.Name},
	}
	r.cacheSet(cacheKey, cfg, *bestProvider, best.Capabilities, policyWithProvider(&best, bestProvider))
	return cfg, *bestProvider, nil
}

// resolveEmbeddingMarked 是 ④ 级（embedding 专用）：default_embedding 标记的
// 模型优先，无标记则取 enabled 列表第一个（保留 sort.Strings 字典序语义），
// 均要求 provider 可用。全空返回 errModelNotResolved。
func (r *ModelRegistry) resolveEmbeddingMarked(ctx context.Context, cacheKey string) (ProviderConfig, domain.Provider, error) {
	c, err := r.pickEmbeddingCandidate(ctx)
	if err != nil {
		return ProviderConfig{}, domain.Provider{}, err
	}
	if c == nil {
		return ProviderConfig{}, domain.Provider{}, errModelNotResolved
	}
	cfg := ProviderConfig{
		Name:        c.p.Name,
		BaseURL:     c.p.BaseURL,
		APIKey:      c.p.APIKey,
		HealthModel: c.p.DefaultModel,
		Models:      []string{c.m.Name},
	}
	r.cacheSet(cacheKey, cfg, *c.p, c.m.Capabilities, policyWithProvider(&c.m, c.p))
	return cfg, *c.p, nil
}

// embeddingCandidate 是 ④ 级选中的候选：marked 优先，否则 name 最小。
type embeddingCandidate struct {
	m domain.Model
	p *domain.Provider
}

// pickEmbeddingCandidate 在 enabled 且 provider 可用的 embedding 模型中，
// 优先 default_embedding 标记，无标记取 name 最小者（字典序语义）。全空
// 返回 (nil, nil)，由调用方按 errModelNotResolved 处理。
func (r *ModelRegistry) pickEmbeddingCandidate(ctx context.Context) (*embeddingCandidate, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, port.ModelFilter{Enabled: &enabled, Capability: domain.CapEmbedding})
	if err != nil {
		return nil, fmt.Errorf("model registry: list embedding models: %w", err)
	}
	models = usableModels(models, r.isModelUsable)
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	var marked, first *embeddingCandidate
	for _, m := range models {
		provider, err := r.providerRepo.Get(ctx, m.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("model registry: get provider: %w", err)
		}
		if !provider.Enabled || !r.supports(provider.Kind, domain.CapEmbedding) {
			continue
		}
		cand := &embeddingCandidate{m: m, p: provider}
		if first == nil {
			first = cand
		}
		if m.DefaultEmbedding && marked == nil {
			marked = cand
		}
	}
	if marked != nil {
		return marked, nil
	}
	return first, nil
}

// FallbackCandidate 是已解析的 fallback 候选：模型名 + 可直接调用的
// provider 配置与 chat 协议。
type FallbackCandidate struct {
	Model    string
	Config   ProviderConfig
	Protocol ChatProtocol
}

// ResolveFallbackCandidates 有序列举主模型之外的可用 chat 模型（总链长上限
// constants.MaxModelFallbackCandidates），供 Gateway 在瞬态失败时降级。
// 显式候选优先：主模型显式配置的 FallbackCandidates 按配置顺序优先入链，
// 单项失效（退役/禁用/不健康/协议不支持）容错跳过；不足上限时由隐式规则
// 兜底补齐（排除显式配置名，按同 provider 优先 → Recommended desc →
// name asc 排序）。primary 必须可解析，否则调用方无法发起主调用，直接返回
// 解析错误。
func (r *ModelRegistry) ResolveFallbackCandidates(ctx context.Context, primary string) ([]FallbackCandidate, error) {
	primaryCfg, _, err := r.Resolve(ctx, primary)
	if err != nil {
		return nil, err
	}
	// 排除「实际解析出的模型」而非入参 primary：显式指定模型不可用时可能
	// 已降级到健康默认，避免同一模型既作 primary 又入候选。
	resolvedPrimary := primary
	if len(primaryCfg.Models) > 0 {
		resolvedPrimary = primaryCfg.Models[0]
	}
	cands, primaryModel, err := r.listFallbackCandidates(ctx, resolvedPrimary, primaryCfg.Name)
	if err != nil {
		return nil, err
	}
	var explicitNames []string
	if primaryModel != nil {
		explicitNames = primaryModel.FallbackCandidates
	}
	explicit := r.resolveExplicitFallbackCandidates(primaryModel, cands)
	implicit := r.listImplicitFallbackCandidates(cands, explicitNames, len(explicit))
	// appendAssign: explicit 可能与调用方共享底层数组,先拷贝再拼接,避免后续
	// chain[:N] 截断时意外修改 explicit 的底层数组。
	chain := make([]FallbackCandidate, 0, len(explicit)+len(implicit))
	chain = append(chain, explicit...)
	chain = append(chain, implicit...)
	if len(chain) > constants.MaxModelFallbackCandidates {
		chain = chain[:constants.MaxModelFallbackCandidates]
	}
	return chain, nil
}

// fallbackCand 是候选模型及其 provider（samePrimary 标记与主模型同 provider）。
type fallbackCand struct {
	model       domain.Model
	provider    domain.Provider
	samePrimary bool
}

// listFallbackCandidates 列举主模型之外的 enabled chat 模型，跳过 disabled
// provider 与不支持 chat 协议的模型；同时返回主模型完整对象（含显式候选配置，
// 主模型不在 enabled chat 目录时为 nil，此时显式候选视为未配置）。
func (r *ModelRegistry) listFallbackCandidates(ctx context.Context, primary, primaryProviderName string) ([]fallbackCand, *domain.Model, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, port.ModelFilter{Enabled: &enabled, Capability: domain.CapChat})
	if err != nil {
		return nil, nil, fmt.Errorf("model registry: list models: %w", err)
	}
	// 主模型查找在健康过滤前：主模型可能瞬时不可用（unhealthy），但其显式
	// 候选配置仍需读取，健康过滤只约束候选集合。
	var primaryModel *domain.Model
	for i := range models {
		if models[i].Name == primary {
			m := models[i]
			primaryModel = &m
			break
		}
	}
	usable := usableModels(models, r.isModelUsable)
	cands := make([]fallbackCand, 0, len(usable))
	for _, m := range usable {
		if m.Name == primary {
			continue
		}
		provider, err := r.providerRepo.Get(ctx, m.ProviderID)
		if err != nil {
			return nil, nil, fmt.Errorf("model registry: get provider: %w", err)
		}
		if !provider.Enabled || !r.supports(provider.Kind, domain.CapChat) {
			continue
		}
		cands = append(cands, fallbackCand{model: m, provider: *provider, samePrimary: provider.Name == primaryProviderName})
	}
	return cands, primaryModel, nil
}

// resolveExplicitFallbackCandidates 解析主模型显式配置的降级候选（有序，保持
// 配置顺序）。逐项容错：候选不在当前可用 chat 目录（退役/禁用/不健康）、
// provider 不可用或不支持 chat 协议时跳过该项，绝不因单项失效而中止整条链。
// 返回实际构造成功的候选；配置名全集由调用方传给隐式兜底做排除，避免同一
// 模型被显式与隐式重复引入。
func (r *ModelRegistry) resolveExplicitFallbackCandidates(primaryModel *domain.Model, cands []fallbackCand) []FallbackCandidate {
	if primaryModel == nil || len(primaryModel.FallbackCandidates) == 0 {
		return nil
	}
	byName := make(map[string]fallbackCand, len(cands))
	for _, c := range cands {
		byName[c.model.Name] = c
	}
	result := make([]FallbackCandidate, 0, len(primaryModel.FallbackCandidates))
	for _, name := range primaryModel.FallbackCandidates {
		c, ok := byName[name]
		if !ok {
			continue
		}
		if cand, ok := r.buildFallbackCandidate(c); ok {
			result = append(result, cand)
		}
	}
	return result
}

// listImplicitFallbackCandidates 从可用候选池排除「显式配置的全部候选名」后，
// 按同 provider 优先 → Recommended desc → name asc 排序，补齐到上限。已由
// 显式候选占用的位置不补隐式；显式候选不足上限时隐式填充，保证链有降级
// 兜底（显式配置项失效时不至于链空）。
func (r *ModelRegistry) listImplicitFallbackCandidates(cands []fallbackCand, explicitNames []string, explicitUsed int) []FallbackCandidate {
	if explicitUsed >= constants.MaxModelFallbackCandidates {
		return nil
	}
	excluded := make(map[string]struct{}, len(explicitNames))
	for _, n := range explicitNames {
		excluded[n] = struct{}{}
	}
	pool := make([]fallbackCand, 0, len(cands))
	for _, c := range cands {
		if _, skip := excluded[c.model.Name]; skip {
			continue
		}
		pool = append(pool, c)
	}
	sort.SliceStable(pool, func(i, j int) bool { return candidateLess(pool[i], pool[j]) })
	remaining := constants.MaxModelFallbackCandidates - explicitUsed
	if len(pool) > remaining {
		pool = pool[:remaining]
	}
	result := make([]FallbackCandidate, 0, len(pool))
	for _, c := range pool {
		if cand, ok := r.buildFallbackCandidate(c); ok {
			result = append(result, cand)
		}
	}
	return result
}

// buildFallbackCandidate 为单个候选构造可调用配置并写回 TTL 缓存；provider
// 不支持 chat 协议时返回 ok=false（调用方跳过该项）。
func (r *ModelRegistry) buildFallbackCandidate(c fallbackCand) (FallbackCandidate, bool) {
	cfg := ProviderConfig{
		Name:        c.provider.Name,
		BaseURL:     c.provider.BaseURL,
		APIKey:      c.provider.APIKey,
		HealthModel: c.provider.DefaultModel,
		Models:      []string{c.model.Name},
	}
	// 复用 TTL 缓存语义：Warm/Resolve 已缓存的 entry 保持有效，这里与缓存
	// 数据同源（同 modelRepo/providerRepo），直接写回。
	r.cacheSet("chat:"+c.model.Name, cfg, c.provider, c.model.Capabilities, policyWithProvider(&c.model, &c.provider))
	proto, ok := r.chatProtos[c.provider.Kind]
	if !ok {
		return FallbackCandidate{}, false
	}
	return FallbackCandidate{Model: c.model.Name, Config: cfg, Protocol: proto}, true
}

// candidateLess 是 fallback 候选的排序比较：同 provider 优先 → Recommended
// desc → name asc。
func candidateLess(a, b fallbackCand) bool {
	if a.samePrimary != b.samePrimary {
		return a.samePrimary
	}
	if a.model.Recommended != b.model.Recommended {
		return a.model.Recommended
	}
	return a.model.Name < b.model.Name
}

// ListChatModelsByTenant 返回全局 enabled chat 模型名（排序）。方法名保留
// 历史命名，但目录已是全局，tenantID 参数已去除。
func (r *ModelRegistry) ListChatModelsByTenant(ctx context.Context) ([]string, error) {
	return r.listModelsByCapability(ctx, domain.CapChat)
}

// ListEmbeddingModelsByTenant 返回全局 enabled embedding 模型名（排序）。
func (r *ModelRegistry) ListEmbeddingModelsByTenant(ctx context.Context) ([]string, error) {
	return r.listModelsByCapability(ctx, domain.CapEmbedding)
}

// ListRerankModelsByTenant 返回全局 enabled rerank 模型名（排序）。目录化只
// 约束模型选择：实际调用仍走 knowledge/infrastructure/rerank 独立 HTTP 服务。
func (r *ModelRegistry) ListRerankModelsByTenant(ctx context.Context) ([]string, error) {
	return r.listModelsByCapability(ctx, domain.CapRerank)
}

// ResolveDefaultEmbeddingModel 解析全局默认嵌入模型名：
// 1. enabled 且 provider 可用且标记 default_embedding 的模型优先；
// 2. 无标记 → enabled 列表第一个（保留 sort.Strings 字典序语义）；
// 3. 列表为空 → 返回 ""，调用方 fail-closed（不默认放行）。
func (r *ModelRegistry) ResolveDefaultEmbeddingModel(ctx context.Context) (string, error) {
	cfg, _, err := r.resolveEmbeddingMarked(ctx, "embed:")
	if err != nil {
		if errors.Is(err, errModelNotResolved) {
			return "", nil
		}
		return "", err
	}
	return cfg.Models[0], nil
}

// ListModelsByTenantDetails 返回全局模型目录（包括 disabled 和
// provider-managed 模型）按 name 排序。模型 provider 缺失时 fail closed
// （未解析的 provider 不得作为健康模型浮出）。返回行由组合根投影为
// platform-assistant DTO；provider 凭据不出此边界。
func (r *ModelRegistry) ListModelsByTenantDetails(ctx context.Context) ([]domain.Model, error) {
	models, err := r.modelRepo.List(ctx, port.ModelFilter{})
	if err != nil {
		return nil, fmt.Errorf("model registry: list models: %w", err)
	}
	details := make([]domain.Model, 0, len(models))
	for _, m := range models {
		provider, err := r.providerRepo.Get(ctx, m.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("model registry: get provider: %w", err)
		}
		if !provider.Enabled {
			continue
		}
		if r.health != nil {
			m.Health = r.health.ModelHealth(m.Name)
		}
		details = append(details, m)
	}
	sort.Slice(details, func(i, j int) bool { return details[i].Name < details[j].Name })
	return details, nil
}

func (r *ModelRegistry) listModelsByCapability(
	ctx context.Context,
	capability domain.ModelCapability,
) ([]string, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, port.ModelFilter{
		Enabled:    &enabled,
		Capability: capability,
	})
	if err != nil {
		return nil, fmt.Errorf("model registry: list models: %w", err)
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		provider, err := r.providerRepo.Get(ctx, m.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("model registry: get provider: %w", err)
		}
		if !provider.Enabled || !r.supports(provider.Kind, capability) {
			continue
		}
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names, nil
}

// usableModels 过滤出健康可用的模型（usable 为 nil 即未注入健康 registry 时
// 原样返回）。解析链各循环顶部先剔除不可用模型，避免循环内叠加健康分支抬高
// 圈复杂度，也让「先剔除不可用，再选候选」的选择语义更清晰。
func usableModels(models []domain.Model, usable func(string) bool) []domain.Model {
	if usable == nil {
		return models
	}
	out := make([]domain.Model, 0, len(models))
	for _, m := range models {
		if usable(m.Name) {
			out = append(out, m)
		}
	}
	return out
}

func (r *ModelRegistry) supports(kind domain.ProviderKind, capability domain.ModelCapability) bool {
	switch capability {
	case domain.CapChat:
		_, ok := r.chatProtos[kind]
		return ok
	case domain.CapEmbedding:
		_, ok := r.embedProtos[kind]
		return ok
	case domain.CapRerank:
		// cohere rerank 走独立 HTTP 服务（非 ModelRegistry 网关），此处仅约束
		// 模型选择来源：rerank 能力模型只属于 cohere provider。
		return kind == domain.ProviderCohere
	default:
		return false
	}
}

// Warm pre-warms the cache by listing enabled models across the global
// catalog and populating cache entries so that subsequent Resolve and
// ResolveEmbedding calls hit the cache. 启动时预热一次（全局目录，无租户维度）。
func (r *ModelRegistry) Warm(ctx context.Context) error {
	enabled := true
	models, err := r.modelRepo.List(ctx, port.ModelFilter{Enabled: &enabled})
	if err != nil {
		return fmt.Errorf("model registry: warm: %w", err)
	}
	for _, m := range models {
		if err := r.warmModel(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// warmModel 为单个 enabled 模型写入 chat/embed 缓存条目；provider 不可用
// 或能力不支持时跳过。
func (r *ModelRegistry) warmModel(ctx context.Context, m domain.Model) error {
	provider, err := r.providerRepo.Get(ctx, m.ProviderID)
	if err != nil {
		return fmt.Errorf("model registry: warm: get provider: %w", err)
	}
	if !provider.Enabled {
		return nil
	}
	cfg := ProviderConfig{
		Name:        provider.Name,
		BaseURL:     provider.BaseURL,
		APIKey:      provider.APIKey,
		HealthModel: provider.DefaultModel,
		Models:      []string{m.Name},
	}
	for _, cap := range m.Capabilities {
		if !r.supports(provider.Kind, cap) {
			continue
		}
		switch cap {
		case domain.CapChat:
			r.cacheSet("chat:"+m.Name, cfg, *provider, m.Capabilities, policyWithProvider(&m, provider))
		case domain.CapEmbedding:
			r.cacheSet("embed:"+m.Name, cfg, *provider, m.Capabilities, policyWithProvider(&m, provider))
		}
	}
	return nil
}

// GetChatModelContextWindow returns the ContextWindow for a named chat model.
// Returns 0 when the model is not found or has no known context window.
func (r *ModelRegistry) GetChatModelContextWindow(ctx context.Context, modelName string) (int, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, port.ModelFilter{Enabled: &enabled, Capability: domain.CapChat})
	if err != nil {
		return 0, fmt.Errorf("model registry: get context window: %w", err)
	}
	for _, m := range models {
		if m.Name == modelName {
			return m.ContextWindow, nil
		}
	}
	return 0, nil
}

// ListChatModels returns an empty slice. 全局模型列表经
// ListChatModelsByTenant(ctx) 获取。该方法满足 port.ModelCatalog 的
// 无参变体（历史兼容）。
func (r *ModelRegistry) ListChatModels() []string {
	return []string{}
}

// ListEmbeddingModels returns an empty slice. 全局模型列表经
// ListEmbeddingModelsByTenant(ctx) 获取。
func (r *ModelRegistry) ListEmbeddingModels() []string {
	return []string{}
}

// Invalidate clears the entire global cache.
func (r *ModelRegistry) Invalidate() {
	r.mu.Lock()
	r.cache = make(map[string]*resolvedEntry)
	r.mu.Unlock()
}

// cacheGet returns a non-expired cached entry, or nil.
func (r *ModelRegistry) cacheGet(key string) *resolvedEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.cache[key]
	if !ok || time.Now().After(e.expires) {
		return nil
	}
	return e
}

// cacheSet stores an entry in the cache. policy 是该模型的权威数据快照
// （cache 预计算）；无模型记录（②③④ 级解析）传 nil。
func (r *ModelRegistry) cacheSet(key string, cfg ProviderConfig, provider domain.Provider, capabilities []domain.ModelCapability, policy *ModelPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = &resolvedEntry{
		config:       cfg,
		provider:     provider,
		capabilities: capabilities,
		policy:       policy,
		expires:      time.Now().Add(r.cacheTTL),
	}
}

// PolicyFor 返回模型权威数据快照（缓存命中）；miss 返回 nil（调用方按
// 权威数据不存在处理，不触发 DB 查询）。
func (r *ModelRegistry) PolicyFor(ctx context.Context, model string) *ModelPolicy {
	if e := r.cacheGet("chat:" + model); e != nil {
		return e.policy
	}
	return nil
}

// ResolveReasoning 判断模型是否为推理模型。能力来源并集：DB models.capabilities
// 含 CapReasoning → true；否则 catalog 已知推理模型（ModelSupportsReasoning）
// → true；否则 false。unknown 返回 false，fail-closed：网关据此清空
// reasoning_effort，禁止对非推理/未知模型盲透传（严格端点 400 是永久错误，
// 会中止整条 fallback 链）。
//
// 缓存预计算优先：cache entry 携带 policy（Warm/Resolve 已预热）时直接
// 读取，不再查 DB（吸收解析路径的 N+1）；cache miss 或权威数据缺失
// （policy nil）才回退旧 DB+catalog 解析。
func (r *ModelRegistry) ResolveReasoning(ctx context.Context, modelName string) bool {
	if e := r.cacheGet("chat:" + modelName); e != nil && e.policy != nil {
		return e.policy.Reasoning
	}
	enabled := true
	models, err := r.modelRepo.List(ctx, port.ModelFilter{Enabled: &enabled})
	if err != nil {
		return false
	}
	for _, m := range models {
		if m.Name != modelName {
			continue
		}
		for _, cap := range m.Capabilities {
			if cap == domain.CapReasoning {
				return true
			}
		}
		// DB 无 CapReasoning 标记：回退 catalog 已知推理模型（并集兜底）。
		return ModelSupportsReasoning(modelName)
	}
	return false
}

// ResolveStructuredOutput 判断模型是否支持 response_format=json_object。
// JSON mode 是族级 provider 能力（qwen/glm/deepseek/gpt），不由 DB
// capabilities 枚举：统一走 catalog 前缀匹配。unknown 返回 false，
// fail-closed：网关据此清空 response_format（严格端点 400 会中止 fallback 链）。
func (r *ModelRegistry) ResolveStructuredOutput(ctx context.Context, modelName string) bool {
	return ModelSupportsStructuredOutput(modelName)
}
