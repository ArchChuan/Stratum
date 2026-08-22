package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stubPromptResolver 提供固定平台提示词值，供 Registry 注入测试。
type stubPromptResolver struct {
	value string
}

func (s stubPromptResolver) ResolvePlatform(_ context.Context, _ string) (any, bool, error) {
	if s.value == "" {
		return nil, false, nil
	}
	return s.value, true, nil
}

// TestResolveGlobalSystemSuffix 验证全局系统提示词解析语义：平台参数已配置 →
// 返回后缀；未配置/无 resolver → fail-closed；系统助手与普通 agent 一视同仁；
// nil resolver（测试直构）回退 agent 字段。
func TestResolveGlobalSystemSuffix(t *testing.T) {
	base := NewBaseAgent(&domain.AgentConfig{ID: "agent-1"}, zap.NewNop())

	// 已配置 → 返回后缀。
	base.PlatformPromptResolver = stubPromptResolver{value: "platform rules"}
	suffix, err := base.resolveGlobalSystemSuffix(context.Background())
	if err != nil {
		t.Fatalf("configured resolver: %v", err)
	}
	if suffix != "platform rules" {
		t.Fatalf("suffix = %q, want platform rules", suffix)
	}

	// 未配置（present=false）→ fail-closed。
	base.PlatformPromptResolver = stubPromptResolver{}
	if _, err := base.resolveGlobalSystemSuffix(context.Background()); err == nil {
		t.Fatal("unset platform prompt must fail closed")
	}

	// 系统助手与普通 agent 一视同仁，同样追加全局后缀。
	assistant := NewBaseAgent(&domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey,
	}, zap.NewNop())
	assistant.PlatformPromptResolver = stubPromptResolver{value: "platform rules"}
	if suffix, err := assistant.resolveGlobalSystemSuffix(context.Background()); err != nil || suffix != "platform rules" {
		t.Fatalf("system assistant suffix = %q, err = %v, want platform rules", suffix, err)
	}

	// nil resolver（测试直构路径）回退 agent 字段。
	direct := NewBaseAgent(&domain.AgentConfig{ID: "agent-2"}, zap.NewNop())
	direct.GlobalSystemSuffix = "direct rules"
	if suffix, err := direct.resolveGlobalSystemSuffix(context.Background()); err != nil || suffix != "direct rules" {
		t.Fatalf("direct fallback suffix = %q, err = %v", suffix, err)
	}
}

func TestComposeSystemAssistantProfileOrdinaryAgentPassthrough(t *testing.T) {
	want := &domain.AgentConfig{
		ID: "agent-1", Name: "Tenant Agent", SystemPrompt: "tenant prompt",
		AllowedSkills: []string{"skill-1"}, MCPToolIDs: []string{"mcp-1"},
		KnowledgeWorkspaceIDs: []string{"knowledge-1"},
	}

	got, err := ComposeSystemAssistantProfile(want, BuiltinSystemAssistantProfile())
	if err != nil {
		t.Fatalf("ComposeSystemAssistantProfile() error = %v", err)
	}
	if got == want {
		t.Fatal("ComposeSystemAssistantProfile() returned the input pointer")
	}
	if got.Name != want.Name || got.SystemPrompt != want.SystemPrompt || len(got.AllowedSkills) != 1 {
		t.Fatalf("ordinary agent changed: got %#v, want %#v", got, want)
	}
}

func TestComposeSystemAssistantProfileOrdinaryAgentCopiesSliceFields(t *testing.T) {
	want := &domain.AgentConfig{
		Capabilities:                   []domain.AgentCapability{{Name: "tenant capability"}},
		KnowledgeWorkspaceNames:        []string{"Knowledge"},
		KnowledgeWorkspaceDescriptions: []string{"description"},
	}

	got, err := ComposeSystemAssistantProfile(want, BuiltinSystemAssistantProfile())
	if err != nil {
		t.Fatalf("ComposeSystemAssistantProfile() error = %v", err)
	}
	got.Capabilities[0].Name = "changed"
	got.KnowledgeWorkspaceNames[0] = "changed"
	got.KnowledgeWorkspaceDescriptions[0] = "changed"
	if want.Capabilities[0].Name == "changed" || want.KnowledgeWorkspaceNames[0] == "changed" ||
		want.KnowledgeWorkspaceDescriptions[0] == "changed" {
		t.Fatal("ordinary agent slice fields alias the persisted config")
	}
}

