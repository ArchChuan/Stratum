package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// stubParametersProvider is a minimal ParametersProvider double: resolution
// is fully scripted per test.
type stubParametersProvider struct {
	effective map[string]any
	err       error
	// singleKey is returned by Resolve for the requested key; missing from
	// the map → (nil, false, nil). Platform toggles scripted here.
	singleKey map[string]any
}

func (p stubParametersProvider) ResolveForResource(
	_ context.Context, _ map[string]any,
) (map[string]any, error) {
	return p.effective, p.err
}

func (p stubParametersProvider) Resolve(_ context.Context, key string, _ map[string]any) (any, bool, error) {
	if p.err != nil {
		return nil, false, p.err
	}
	v, ok := p.singleKey[key]
	return v, ok, nil
}

func (stubParametersProvider) ValidateResource(_ context.Context, _ map[string]any) error {
	return nil
}

func (stubParametersProvider) ValidateResourceKey(_ context.Context, _ string, _ any) error {
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
			"agent.max_tokens_per_execution": int64(50000),
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
	if cfg.MaxTokensPerExecution != 50000 {
		t.Errorf("MaxTokensPerExecution = %d, want 50000 (platform default, resource unset)", cfg.MaxTokensPerExecution)
	}
}

func TestResolveEffectiveParametersResourceValueWinsOverPlatformDefault(t *testing.T) {
	// 单归属:资源显式声明优先于平台默认(resolver 已实现该回退,这里断言
	// resolveEffectiveParameters 把 declared 层的值透传到 option)。
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{effective: map[string]any{
			"agent.temperature":              1.2,
			"agent.max_tokens":               int64(4096),
			"agent.max_tokens_per_execution": int64(60000),
		}},
		Logger: zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{Temperature: 1.2, MaxTokens: 4096, MaxTokensPerExecution: 60000}}

	cfg := applyOptions(svc.resolveEffectiveParameters(context.Background(), agent, nil))

	if cfg.Temperature != 1.2 {
		t.Errorf("Temperature = %v, want 1.2 (resource-declared)", cfg.Temperature)
	}
	if cfg.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", cfg.MaxTokens)
	}
	if cfg.MaxTokensPerExecution != 60000 {
		t.Errorf("MaxTokensPerExecution = %d, want 60000 (resource-declared)", cfg.MaxTokensPerExecution)
	}
}

func TestResolveEffectiveParametersAllUnsetKeepsDefaults(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{effective: map[string]any{}},
		Logger:             zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{}}

	cfg := applyOptions(svc.resolveEffectiveParameters(context.Background(), agent, nil))

	if cfg.Temperature != 0 || cfg.MaxTokens != 0 {
		t.Errorf("expected all unset, got %+v", cfg)
	}
}

func TestResolveEffectiveParametersWarnsWhenResourceParamsUnset(t *testing.T) {
	// 资源参数未配置不再有平台默认兜底:必须打 WARN(带 agent_id 与 unset_keys),
	// 执行仍按各参数文档规则落到网关/provider/常量默认。
	core, logs := observer.New(zapcore.WarnLevel)
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{effective: map[string]any{}},
		Logger:             zap.New(core),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{ID: "agent-1"}}

	svc.resolveEffectiveParameters(context.Background(), agent, nil)

	var entry *observer.LoggedEntry
	for i := range logs.All() {
		cur := logs.All()[i]
		if strings.Contains(cur.Message, "resource parameters unset") {
			entry = &cur
			break
		}
	}
	if entry == nil {
		t.Fatal("expected WARN log when resource parameters are unset")
	}
	if got := entry.ContextMap()["agent_id"]; got != "agent-1" {
		t.Errorf("agent_id = %v, want agent-1", got)
	}
	raw, ok := entry.ContextMap()["unset_keys"]
	if !ok {
		t.Fatal("unset_keys field missing")
	}
	var keys []string
	switch v := raw.(type) {
	case []string:
		keys = v
	case []any:
		for _, k := range v {
			if s, ok := k.(string); ok {
				keys = append(keys, s)
			}
		}
	}
	if len(keys) == 0 {
		t.Fatal("unset_keys must list the unconfigured resource keys")
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
	err := svc.validateSamplingParams(context.Background(), 3.5, 0, 0, "", 0)
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
func (failingValidateProvider) ValidateResourceKey(_ context.Context, _ string, _ any) error {
	return errors.New("agent.temperature: must be <= 2, got 3.5")
}

func TestValidateSamplingParamsSkipsUnsetValues(t *testing.T) {
	// 0=unset 不产生 declared key,provider 不被调用,校验直接通过。
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: failingValidateProvider{},
		Logger:             zap.NewNop(),
	})
	if err := svc.validateSamplingParams(context.Background(), 0, 0, 0, "", 0); err != nil {
		t.Errorf("all-unset must pass validation, got %v", err)
	}
}

