package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// stubParametersProvider is a minimal ParametersProvider double: resolution
// is fully scripted per test.
type stubParametersProvider struct {
	effective map[string]any
	err       error
}

func (p stubParametersProvider) ResolveForResource(
	_ context.Context, _ map[string]any,
) (map[string]any, error) {
	return p.effective, p.err
}

func (stubParametersProvider) ValidateResource(_ context.Context, _ map[string]any) error {
	return nil
}

// testParamAgent is the smallest Agent that carries a config; execution
// internals are irrelevant to the resolve step.
type testParamAgent struct{ cfg *domain.AgentConfig }

func (a *testParamAgent) GetConfig() *domain.AgentConfig { return a.cfg }
func (a *testParamAgent) Execute(context.Context, string, ...ExecutionOption) (*AgentResult, error) {
	return nil, nil
}
func (a *testParamAgent) Reset()               {}
func (a *testParamAgent) GetMemory() []Message { return nil }

// applyOptions folds the options into a fresh ExecutionConfig so tests can
// assert resolved values instead of peeking at closures.
func applyOptions(options []ExecutionOption) ExecutionConfig {
	cfg := ExecutionConfig{}
	for _, opt := range options {
		opt(&cfg)
	}
	return cfg
}

func TestResolveEffectiveParametersMergesPlatformDefaultsWhenResourceUnset(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{effective: map[string]any{
			"agent.temperature":              0.5,
			"agent.max_tokens":               int64(2048),
			"agent.compaction_recent_groups": int64(3),
			"agent.compaction_safety_ratio":  0.6,
		}},
		Logger: zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{}}

	cfg := applyOptions(svc.resolveEffectiveParameters(context.Background(), agent, nil))

	if cfg.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5 (platform default, resource unset)", cfg.Temperature)
	}
	if cfg.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %d, want 2048", cfg.MaxTokens)
	}
	if cfg.CompactionRecentGroups != 3 {
		t.Errorf("CompactionRecentGroups = %d, want 3", cfg.CompactionRecentGroups)
	}
	if cfg.CompactionSafetyRatio != 0.6 {
		t.Errorf("CompactionSafetyRatio = %v, want 0.6", cfg.CompactionSafetyRatio)
	}
}

func TestResolveEffectiveParametersResourceValueWinsOverPlatformDefault(t *testing.T) {
	// 单归属:资源显式声明优先于平台默认(resolver 已实现该回退,这里断言
	// resolveEffectiveParameters 把 declared 层的值透传到 option)。
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{effective: map[string]any{
			"agent.temperature": 1.2,
			"agent.max_tokens":  int64(4096),
		}},
		Logger: zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{Temperature: 1.2, MaxTokens: 4096}}

	cfg := applyOptions(svc.resolveEffectiveParameters(context.Background(), agent, nil))

	if cfg.Temperature != 1.2 {
		t.Errorf("Temperature = %v, want 1.2 (resource-declared)", cfg.Temperature)
	}
	if cfg.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", cfg.MaxTokens)
	}
}

func TestResolveEffectiveParametersAllUnsetKeepsDefaults(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{effective: map[string]any{}},
		Logger:             zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{}}

	cfg := applyOptions(svc.resolveEffectiveParameters(context.Background(), agent, nil))

	if cfg.Temperature != 0 || cfg.MaxTokens != 0 || cfg.CompactionSafetyRatio != 0 {
		t.Errorf("expected all unset, got %+v", cfg)
	}
}

func TestResolveEffectiveParametersProviderErrorDegradesToDefaults(t *testing.T) {
	// 参数是优化输入不是执行门禁:解析失败保留网关默认,不阻断执行。
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{err: errors.New("db down")},
		Logger:             zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{Temperature: 0.9}}

	cfg := applyOptions(svc.resolveEffectiveParameters(context.Background(), agent, nil))

	if cfg.Temperature != 0 {
		t.Errorf("Temperature = %v, want 0 (provider error degrades to unset)", cfg.Temperature)
	}
}

func TestValidateSamplingParamsRejectsOutOfBoundsWithSentinel(t *testing.T) {
	// 越界/未知采样值必须返回 ErrInvalidSamplingParameters sentinel,错误
	// 中间件据此映射 400(此前越界值 200 落库,真实 bug)。
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: failingValidateProvider{},
		Logger:             zap.NewNop(),
	})
	err := svc.validateSamplingParams(context.Background(), 3.5, 0, 0, 0)
	if err == nil {
		t.Fatal("expected error for out-of-bounds temperature")
	}
	if !errors.Is(err, domain.ErrInvalidSamplingParameters) {
		t.Errorf("err = %v, want sentinel ErrInvalidSamplingParameters", err)
	}
	if !strings.Contains(err.Error(), "must be <= 2") {
		t.Errorf("err must retain bound detail, got %v", err)
	}
}

// failingValidateProvider fails any validation call; the out-of-bounds test
// asserts the sentinel wrap keeps the detail, and the all-unset test proves no
// declared keys reach the provider.
type failingValidateProvider struct{ stubParametersProvider }

func (failingValidateProvider) ValidateResource(_ context.Context, _ map[string]any) error {
	return errors.New("agent.temperature: must be <= 2, got 3.5")
}