func TestComposeSystemAssistantProfileReplacesProtectedFieldsAndPreservesTenantRuntimeSelection(t *testing.T) {
	profile := BuiltinSystemAssistantProfile()
	want := &domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey,
		Name: "tenant name", Description: "tenant description", SystemPrompt: "tenant prompt",
		LLMModel: "qwen-plus", MemoryScope: "user",
		MaxIterations: 99, MaxContextTokens: 99999,
		AllowedSkills: []string{"skill-1"}, MCPToolIDs: []string{"mcp-1"},
		KnowledgeWorkspaceIDs:   []string{"knowledge-1"},
		KnowledgeWorkspaceNames: []string{"Knowledge"}, KnowledgeWorkspaceDescriptions: []string{"tenant"},
		Capabilities: []domain.AgentCapability{{Name: "tenant capability"}}, StuckThreshold: 7,
	}

	got, err := ComposeSystemAssistantProfile(want, profile)
	if err != nil {
		t.Fatalf("ComposeSystemAssistantProfile() error = %v", err)
	}
	if got.ID != want.ID || got.LLMModel != want.LLMModel ||
		got.MemoryScope != want.MemoryScope {
		t.Fatalf("tenant runtime selection not preserved: %#v", got)
	}
	if got.Name != profile.Name {
		t.Fatalf("Name not from profile: got %q, want %q", got.Name, profile.Name)
	}
	if got.Description != want.Description {
		t.Fatalf("DB text field Description not preserved: got %q, want %q", got.Description, want.Description)
	}
	if got.SystemPrompt != want.SystemPrompt {
		t.Fatalf("SystemPrompt not preserved from DB: got %q, want %q", got.SystemPrompt, want.SystemPrompt)
	}
	if got.MaxIterations != want.MaxIterations || got.MaxContextTokens != want.MaxContextTokens {
		t.Fatalf("tenant budgets not preserved: got MaxIterations=%d MaxContextTokens=%d, want %d / %d",
			got.MaxIterations, got.MaxContextTokens, want.MaxIterations, want.MaxContextTokens)
	}
	if got.SystemKey != profile.Key || !got.IsSystem || got.ManagementMode != "platform" {
		t.Fatalf("managed identity not composed: %#v", got)
	}
	if len(got.AllowedSkills) != 1 || got.AllowedSkills[0] != "skill-1" {
		t.Fatal("tenant skills not preserved")
	}
	if len(got.MCPToolIDs) != 1 || got.MCPToolIDs[0] != "mcp-1" {
		t.Fatal("tenant MCP tools not preserved")
	}
	if len(got.KnowledgeWorkspaceIDs) != 1 || got.KnowledgeWorkspaceIDs[0] != "knowledge-1" {
		t.Fatal("tenant knowledge workspaces not preserved")
	}
	if len(got.KnowledgeWorkspaceNames) != 0 || len(got.KnowledgeWorkspaceDescriptions) != 0 ||
		len(got.Capabilities) != 0 || got.StuckThreshold != 0 {
		t.Fatalf("unexpected tenant extensions survived composition: %#v", got)
	}
}