func TestValidateSamplingParamsNilProviderIsNoop(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{Logger: zap.NewNop()})
	if err := svc.validateSamplingParams(context.Background(), 0.9, 2048, 3, "high", 0); err != nil {
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

// TestResolveEffectiveParametersPlatformToggleCaptureParameters verifies the
// platform-scope trace.capture_parameters toggle reaches the execution
// config through the WithCaptureParameters option (Phase 2: value-gated
// parameter attributes on the execution span).
func TestResolveEffectiveParametersPlatformToggleCaptureParameters(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{singleKey: map[string]any{"trace.capture_parameters": true}},
		Logger:             zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{}}
	options := svc.resolveEffectiveParameters(context.Background(), agent, nil)
	if got := applyOptions(options).CaptureParameters; !got {
		t.Fatalf("expected CaptureParameters=true when platform toggle resolves true, got %v", got)
	}
}

func TestResolveEffectiveParametersPlatformToggleUnsetStaysFalse(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{singleKey: map[string]any{}},
		Logger:             zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{}}
	options := svc.resolveEffectiveParameters(context.Background(), agent, nil)
	if got := applyOptions(options).CaptureParameters; got {
		t.Fatalf("expected CaptureParameters=false when toggle is unset, got %v", got)
	}
}

func TestResolveEffectiveParametersPlatformToggleErrorDegradesToUnset(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{err: errors.New("registry down")},
		Logger:             zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{}}
	options := svc.resolveEffectiveParameters(context.Background(), agent, nil)
	if got := applyOptions(options).CaptureParameters; got {
		t.Fatalf("expected CaptureParameters=false on resolution error, got %v", got)
	}
}

func TestApplyParameterOverridesMapWinsAndOnlyPresentKeysOverwrite(t *testing.T) {
	// Parameters map keys take precedence over the top-level sampling fields;
	// keys absent from the map keep the top-level value (merge semantics).
	in := UpdateAgentInput{
		Temperature:            0.9,
		MaxTokens:              2048,
		CompactionRecentGroups: 3,
		ReasoningEffort:        "medium",
		Parameters: map[string]any{
			"temperature":      0.3,
			"max_tokens":       float64(4096),
			"reasoning_effort": "high",
		},
	}
	temperature, maxTokens, recentGroups, reasoningEffort, _, _ := applyParameterOverrides(in)

	if temperature != 0.3 {
		t.Errorf("temperature = %v, want 0.3 (map wins)", temperature)
	}
	if maxTokens != 4096 {
		t.Errorf("max_tokens = %d, want 4096 (map wins)", maxTokens)
	}
	if recentGroups != 3 {
		t.Errorf("compaction_recent_groups = %d, want 3 (absent key keeps top-level)", recentGroups)
	}
	if reasoningEffort != "high" {
		t.Errorf("reasoning_effort = %q, want \"high\" (map wins)", reasoningEffort)
	}
}

func TestApplyParameterOverridesEmptyMapIsNoop(t *testing.T) {
	// 旧客户端 PUT(无 parameters 对象)不得改变采样字段:merge 不清除已存值。
	in := UpdateAgentInput{
		Temperature:            0.9,
		MaxTokens:              2048,
		CompactionRecentGroups: 3,
		ReasoningEffort:        "low",
	}
	temperature, maxTokens, recentGroups, reasoningEffort, _, _ := applyParameterOverrides(in)

	if temperature != 0.9 || maxTokens != 2048 || recentGroups != 3 {
		t.Errorf("empty map must be a no-op, got %v/%d/%d",
			temperature, maxTokens, recentGroups)
	}
	if reasoningEffort != "low" {
		t.Errorf("reasoning_effort = %q, want \"low\" (absent key keeps top-level)", reasoningEffort)
	}
}

func TestApplyParameterOverridesExplicitZeroIsUnset(t *testing.T) {
	// 0=unset:map 显式 0 覆盖顶层字段后由 pack 跳过(omitempty),JSONB 拼接
	// 不清除既有值——与「merge 不清除已存参数」及 pack 注释语义一致。
	in := UpdateAgentInput{
		Temperature: 0.9,
		Parameters:  map[string]any{"temperature": 0},
	}
	temperature, _, _, _, _, _ := applyParameterOverrides(in)

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
			"compaction_recent_groups": float64(5),
		},
	})
	if err != nil {
		t.Fatalf("buildUpdateConfig: %v", err)
	}
	if cfg.Temperature != 0.3 || cfg.MaxTokens != 4096 {
		t.Errorf("sampling fields not merged: temp=%v maxTokens=%d", cfg.Temperature, cfg.MaxTokens)
	}
	if cfg.CompactionRecentGroups != 5 {
		t.Errorf("compaction fields not merged: groups=%d",
			cfg.CompactionRecentGroups)
	}
}

func TestResolveEffectiveParametersCooldownKey(t *testing.T) {
	// agent.compaction_cooldown_sec 是资源层声明的可调参数：资源未设（0）时
	// 平台默认值流入执行配置。
	svc := NewAgentService(AgentServiceDeps{
		ParametersProvider: stubParametersProvider{effective: map[string]any{
			"agent.compaction_cooldown_sec": int64(15),
		}},
		Logger: zap.NewNop(),
	})
	agent := &testParamAgent{cfg: &domain.AgentConfig{}}

	cfg := applyOptions(svc.resolveEffectiveParameters(context.Background(), agent, nil))

	if cfg.CompactionCooldownSec != 15 {
		t.Errorf("CompactionCooldownSec = %d, want 15 (platform default, resource unset)", cfg.CompactionCooldownSec)
	}
}