func TestValidateSamplingParamsSkipsUnsetValues(t *testing.T) {
	// 0=unset 不产生 declared key,provider 不被调用,校验直接通过。
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: failingValidateProvider{},
		Logger:             zap.NewNop(),
	})
	if err := svc.validateSamplingParams(context.Background(), 0, 0, 0, 0); err != nil {
		t.Errorf("all-unset must pass validation, got %v", err)
	}
}

func TestValidateSamplingParamsNilProviderIsNoop(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{Logger: zap.NewNop()})
	if err := svc.validateSamplingParams(context.Background(), 0.9, 2048, 3, 0.5); err != nil {
		t.Errorf("nil provider must degrade to no-op, got %v", err)
	}
}

func TestResolveEffectiveParametersNilProviderIsNoop(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{Logger: zap.NewNop()})
	agent := &testParamAgent{cfg: &domain.AgentConfig{}}

	cfg := applyOptions(svc.resolveEffectiveParameters(context.Background(), agent, nil))

	if cfg.Temperature != 0 || cfg.MaxTokens != 0 {
		t.Errorf("expected no options appended, got %+v", cfg)
	}
}

var _ port.ParametersProvider = stubParametersProvider{}

func TestApplyParameterOverridesMapWinsAndOnlyPresentKeysOverwrite(t *testing.T) {
	// Parameters map keys take precedence over the top-level sampling fields;
	// keys absent from the map keep the top-level value (merge semantics).
	in := UpdateAgentInput{
		Temperature:            0.9,
		MaxTokens:              2048,
		CompactionRecentGroups: 3,
		CompactionSafetyRatio:  0.5,
		Parameters: map[string]any{
			"temperature": 0.3,
			"max_tokens":  float64(4096),
		},
	}
	temperature, maxTokens, recentGroups, safetyRatio := applyParameterOverrides(in)

	if temperature != 0.3 {
		t.Errorf("temperature = %v, want 0.3 (map wins)", temperature)
	}
	if maxTokens != 4096 {
		t.Errorf("max_tokens = %d, want 4096 (map wins)", maxTokens)
	}
	if recentGroups != 3 {
		t.Errorf("compaction_recent_groups = %d, want 3 (absent key keeps top-level)", recentGroups)
	}
	if safetyRatio != 0.5 {
		t.Errorf("compaction_safety_ratio = %v, want 0.5 (absent key keeps top-level)", safetyRatio)
	}
}

func TestApplyParameterOverridesEmptyMapIsNoop(t *testing.T) {
	// 旧客户端 PUT(无 parameters 对象)不得改变采样字段:merge 不清除已存值。
	in := UpdateAgentInput{
		Temperature:            0.9,
		MaxTokens:              2048,
		CompactionRecentGroups: 3,
		CompactionSafetyRatio:  0.5,
	}
	temperature, maxTokens, recentGroups, safetyRatio := applyParameterOverrides(in)

	if temperature != 0.9 || maxTokens != 2048 || recentGroups != 3 || safetyRatio != 0.5 {
		t.Errorf("empty map must be a no-op, got %v/%d/%d/%v",
			temperature, maxTokens, recentGroups, safetyRatio)
	}
}

func TestApplyParameterOverridesExplicitZeroIsUnset(t *testing.T) {
	// 0=unset:map 显式 0 覆盖顶层字段后由 pack 跳过(omitempty),JSONB 拼接
	// 不清除既有值——与「merge 不清除已存参数」及 pack 注释语义一致。
	in := UpdateAgentInput{
		Temperature: 0.9,
		Parameters:  map[string]any{"temperature": 0},
	}
	temperature, _, _, _ := applyParameterOverrides(in)

	if temperature != 0 {
		t.Errorf("temperature = %v, want 0 (explicit 0 maps to unset; pack skips it)", temperature)
	}
}

func TestBuildUpdateConfigMergesDeclaredParameters(t *testing.T) {
	// handler PUT parameters 对象 → service 合并进 cfg → repo merge 落库的
	// 中间环节:此前 Parameters map 从未被消费,PUT 后 DB parameters='{}'(真实 bug)。
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{},
		Logger:             zap.NewNop(),
	})
	cfg, err := svc.buildUpdateConfig(context.Background(), "agent-1", UpdateAgentInput{
		Name:             "e2e",
		LLMModel:         "qwen-plus",
		MaxContextTokens: 100,
		Parameters: map[string]any{
			"temperature":              0.3,
			"max_tokens":               float64(4096),
			"compaction_safety_ratio":  0.4,
			"compaction_recent_groups": float64(5),
		},
	})
	if err != nil {
		t.Fatalf("buildUpdateConfig: %v", err)
	}
	if cfg.Temperature != 0.3 || cfg.MaxTokens != 4096 {
		t.Errorf("sampling fields not merged: temp=%v maxTokens=%d", cfg.Temperature, cfg.MaxTokens)
	}
	if cfg.CompactionRecentGroups != 5 || cfg.CompactionSafetyRatio != 0.4 {
		t.Errorf("compaction fields not merged: groups=%d ratio=%v",
			cfg.CompactionRecentGroups, cfg.CompactionSafetyRatio)
	}
}