func TestComposeSystemAssistantProfileManagedBranchPreservesSamplingFields(t *testing.T) {
	// B1 修复回归:托管分支此前丢弃采样字段(只透传 MaxIterations/
	// MaxContextTokens),落库值在运行时组装时被清零,GET 恒读 0。
	profile := BuiltinSystemAssistantProfile()
	want := &domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey,
		LLMModel:               "qwen-plus",
		Temperature:            0.7,
		MaxTokens:              2048,
		CompactionRecentGroups: 4,
		MemoryParameters:       map[string]any{"memory.recall_top_k": 9},
	}

	got, err := ComposeSystemAssistantProfile(want, profile)
	if err != nil {
		t.Fatalf("ComposeSystemAssistantProfile() error = %v", err)
	}
	if got.Temperature != want.Temperature || got.MaxTokens != want.MaxTokens ||
		got.CompactionRecentGroups != want.CompactionRecentGroups {
		t.Fatalf("managed branch dropped sampling fields: got %#v, want %#v", got, want)
	}
	if got.MemoryParameters["memory.recall_top_k"] != 9 {
		t.Fatalf("managed branch dropped memory parameters: got %#v", got.MemoryParameters)
	}
	got.MemoryParameters["memory.recall_top_k"] = 3
	if want.MemoryParameters["memory.recall_top_k"] != 9 {
		t.Fatal("managed branch retained caller memory parameter map")
	}
}

func TestComposeSystemAssistantProfileFailsClosedForInvalidProfile(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *domain.AgentConfig
		profile *domain.SystemAssistantProfile
	}{
		{name: "nil config", profile: BuiltinSystemAssistantProfile()},
		{name: "nil profile", cfg: &domain.AgentConfig{SystemKey: domain.SystemAssistantKey}},
		{
			name: "unknown key", cfg: &domain.AgentConfig{SystemKey: domain.SystemAssistantKey},
			profile: &domain.SystemAssistantProfile{Key: "unknown", Version: "2026-07-23.v1"},
		},
		{
			name: "unknown version", cfg: &domain.AgentConfig{SystemKey: domain.SystemAssistantKey},
			profile: &domain.SystemAssistantProfile{Key: domain.SystemAssistantKey, Version: "unknown"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ComposeSystemAssistantProfile(tt.cfg, tt.profile); err == nil {
				t.Fatal("ComposeSystemAssistantProfile() error = nil")
			}
		})
	}
}

func TestComposeSystemAssistantProfileUsesCodeReviewedVersionInsteadOfMutableInput(t *testing.T) {
	profile := BuiltinSystemAssistantProfile()
	profile.Name = "tampered name"

	got, err := ComposeSystemAssistantProfile(&domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey,
		SystemPrompt: "tenant db prompt",
	}, profile)
	if err != nil {
		t.Fatalf("ComposeSystemAssistantProfile() error = %v", err)
	}
	want := BuiltinSystemAssistantProfile()
	if got.Name != want.Name {
		t.Fatalf("mutable profile name reached runtime: %#v", got)
	}
	if got.SystemPrompt != "tenant db prompt" {
		t.Fatalf("system prompt must come from DB field, got %q", got.SystemPrompt)
	}
}

type systemAssistantProfileRepo struct {
	cfgs []*domain.AgentConfig
	err  error
}

func (r systemAssistantProfileRepo) Register(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _ []string) error {
	return nil
}
func (r systemAssistantProfileRepo) Get(context.Context, string) (*domain.AgentConfig, bool, error) {
	if r.err != nil {
		return nil, false, r.err
	}
	if len(r.cfgs) == 0 {
		return nil, false, nil
	}
	return r.cfgs[0], true, nil
}
func (r systemAssistantProfileRepo) GetSystemAssistant(ctx context.Context) (*domain.AgentConfig, bool, error) {
	return r.Get(ctx, domain.SystemAssistantKey)
}
func (r systemAssistantProfileRepo) GetAll(context.Context) ([]*domain.AgentConfig, error) {
	return r.cfgs, r.err
}
func (r systemAssistantProfileRepo) Update(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _ string, _ bool) error {
	return nil
}
func (r systemAssistantProfileRepo) UpdateSystemAssistantModel(_ context.Context, _ string, _ string, _ int, _ int, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	return nil, nil
}
func (r systemAssistantProfileRepo) UpdateSystemAssistantAll(_ context.Context, _ string, _ string, _ int, _ int, _ int, _ map[string]any, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	return nil, nil
}
func (r systemAssistantProfileRepo) Remove(_ context.Context, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r systemAssistantProfileRepo) UpdateSystemAssistantBindings(context.Context, []string, []string, []string) (*domain.AgentConfig, error) {
	return nil, nil
}

var _ port.AgentRepo = systemAssistantProfileRepo{}

type systemAssistantPromptGateway struct {
	request port.CapabilityRequest
}

func (g *systemAssistantPromptGateway) Route(
	_ context.Context, request port.CapabilityRequest,
) (port.CapabilityResponse, error) {
	g.request = request
	return port.CapabilityResponse{Content: "done"}, nil
}

func TestSystemAssistantProfileRegistryPropagatesRepositoryAndCompositionFailures(t *testing.T) {
	wantErr := errors.New("repository unavailable")
	registry := NewRegistry(systemAssistantProfileRepo{err: wantErr}, BuiltinSystemAssistantProfileSource(), zap.NewNop())
	if _, _, err := registry.Get(context.Background(), "assistant-1"); !errors.Is(err, wantErr) {
		t.Fatalf("Registry.Get() error = %v, want %v", err, wantErr)
	}
	if _, err := registry.GetAll(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Registry.GetAll() error = %v, want %v", err, wantErr)
	}

	registry = NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey,
	}}}, nil, zap.NewNop())
	if _, _, err := registry.Get(context.Background(), "assistant-1"); err == nil {
		t.Fatal("Registry.Get() composition error = nil")
	}
	if _, err := registry.GetAll(context.Background()); err == nil {
		t.Fatal("Registry.GetAll() composition error = nil")
	}
}

func TestSystemAssistantProfileAgentServicePropagatesRegistryFailures(t *testing.T) {
	wantErr := errors.New("repository unavailable")
	registry := NewRegistry(systemAssistantProfileRepo{err: wantErr}, BuiltinSystemAssistantProfileSource(), zap.NewNop())
	svc := NewAgentService(AgentServiceDeps{Registry: registry})

	if _, err := svc.Get(context.Background(), "assistant-1"); !errors.Is(err, wantErr) {
		t.Fatalf("AgentService.Get() error = %v, want %v", err, wantErr)
	}
	if _, err := svc.List(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("AgentService.List() error = %v, want %v", err, wantErr)
	}
}

func TestSystemAssistantProfileVersionRecordedInTraceMetadata(t *testing.T) {
	source := BuiltinSystemAssistantProfileSource()
	svc := NewAgentService(AgentServiceDeps{
		Registry: NewRegistry(nil, source, zap.NewNop()), TenantModelValidator: &strictModelValidatorStub{},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model", MaxIterations: 3,
	}}

	_, options, err := svc.assembleOptions(
		context.Background(), agent, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	if err != nil {
		t.Fatalf("assembleOptions() error = %v", err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if got := cfg.EvolutionTrace.ResourceManifest["system-assistant-profile"]; got != source.Version() {
		t.Fatalf("profile version = %q, want %q", got, source.Version())
	}
}

func TestSystemAssistantProfileTraceFailsClosedWithoutSharedSource(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		Registry: NewRegistry(nil, nil, zap.NewNop()), TenantModelValidator: &strictModelValidatorStub{},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "assistant-1", SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model", MaxIterations: 3,
	}}

	if _, _, err := svc.assembleOptions(
		context.Background(), agent, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	); err == nil {
		t.Fatal("assembleOptions() error = nil without shared profile source")
	}
}

func TestSystemAssistantProfileManagedRuntimeAppendsGlobalSuffix(t *testing.T) {
	source, err := NewBuiltinSystemAssistantProfileSource(domain.CurrentSystemAssistantProfileVersion)
	if err != nil {
		t.Fatalf("NewBuiltinSystemAssistantProfileSource() error = %v", err)
	}
	registry := NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{
		{ID: "assistant-1", SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model"},
	}}, source, zap.NewNop())
	registry.SetPlatformPromptResolver(stubPromptResolver{value: "tenant-global-suffix"})

	agent, found, err := registry.Get(context.Background(), "assistant-1")
	if err != nil || !found {
		t.Fatalf("Registry.Get() found = %v, error = %v", found, err)
	}
	base := agent.(*BaseAgent)
	if base.PlatformPromptResolver == nil {
		t.Fatal("managed agent must carry platform prompt resolver (Execute 对所有 agent 统一追加全局后缀)")
	}
	managedGateway := &systemAssistantPromptGateway{}
	base.SetCapGateway(managedGateway)
	// 显式窗口：默认 fallback 8000 在账本下 usable 为 0，初始组装退化为
	// 最小 head（system 截到 200t + 输入）；显式 30000 窗口下完整提示
	// 直达 LLM，后缀断言更有意义。
	if _, err := base.Execute(context.Background(), "help",
		WithMaxContextTokens(30000)); err != nil {
		t.Fatalf("managed Execute() error = %v", err)
	}
	if got := managedGateway.request.LLM.Messages[0].Content; !strings.Contains(got, "tenant-global-suffix") {
		t.Fatalf("managed effective prompt omits global suffix: %q", got)
	}

	registry = NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{
		{ID: "agent-1", SystemPrompt: "tenant prompt"},
	}}, source, zap.NewNop())
	registry.SetPlatformPromptResolver(stubPromptResolver{value: "tenant-global-suffix"})
	agent, found, err = registry.Get(context.Background(), "agent-1")
	if err != nil || !found {
		t.Fatalf("Registry.Get() found = %v, error = %v", found, err)
	}
	if got := agent.(*BaseAgent).PlatformPromptResolver; got == nil {
		t.Fatal("ordinary agent must carry platform prompt resolver")
	}
	ordinary := agent.(*BaseAgent)
	ordinaryGateway := &systemAssistantPromptGateway{}
	ordinary.SetCapGateway(ordinaryGateway)
	if _, err := ordinary.Execute(context.Background(), "help",
		WithMaxContextTokens(30000)); err != nil {
		t.Fatalf("ordinary Execute() error = %v", err)
	}
	if got := ordinaryGateway.request.LLM.Messages[0].Content; !strings.Contains(got, "tenant-global-suffix") {
		t.Fatalf("ordinary effective prompt omits global suffix: %q", got)
	}
}

func TestSystemAssistantProfileRollbackSourceKeepsRuntimeAndTraceOnSameImmutableVersion(t *testing.T) {
	const rollbackVersion = "2026-07-22.v0"
	source, err := NewBuiltinSystemAssistantProfileSource(rollbackVersion)
	if err != nil {
		t.Fatalf("NewBuiltinSystemAssistantProfileSource() error = %v", err)
	}
	snapshot := source.Profile()
	snapshot.Version = "mutated"

	registry := NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{
		{ID: "assistant-1", SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model"},
	}}, source, zap.NewNop())
	agent, found, err := registry.Get(context.Background(), "assistant-1")
	if err != nil || !found {
		t.Fatalf("Registry.Get() found = %v, error = %v", found, err)
	}
	if agent.GetConfig().SystemPrompt == "mutated prompt" {
		t.Fatal("caller mutation changed composed runtime prompt")
	}

	svc := NewAgentService(AgentServiceDeps{Registry: registry, TenantModelValidator: &strictModelValidatorStub{}})
	_, options, err := svc.assembleOptions(
		context.Background(), agent, ExecRequest{}, ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1",
	)
	if err != nil {
		t.Fatalf("assembleOptions() error = %v", err)
	}
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	if got := cfg.EvolutionTrace.ResourceManifest["system-assistant-profile"]; got != rollbackVersion {
		t.Fatalf("trace profile version = %q, runtime source version = %q", got, rollbackVersion)
	}
}

func TestSystemAssistantProfileKeepsHistoricalVersions(t *testing.T) {
	profiles := BuiltinSystemAssistantProfiles()
	require.Contains(t, profiles, "2026-07-22.v0")
	require.Contains(t, profiles, "2026-08-04.v2")
	old, err := NewBuiltinSystemAssistantProfileSource("2026-08-04.v2")
	require.NoError(t, err)
	// 提示词由内置 agent 实例 DB 字段承载；profile 仅保留版本化元数据。
	require.Equal(t, "2026-08-04.v2", old.Profile().Version)
	// The current version resolves through the active constant.
	current := BuiltinSystemAssistantProfiles()[domain.CurrentSystemAssistantProfileVersion]
	require.Equal(t, "2026-08-08.v3", current.Version)
}
